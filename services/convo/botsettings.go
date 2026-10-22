package convo

import (
	"context"
	"fmt"
)

// BotSettingRow is one stored per-bot override. Value is JSON for plain
// settings and an opaque ciphertext for secrets (see services/botconfig).
type BotSettingRow struct {
	Platform, Key string
	Value         []byte
	IsSecret      bool
	UpdatedAt     int64
	UpdatedBy     string
}

// ListBotSettings returns every override stored for platform.
func (s *Store) ListBotSettings(ctx context.Context, platform string) ([]BotSettingRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT platform, key, value, is_secret, updated_at, updated_by FROM bot_settings WHERE platform = ? ORDER BY key`, platform)
	if err != nil {
		return nil, fmt.Errorf("convo: list bot settings: %w", err)
	}
	defer rows.Close()
	var out []BotSettingRow
	for rows.Next() {
		var r BotSettingRow
		var sec int
		if err := rows.Scan(&r.Platform, &r.Key, &r.Value, &sec, &r.UpdatedAt, &r.UpdatedBy); err != nil {
			return nil, err
		}
		r.IsSecret = sec != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// ApplyBotSettings upserts puts and deletes the named keys in one transaction,
// writing one admin_audit row per change (event setting_changed / setting_reset,
// detail "platform/key", the actor; never a value). Nothing is written on error.
func (s *Store) ApplyBotSettings(ctx context.Context, platform string, puts []BotSettingRow, deletes []string, actor string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("convo: apply bot settings: %w", err)
	}
	defer tx.Rollback()
	now := s.now().UnixMilli()
	actor = truncRunes(actor, 100)
	for _, r := range puts {
		sec := 0
		if r.IsSecret {
			sec = 1
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO bot_settings(platform,key,value,is_secret,updated_at,updated_by) VALUES(?,?,?,?,?,?)
			ON CONFLICT(platform,key) DO UPDATE SET value=excluded.value, is_secret=excluded.is_secret, updated_at=excluded.updated_at, updated_by=excluded.updated_by`,
			platform, r.Key, r.Value, sec, now, actor); err != nil {
			return fmt.Errorf("convo: apply bot settings: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO admin_audit(at,event,ip,detail,actor) VALUES(?,?,?,?,?)`, now, "setting_changed", "", platform+"/"+r.Key, actor); err != nil {
			return fmt.Errorf("convo: apply bot settings: %w", err)
		}
	}
	for _, k := range deletes {
		res, err := tx.ExecContext(ctx, `DELETE FROM bot_settings WHERE platform = ? AND key = ?`, platform, k)
		if err != nil {
			return fmt.Errorf("convo: apply bot settings: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO admin_audit(at,event,ip,detail,actor) VALUES(?,?,?,?,?)`, now, "setting_reset", "", platform+"/"+k, actor); err != nil {
			return fmt.Errorf("convo: apply bot settings: %w", err)
		}
	}
	return tx.Commit()
}
