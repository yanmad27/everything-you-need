// Package reminder persists and schedules Vietnamese natural-language reminders.
package reminder

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

var (
	ErrNotFound     = errors.New("reminder: not found")
	ErrAlreadyFired = errors.New("reminder: already fired")
)

// maxAttempts is the retry ceiling used by ListPending.
const maxAttempts = 3

type Reminder struct {
	ID             int64
	ChatID         string
	UserID         string
	UserName       string
	OriginalMsg    string
	OriginalMsgID  int64
	Task           string
	FireAt         time.Time
	CreatedAt      time.Time
	FiredAt        *time.Time
	CanceledAt     *time.Time
	Attempts       int
}

type Store struct {
	db *sql.DB
}

func OpenStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)

	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func migrate(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS reminders (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id         TEXT NOT NULL,
			user_id         TEXT NOT NULL,
			user_name       TEXT NOT NULL,
			original_msg    TEXT NOT NULL,
			original_msg_id INTEGER NOT NULL,
			task            TEXT NOT NULL,
			fire_at         DATETIME NOT NULL,
			created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			fired_at        DATETIME,
			canceled_at     DATETIME,
			attempts        INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_reminders_pending
			ON reminders(fire_at)
			WHERE fired_at IS NULL AND canceled_at IS NULL`,
		`CREATE TABLE IF NOT EXISTS processed_updates (
			update_id   INTEGER PRIMARY KEY,
			received_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	return nil
}

func (s *Store) Create(ctx context.Context, r Reminder) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO reminders
		  (chat_id, user_id, user_name, original_msg, original_msg_id, task, fire_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		r.ChatID, r.UserID, r.UserName, r.OriginalMsg, r.OriginalMsgID, r.Task, r.FireAt.UTC(),
	)
	if err != nil {
		return 0, fmt.Errorf("insert reminder: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("last insert id: %w", err)
	}
	return id, nil
}

func (s *Store) GetByID(ctx context.Context, id int64) (*Reminder, error) {
	row := s.db.QueryRowContext(ctx, selectReminderBase+` WHERE id = ?`, id)
	r, err := scanReminder(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return r, nil
}

func (s *Store) ListPending(ctx context.Context, now time.Time, limit int) ([]Reminder, error) {
	rows, err := s.db.QueryContext(ctx, selectReminderBase+`
		WHERE fired_at IS NULL
		  AND canceled_at IS NULL
		  AND attempts < ?
		  AND fire_at <= ?
		ORDER BY fire_at ASC
		LIMIT ?`, maxAttempts, now.UTC(), limit)
	if err != nil {
		return nil, fmt.Errorf("query pending: %w", err)
	}
	defer rows.Close()
	return scanReminders(rows)
}

func (s *Store) ListForChat(ctx context.Context, chatID string) ([]Reminder, error) {
	rows, err := s.db.QueryContext(ctx, selectReminderBase+`
		WHERE chat_id = ?
		  AND fired_at IS NULL
		  AND canceled_at IS NULL
		ORDER BY fire_at ASC`, chatID)
	if err != nil {
		return nil, fmt.Errorf("query chat: %w", err)
	}
	defer rows.Close()
	return scanReminders(rows)
}

func (s *Store) Cancel(ctx context.Context, id int64, _ string) error {
	existing, err := s.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if existing.FiredAt != nil {
		return ErrAlreadyFired
	}
	if existing.CanceledAt != nil {
		return nil
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE reminders SET canceled_at = ? WHERE id = ? AND fired_at IS NULL`,
		time.Now().UTC(), id)
	if err != nil {
		return fmt.Errorf("cancel: %w", err)
	}
	return nil
}

func (s *Store) MarkFired(ctx context.Context, id int64, at time.Time) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE reminders SET fired_at = ? WHERE id = ? AND fired_at IS NULL`,
		at.UTC(), id)
	if err != nil {
		return fmt.Errorf("mark fired: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) IncrementAttempts(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE reminders SET attempts = attempts + 1 WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("increment attempts: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) IsUpdateProcessed(ctx context.Context, updateID int64) (bool, error) {
	var exists int
	err := s.db.QueryRowContext(ctx,
		`SELECT 1 FROM processed_updates WHERE update_id = ?`, updateID,
	).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("is update processed: %w", err)
	}
	return true, nil
}

func (s *Store) MarkUpdateProcessed(ctx context.Context, updateID int64) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO processed_updates (update_id) VALUES (?)`, updateID)
	if err != nil {
		return fmt.Errorf("mark update processed: %w", err)
	}
	return nil
}

// PurgeOld deletes reminders whose fired_at or canceled_at is before remindersCutoff,
// and processed_updates rows received before updatesCutoff.
func (s *Store) PurgeOld(ctx context.Context, remindersCutoff, updatesCutoff time.Time) (int64, int64, error) {
	res, err := s.db.ExecContext(ctx, `
		DELETE FROM reminders
		WHERE (fired_at IS NOT NULL AND fired_at < ?)
		   OR (canceled_at IS NOT NULL AND canceled_at < ?)`,
		remindersCutoff.UTC(), remindersCutoff.UTC())
	if err != nil {
		return 0, 0, fmt.Errorf("purge reminders: %w", err)
	}
	rCount, _ := res.RowsAffected()

	res, err = s.db.ExecContext(ctx,
		`DELETE FROM processed_updates WHERE received_at < ?`, updatesCutoff.UTC())
	if err != nil {
		return rCount, 0, fmt.Errorf("purge processed_updates: %w", err)
	}
	uCount, _ := res.RowsAffected()
	return rCount, uCount, nil
}

const selectReminderBase = `
	SELECT id, chat_id, user_id, user_name, original_msg, original_msg_id,
	       task, fire_at, created_at, fired_at, canceled_at, attempts
	FROM reminders`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanReminder(row rowScanner) (*Reminder, error) {
	var r Reminder
	var fireAt, createdAt time.Time
	var firedAt, canceledAt sql.NullTime
	err := row.Scan(
		&r.ID, &r.ChatID, &r.UserID, &r.UserName, &r.OriginalMsg, &r.OriginalMsgID,
		&r.Task, &fireAt, &createdAt, &firedAt, &canceledAt, &r.Attempts,
	)
	if err != nil {
		return nil, err
	}
	r.FireAt = fireAt.UTC()
	r.CreatedAt = createdAt.UTC()
	if firedAt.Valid {
		t := firedAt.Time.UTC()
		r.FiredAt = &t
	}
	if canceledAt.Valid {
		t := canceledAt.Time.UTC()
		r.CanceledAt = &t
	}
	return &r, nil
}

func scanReminders(rows *sql.Rows) ([]Reminder, error) {
	var out []Reminder
	for rows.Next() {
		r, err := scanReminder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}
