package convo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Admin credential and audit storage (used by services/admin). Writes are
// synchronous: they are rare and must not be lost or reordered.

// AdminPasswordHash returns the stored bcrypt hash, if any.
func (s *Store) AdminPasswordHash(ctx context.Context) (string, bool, error) {
	var h string
	err := s.db.QueryRowContext(ctx, `SELECT password_hash FROM admin_credentials WHERE id = 1`).Scan(&h)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("convo: read admin credentials: %w", err)
	}
	return h, true, nil
}

// SetAdminPasswordHash stores (replacing) the single credentials row.
func (s *Store) SetAdminPasswordHash(ctx context.Context, username, hash string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO admin_credentials(id, username, password_hash, updated_at) VALUES(1,?,?,?)
		ON CONFLICT(id) DO UPDATE SET username=excluded.username, password_hash=excluded.password_hash, updated_at=excluded.updated_at`,
		username, hash, s.now().UnixMilli())
	if err != nil {
		return fmt.Errorf("convo: store admin credentials: %w", err)
	}
	return nil
}

// ClearAdminPasswordHash removes the stored hash so the env hash applies again.
func (s *Store) ClearAdminPasswordHash(ctx context.Context) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM admin_credentials`)
	if err != nil {
		return false, fmt.Errorf("convo: clear admin credentials: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// RecordAdminAudit appends an audit row (event name and client IP only).
func (s *Store) RecordAdminAudit(ctx context.Context, event, ip string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO admin_audit(at, event, ip) VALUES(?,?,?)`, s.now().UnixMilli(), truncRunes(event, 40), truncRunes(ip, 64))
	if err != nil {
		return fmt.Errorf("convo: admin audit: %w", err)
	}
	return nil
}

// AdminAuditEntry is one audit row.
type AdminAuditEntry struct {
	At    int64
	Event string
	IP    string
}

// AdminAudit returns audit rows, oldest first.
func (s *Store) AdminAudit(ctx context.Context) ([]AdminAuditEntry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT at, event, ip FROM admin_audit ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AdminAuditEntry
	for rows.Next() {
		var e AdminAuditEntry
		if err := rows.Scan(&e.At, &e.Event, &e.IP); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
