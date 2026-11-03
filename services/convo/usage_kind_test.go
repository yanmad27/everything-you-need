package convo

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"everything-you-need/m/services/chat"
)

func recKind(t *testing.T, s *Store, c *tclock, kind string, total int) {
	t.Helper()
	c.set(time.Date(2026, 10, 1, 12, 0, 0, 0, ictZ))
	s.RecordMessage(context.Background(), chat.MessageEvent{Platform: "zalo", ChatID: "c", UserID: "u", Model: "m", Kind: kind,
		Rounds: 1, PromptTokens: total, Total: total})
	if err := s.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestUsageKindSplit(t *testing.T) {
	s, c := usageStore(t, Options{})
	ctx := context.Background()
	recKind(t, s, c, "", 100) // default = chat
	recKind(t, s, c, chat.UsageKindChat, 200)
	recKind(t, s, c, chat.UsageKindMemory, 40)
	recKind(t, s, c, "bogus", 1000) // unknown kinds are stored as chat

	all, _ := s.UsageTotals(ctx, UsageFilter{})
	mem, _ := s.UsageTotals(ctx, UsageFilter{Kind: chat.UsageKindMemory})
	ch, _ := s.UsageTotals(ctx, UsageFilter{Kind: chat.UsageKindChat})
	if all.TotalTokens != 1340 || mem.TotalTokens != 40 || mem.Turns != 1 || ch.TotalTokens != 1300 || ch.Turns != 3 {
		t.Fatalf("all=%+v mem=%+v chat=%+v", all, mem, ch)
	}
	days, _ := s.UsageByDay(ctx, UsageFilter{Kind: chat.UsageKindMemory})
	if len(days) != 1 || days[0].TotalTokens != 40 {
		t.Fatalf("%+v", days)
	}
}

func TestUsageKindMigrationKeepsLegacyRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE usage (id INTEGER PRIMARY KEY AUTOINCREMENT, turn_id TEXT NOT NULL DEFAULT '', platform TEXT NOT NULL, chat_id TEXT NOT NULL,
			user_id TEXT NOT NULL DEFAULT '', rounds INTEGER NOT NULL DEFAULT 0, prompt_tokens INTEGER NOT NULL DEFAULT 0,
			completion_tokens INTEGER NOT NULL DEFAULT 0, total_tokens INTEGER NOT NULL DEFAULT 0, model TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL)`,
		`INSERT INTO usage(platform,chat_id,rounds,prompt_tokens,total_tokens,model,created_at) VALUES('zalo','c',1,70,70,'m',1)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	for i := 0; i < 2; i++ { // second open: migration is idempotent
		s, err := Open(path, Options{NoBackground: true})
		if err != nil {
			t.Fatal(err)
		}
		ch, _ := s.UsageTotals(context.Background(), UsageFilter{Kind: chat.UsageKindChat})
		all, _ := s.UsageTotals(context.Background(), UsageFilter{})
		if ch.TotalTokens != 70 || all.TotalTokens != 70 {
			t.Fatalf("legacy row lost: chat=%+v all=%+v", ch, all)
		}
		s.Close()
	}
}
