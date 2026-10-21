// Package convo persists Zalo conversations, tool calls and LLM usage in
// SQLite so an admin UI can show timelines and insights. Recording is
// fail-open: enqueueing never blocks and never fails a reply.
package convo

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"
)

const (
	maxTextRunes    = 8000
	maxSummaryRunes = 300

	defQueueSize     = 1000
	defBatchSize     = 100
	defFlushEvery    = 200 * time.Millisecond
	defCloseTimeout  = 5 * time.Second
	defRetentionDays = 90
	pruneEvery       = 24 * time.Hour
	dropLogEvery     = time.Minute
)

// Options tunes a Store; zero values use the defaults.
type Options struct {
	RetentionDays int
	QueueSize     int
	BatchSize     int
	FlushEvery    time.Duration
	CloseTimeout  time.Duration
	// Estimated cost per 1000 tokens (0 = show tokens only).
	PricePromptPer1K     float64
	PriceCompletionPer1K float64
	// Now is injectable for tests.
	Now func() time.Time
	// NoBackground disables the writer's periodic prune and drop-log tickers (tests).
	NoBackground bool
}

// Store is both the recorder (write path) and the read-only query API.
type Store struct {
	db   *sql.DB
	opt  Options
	now  func() time.Time
	ch   chan op
	done chan struct{} // closed when the writer has drained
	stop chan struct{} // closed by Close to stop tickers

	mu      sync.RWMutex // guards closed vs. sends on ch
	closed  bool
	dropped atomic.Int64
	wg      sync.WaitGroup
}

// Open opens (creating if needed) the database at path, migrates it, and starts
// the background writer.
func Open(path string, opt Options) (*Store, error) {
	if opt.RetentionDays <= 0 {
		opt.RetentionDays = defRetentionDays
	}
	if opt.QueueSize <= 0 {
		opt.QueueSize = defQueueSize
	}
	if opt.BatchSize <= 0 {
		opt.BatchSize = defBatchSize
	}
	if opt.FlushEvery <= 0 {
		opt.FlushEvery = defFlushEvery
	}
	if opt.CloseTimeout <= 0 {
		opt.CloseTimeout = defCloseTimeout
	}
	if opt.Now == nil {
		opt.Now = time.Now
	}
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("convo: create dir: %w", err)
		}
	}
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(NORMAL)")
	db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, fmt.Errorf("convo: open sqlite: %w", err)
	}
	db.SetMaxOpenConns(4)
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	s := &Store{db: db, opt: opt, now: opt.Now,
		ch: make(chan op, opt.QueueSize), done: make(chan struct{}), stop: make(chan struct{})}
	go s.writer()
	if !opt.NoBackground {
		s.wg.Add(2)
		go s.pruneLoop()
		go s.dropLogLoop()
	}
	return s, nil
}

