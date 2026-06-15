package news

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// Store tracks which article URLs have already been posted, so the daily
// digest never repeats a story within the dedup window.
type Store struct {
	db *sql.DB
}

func OpenStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS posted_news (
		url       TEXT PRIMARY KEY,
		title     TEXT NOT NULL,
		posted_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate posted_news: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// FilterUnseen returns the subset of items whose URL has not been posted since
// cutoff.
func (s *Store) FilterUnseen(ctx context.Context, items []FeedItem, cutoff time.Time) ([]FeedItem, error) {
	var out []FeedItem
	for _, item := range items {
		var posted int
		err := s.db.QueryRowContext(ctx,
			`SELECT 1 FROM posted_news WHERE url = ? AND posted_at >= ?`,
			item.URL, cutoff.UTC()).Scan(&posted)
		if err == sql.ErrNoRows {
			out = append(out, item)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("filter unseen: %w", err)
		}
		// already posted within window → skip
	}
	return out, nil
}

// MarkPosted records that these URLs were posted now.
func (s *Store) MarkPosted(ctx context.Context, items []FeedItem) error {
	for _, item := range items {
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO posted_news (url, title, posted_at) VALUES (?, ?, ?)
			 ON CONFLICT(url) DO UPDATE SET posted_at = excluded.posted_at`,
			item.URL, item.Title, time.Now().UTC()); err != nil {
			return fmt.Errorf("mark posted: %w", err)
		}
	}
	return nil
}

// PurgeOld deletes records older than cutoff to keep the table small.
func (s *Store) PurgeOld(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM posted_news WHERE posted_at < ?`, cutoff.UTC())
	if err != nil {
		return 0, fmt.Errorf("purge posted_news: %w", err)
	}
	count, _ := res.RowsAffected()
	return count, nil
}
