// Package note stores short per-chat notes (SQLite). A note belongs to exactly
// one (platform, chat_id); every method takes the scope explicitly and filters
// on it, and users only ever see the per-chat sequence number, never the rowid.
package note

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	_ "modernc.org/sqlite"
)

// Limits.
const (
	MaxBodyRunes   = 4000
	MaxTitleRunes  = 100
	MaxPerChat     = 200
	MaxResults     = 20
	SnippetRunes   = 200
	maxQueryRunes  = 100
	maxQueryTokens = 8
)

var (
	ErrNotFound  = errors.New("note: not found")
	ErrTooMany   = errors.New("note: too many notes in this chat")
	ErrEmpty     = errors.New("note: empty body")
	ErrBodyLong  = errors.New("note: body too long")
	ErrTitleLong = errors.New("note: title too long")
	ErrScope     = errors.New("note: empty platform or chat")
)

// Note is one stored note. ID is the per-chat sequence number.
type Note struct {
	ID         int
	Platform   string
	ChatID     string
	Title      string
	Body       string
	AuthorID   string
	AuthorName string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// ChatSummary is one chat that has notes (admin view).
type ChatSummary struct {
	Platform  string
	ChatID    string
	Count     int
	UpdatedAt time.Time
}

type Store struct {
	db  *sql.DB
	now func() time.Time
}

// Open opens (creating if needed) the notes database. The migration is
// idempotent.
func Open(path string) (*Store, error) {
	// Notes are private: the file is always 0600 (SQLite gives -wal/-shm the same mode).
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("note: create dir: %w", err)
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("note: create db file: %w", err)
	}
	_ = f.Close()
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, fmt.Errorf("note: chmod db file: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("note: open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db, now: time.Now}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// SetClock replaces the clock (tests).
func (s *Store) SetClock(now func() time.Time) { s.now = now }

func migrate(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS notes (
			rowid_pk    INTEGER PRIMARY KEY AUTOINCREMENT,
			platform    TEXT NOT NULL,
			chat_id     TEXT NOT NULL,
			seq         INTEGER NOT NULL,
			title       TEXT NOT NULL DEFAULT '',
			body        TEXT NOT NULL,
			author_id   TEXT NOT NULL DEFAULT '',
			author_name TEXT NOT NULL DEFAULT '',
			created_at  INTEGER NOT NULL,
			updated_at  INTEGER NOT NULL,
			UNIQUE (platform, chat_id, seq)
		)`,
		// Sequence numbers are never reused after a delete, so a stale
		// "xoá note 3" cannot hit a different, newer note.
		`CREATE TABLE IF NOT EXISTS note_counters (
			platform TEXT NOT NULL,
			chat_id  TEXT NOT NULL,
			next_seq INTEGER NOT NULL,
			PRIMARY KEY (platform, chat_id)
		)`,
	}
	for _, st := range stmts {
		if _, err := db.Exec(st); err != nil {
			return fmt.Errorf("note: migrate: %w", err)
		}
	}
	return nil
}

func normTitle(t string) string { return strings.Join(strings.Fields(t), " ") }

func checkScope(platform, chatID string) error {
	if platform == "" || chatID == "" {
		return ErrScope
	}
	return nil
}

func validate(title, body string) error {
	if strings.TrimSpace(body) == "" {
		return ErrEmpty
	}
	if utf8.RuneCountInString(body) > MaxBodyRunes {
		return ErrBodyLong
	}
	if utf8.RuneCountInString(title) > MaxTitleRunes {
		return ErrTitleLong
	}
	return nil
}

// Create stores a note and returns its per-chat sequence number.
func (s *Store) Create(ctx context.Context, platform, chatID, authorID, authorName, title, body string) (int, error) {
	if err := checkScope(platform, chatID); err != nil {
		return 0, err
	}
	title, body = normTitle(title), strings.TrimSpace(body)
	if err := validate(title, body); err != nil {
		return 0, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM notes WHERE platform=? AND chat_id=?`, platform, chatID).Scan(&n); err != nil {
		return 0, err
	}
	if n >= MaxPerChat {
		return 0, ErrTooMany
	}
	seq := 1
	err = tx.QueryRowContext(ctx, `SELECT next_seq FROM note_counters WHERE platform=? AND chat_id=?`, platform, chatID).Scan(&seq)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	if errors.Is(err, sql.ErrNoRows) {
		// Fresh counter; start above any pre-existing row for safety.
		_ = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq),0)+1 FROM notes WHERE platform=? AND chat_id=?`, platform, chatID).Scan(&seq)
	}
	ts := s.now().UnixMilli()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO notes (platform, chat_id, seq, title, body, author_id, author_name, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?)`,
		platform, chatID, seq, title, body, authorID, authorName, ts, ts); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO note_counters (platform, chat_id, next_seq) VALUES (?,?,?)
		 ON CONFLICT(platform, chat_id) DO UPDATE SET next_seq=excluded.next_seq`, platform, chatID, seq+1); err != nil {
		return 0, err
	}
	return seq, tx.Commit()
}

const cols = `seq, platform, chat_id, title, body, author_id, author_name, created_at, updated_at`

type scanner interface{ Scan(...any) error }

func scanNote(r scanner) (Note, error) {
	var n Note
	var c, u int64
	if err := r.Scan(&n.ID, &n.Platform, &n.ChatID, &n.Title, &n.Body, &n.AuthorID, &n.AuthorName, &c, &u); err != nil {
		return Note{}, err
	}
	n.CreatedAt, n.UpdatedAt = time.UnixMilli(c), time.UnixMilli(u)
	return n, nil
}

// Get returns note seq of exactly this chat.
func (s *Store) Get(ctx context.Context, platform, chatID string, seq int) (Note, error) {
	if err := checkScope(platform, chatID); err != nil {
		return Note{}, err
	}
	n, err := scanNote(s.db.QueryRowContext(ctx, `SELECT `+cols+` FROM notes WHERE platform=? AND chat_id=? AND seq=?`, platform, chatID, seq))
	if errors.Is(err, sql.ErrNoRows) {
		return Note{}, ErrNotFound
	}
	return n, err
}

// All returns every note of the chat, oldest sequence first.
func (s *Store) All(ctx context.Context, platform, chatID string) ([]Note, error) {
	if err := checkScope(platform, chatID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+cols+` FROM notes WHERE platform=? AND chat_id=? ORDER BY seq`, platform, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Note
	for rows.Next() {
		n, err := scanNote(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// List returns up to limit (<= MaxResults) most recently created notes of the
// chat, newest first, and the chat's total.
func (s *Store) List(ctx context.Context, platform, chatID string, limit int) ([]Note, int, error) {
	all, err := s.All(ctx, platform, chatID)
	if err != nil {
		return nil, 0, err
	}
	total := len(all)
	if limit <= 0 || limit > MaxResults {
		limit = MaxResults
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].ID > all[j].ID })
	if len(all) > limit {
		all = all[:limit]
	}
	return all, total, nil
}

// Search matches the query's words (case- and diacritic-insensitively) against
// title and body of this chat's notes; best match first, at most limit
// (<= MaxResults) results. A note matches when it contains at least one word;
// more words (and title hits) rank higher.
func (s *Store) Search(ctx context.Context, platform, chatID, query string, limit int) ([]Note, error) {
	all, err := s.All(ctx, platform, chatID)
	if err != nil {
		return nil, err
	}
	if utf8.RuneCountInString(query) > maxQueryRunes {
		query = string([]rune(query)[:maxQueryRunes])
	}
	toks := strings.Fields(Fold(query))
	if len(toks) == 0 {
		return nil, nil
	}
	if len(toks) > maxQueryTokens {
		toks = toks[:maxQueryTokens]
	}
	type hit struct {
		n     Note
		score int
	}
	var hits []hit
	for _, n := range all {
		title, body := Fold(n.Title), Fold(n.Body)
		score := 0
		for _, t := range toks {
			if strings.Contains(title, t) {
				score += 3
			} else if strings.Contains(body, t) {
				score += 2
			}
		}
		if score > 0 {
			hits = append(hits, hit{n, score})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].n.ID > hits[j].n.ID
	})
	if limit <= 0 || limit > MaxResults {
		limit = MaxResults
	}
	var out []Note
	for _, h := range hits {
		if len(out) == limit {
			break
		}
		out = append(out, h.n)
	}
	return out, nil
}

// Update changes a note of this chat. A nil title keeps the current one. With
// appendMode the body is added after the existing text on a new line.
func (s *Store) Update(ctx context.Context, platform, chatID string, seq int, title *string, body string, appendMode bool) error {
	if err := checkScope(platform, chatID); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	cur, err := scanNote(tx.QueryRowContext(ctx, `SELECT `+cols+` FROM notes WHERE platform=? AND chat_id=? AND seq=?`, platform, chatID, seq))
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	newTitle, newBody := cur.Title, cur.Body
	if title != nil {
		newTitle = normTitle(*title)
	}
	body = strings.TrimSpace(body)
	switch {
	case body != "" && appendMode:
		newBody = cur.Body + "\n" + body
	case body != "":
		newBody = body
	case title == nil:
		// Empty body and no new title: nothing to change, never a silent success.
		// (With a title, only the title is updated, in either mode.)
		return ErrEmpty
	}
	if err := validate(newTitle, newBody); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE notes SET title=?, body=?, updated_at=? WHERE platform=? AND chat_id=? AND seq=?`,
		newTitle, newBody, s.now().UnixMilli(), platform, chatID, seq); err != nil {
		return err
	}
	return tx.Commit()
}