func migrate(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS chats (
			platform   TEXT NOT NULL,
			chat_id    TEXT NOT NULL,
			chat_type  TEXT NOT NULL DEFAULT '',
			title      TEXT NOT NULL DEFAULT '',
			first_seen INTEGER NOT NULL,
			last_seen  INTEGER NOT NULL,
			PRIMARY KEY (platform, chat_id)
		)`,
		`CREATE TABLE IF NOT EXISTS users (
			platform     TEXT NOT NULL,
			user_id      TEXT NOT NULL,
			display_name TEXT NOT NULL DEFAULT '',
			first_seen   INTEGER NOT NULL,
			last_seen    INTEGER NOT NULL,
			PRIMARY KEY (platform, user_id)
		)`,
		`CREATE TABLE IF NOT EXISTS messages (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			platform    TEXT NOT NULL,
			chat_id     TEXT NOT NULL,
			user_id     TEXT,
			direction   TEXT NOT NULL,
			kind        TEXT NOT NULL,
			text        TEXT NOT NULL DEFAULT '',
			image_mime  TEXT NOT NULL DEFAULT '',
			image_bytes INTEGER NOT NULL DEFAULT 0,
			created_at  INTEGER NOT NULL,
			turn_id     TEXT NOT NULL DEFAULT '',
			error_tag   TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_chat ON messages(platform, chat_id, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_user ON messages(platform, user_id, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_created ON messages(created_at)`,
		`CREATE TABLE IF NOT EXISTS tool_calls (
			id             INTEGER PRIMARY KEY AUTOINCREMENT,
			turn_id        TEXT NOT NULL DEFAULT '',
			platform       TEXT NOT NULL,
			chat_id        TEXT NOT NULL,
			user_id        TEXT NOT NULL DEFAULT '',
			name           TEXT NOT NULL,
			args_summary   TEXT NOT NULL DEFAULT '',
			result_summary TEXT NOT NULL DEFAULT '',
			outcome        TEXT NOT NULL,
			latency_ms     INTEGER NOT NULL DEFAULT 0,
			created_at     INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_tool_calls_chat ON tool_calls(platform, chat_id, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_tool_calls_name ON tool_calls(name, created_at)`,
		`CREATE TABLE IF NOT EXISTS usage (
			id                INTEGER PRIMARY KEY AUTOINCREMENT,
			turn_id           TEXT NOT NULL DEFAULT '',
			platform          TEXT NOT NULL,
			chat_id           TEXT NOT NULL,
			user_id           TEXT NOT NULL DEFAULT '',
			rounds            INTEGER NOT NULL DEFAULT 0,
			prompt_tokens     INTEGER NOT NULL DEFAULT 0,
			completion_tokens INTEGER NOT NULL DEFAULT 0,
			total_tokens      INTEGER NOT NULL DEFAULT 0,
			model             TEXT NOT NULL DEFAULT '',
			created_at        INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_usage_chat ON usage(platform, chat_id, created_at)`,
	}
	for _, st := range stmts {
		if _, err := db.Exec(st); err != nil {
			return fmt.Errorf("convo: migrate: %w", err)
		}
	}
	return nil
}

// Close stops accepting events, flushes the queue (bounded by CloseTimeout) and
// closes the database.
func (s *Store) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	close(s.ch)
	close(s.stop)
	s.mu.Unlock()

	select {
	case <-s.done:
	case <-time.After(s.opt.CloseTimeout):
		log.Printf("convo: close timed out, pending events dropped")
	}
	s.wg.Wait()
	return s.db.Close()
}

// Dropped reports how many events were dropped because the queue was full.
func (s *Store) Dropped() int64 { return s.dropped.Load() }

// Prune deletes rows older than the retention window and returns the number of
// rows removed.
func (s *Store) Prune(ctx context.Context) (int64, error) {
	cut := s.now().Add(-time.Duration(s.opt.RetentionDays) * 24 * time.Hour).UnixMilli()
	var total int64
	for _, q := range []string{
		`DELETE FROM messages WHERE created_at < ?`,
		`DELETE FROM tool_calls WHERE created_at < ?`,
		`DELETE FROM usage WHERE created_at < ?`,
		`DELETE FROM chats WHERE last_seen < ?`,
		`DELETE FROM users WHERE last_seen < ?`,
	} {
		res, err := s.db.ExecContext(ctx, q, cut)
		if err != nil {
			return total, fmt.Errorf("convo: prune: %w", err)
		}
		n, _ := res.RowsAffected()
		total += n
	}
	return total, nil
}

func (s *Store) pruneLoop() {
	defer s.wg.Done()
	t := time.NewTicker(pruneEvery)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			n, err := s.Prune(ctx)
			cancel()
			if err != nil {
				log.Printf("convo: prune failed")
			} else {
				log.Printf("convo: pruned rows=%d", n)
			}
		}
	}
}

func (s *Store) dropLogLoop() {
	defer s.wg.Done()
	t := time.NewTicker(dropLogEvery)
	defer t.Stop()
	var last int64
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			if d := s.dropped.Load(); d != last {
				log.Printf("convo: dropped events total=%d (+%d)", d, d-last)
				last = d
			}
		}
	}
}