// Delete removes note seq of exactly this chat.
func (s *Store) Delete(ctx context.Context, platform, chatID string, seq int) error {
	if err := checkScope(platform, chatID); err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM notes WHERE platform=? AND chat_id=? AND seq=?`, platform, chatID, seq)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Chats lists the chats that have notes, most recently updated first (admin).
func (s *Store) Chats(ctx context.Context) ([]ChatSummary, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT platform, chat_id, COUNT(*), MAX(updated_at) FROM notes GROUP BY platform, chat_id ORDER BY MAX(updated_at) DESC LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChatSummary
	for rows.Next() {
		var c ChatSummary
		var u int64
		if err := rows.Scan(&c.Platform, &c.ChatID, &c.Count, &u); err != nil {
			return nil, err
		}
		c.UpdatedAt = time.UnixMilli(u)
		out = append(out, c)
	}
	return out, rows.Err()
}

// ChatNotes lists all notes of one chat (admin), newest first.
func (s *Store) ChatNotes(ctx context.Context, platform, chatID string) ([]Note, error) {
	all, err := s.All(ctx, platform, chatID)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].ID > all[j].ID })
	return all, nil
}

// Snippet shortens s to at most SnippetRunes runes on one line.
func Snippet(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= SnippetRunes {
		return s
	}
	return string([]rune(s)[:SnippetRunes-1]) + "…"
}

var foldMap = func() map[rune]rune {
	m := map[rune]rune{'đ': 'd', 'Đ': 'd'}
	for base, set := range map[rune]string{
		'a': "àáảãạăằắẳẵặâầấẩẫậ", 'e': "èéẻẽẹêềếểễệ", 'i': "ìíỉĩị",
		'o': "òóỏõọôồốổỗộơờớởỡợ", 'u': "ùúủũụưừứửữự", 'y': "ỳýỷỹỵ",
	} {
		for _, r := range set {
			m[r] = base
			m[[]rune(strings.ToUpper(string(r)))[0]] = base
		}
	}
	return m
}()

// Fold lowercases and strips Vietnamese diacritics so "Mật khẩu" matches "mat khau".
func Fold(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if f, ok := foldMap[r]; ok {
			b.WriteRune(f)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
