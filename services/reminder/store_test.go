package reminder

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func sampleReminder(fireAt time.Time) Reminder {
	return Reminder{
		ChatID:        "-100123",
		UserID:        "42",
		UserName:      "Doan",
		OriginalMsg:   "2h nữa nhắc tao ăn cơm",
		OriginalMsgID: 777,
		Task:          "ăn cơm",
		FireAt:        fireAt.UTC(),
	}
}

func TestCreateAndGetByID(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	fireAt := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	id, err := store.Create(ctx, sampleReminder(fireAt))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected positive id, got %d", id)
	}

	got, err := store.GetByID(ctx, id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Task != "ăn cơm" {
		t.Errorf("Task: got %q", got.Task)
	}
	if !got.FireAt.Equal(fireAt) {
		t.Errorf("FireAt: got %v want %v", got.FireAt, fireAt)
	}
	if got.FiredAt != nil || got.CanceledAt != nil {
		t.Errorf("new reminder must not be fired or canceled")
	}
	if got.Attempts != 0 {
		t.Errorf("Attempts: got %d want 0", got.Attempts)
	}
}

func TestGetByIDNotFound(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	_, err := store.GetByID(ctx, 9999)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestListPendingFiltersFutureAndFiredAndCanceled(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	now := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)

	pastID, _ := store.Create(ctx, sampleReminder(now.Add(-10*time.Minute)))
	_, _ = store.Create(ctx, sampleReminder(now.Add(10*time.Minute))) // future: excluded
	firedID, _ := store.Create(ctx, sampleReminder(now.Add(-5*time.Minute)))
	canceledID, _ := store.Create(ctx, sampleReminder(now.Add(-1*time.Minute)))

	if err := store.MarkFired(ctx, firedID, now); err != nil {
		t.Fatalf("MarkFired: %v", err)
	}
	if err := store.CancelForChat(ctx, PlatformTelegram, "-100123", canceledID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	pending, err := store.ListPending(ctx, now, 50)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != pastID {
		t.Fatalf("expected only pastID %d pending, got %+v", pastID, pending)
	}
}

func TestListPendingExcludesTooManyAttempts(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	now := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	id, _ := store.Create(ctx, sampleReminder(now.Add(-10*time.Minute)))

	for i := 0; i < 3; i++ {
		if err := store.IncrementAttempts(ctx, id); err != nil {
			t.Fatalf("IncrementAttempts: %v", err)
		}
	}

	pending, err := store.ListPending(ctx, now, 50)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("expected reminder with 3 attempts excluded, got %+v", pending)
	}
}

func TestListForChatReturnsOnlyActiveInChat(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	now := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)

	r1 := sampleReminder(now.Add(1 * time.Hour))
	r1.ChatID = "chatA"
	aID, _ := store.Create(ctx, r1)

	r2 := sampleReminder(now.Add(2 * time.Hour))
	r2.ChatID = "chatB"
	_, _ = store.Create(ctx, r2)

	list, err := store.ListForChat(ctx, "chatA")
	if err != nil {
		t.Fatalf("ListForChat: %v", err)
	}
	if len(list) != 1 || list[0].ID != aID {
		t.Fatalf("expected one reminder for chatA, got %+v", list)
	}
}

func TestCancelErrors(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	now := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)

	id, _ := store.Create(ctx, sampleReminder(now.Add(1*time.Hour)))

	if err := store.CancelForChat(ctx, PlatformTelegram, "-100123", 9999); !errors.Is(err, ErrNotFound) {
		t.Errorf("cancel missing: want ErrNotFound, got %v", err)
	}

	if err := store.MarkFired(ctx, id, now); err != nil {
		t.Fatalf("MarkFired: %v", err)
	}
	if err := store.CancelForChat(ctx, PlatformTelegram, "-100123", id); !errors.Is(err, ErrAlreadyFired) {
		t.Errorf("cancel fired: want ErrAlreadyFired, got %v", err)
	}
}

func TestUpdateDedupe(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	seen, err := store.IsUpdateProcessed(ctx, 100)
	if err != nil || seen {
		t.Fatalf("IsUpdateProcessed(100): seen=%v err=%v, want false/nil", seen, err)
	}

	if err := store.MarkUpdateProcessed(ctx, 100); err != nil {
		t.Fatalf("MarkUpdateProcessed: %v", err)
	}

	seen, err = store.IsUpdateProcessed(ctx, 100)
	if err != nil || !seen {
		t.Fatalf("IsUpdateProcessed(100) after mark: seen=%v err=%v, want true/nil", seen, err)
	}

	// Marking the same update_id twice must be idempotent (no error).
	if err := store.MarkUpdateProcessed(ctx, 100); err != nil {
		t.Errorf("MarkUpdateProcessed duplicate: %v", err)
	}
}

func TestPurgeOld(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	now := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)

	oldFired, _ := store.Create(ctx, sampleReminder(now.Add(-50*24*time.Hour)))
	_ = store.MarkFired(ctx, oldFired, now.Add(-50*24*time.Hour))

	recentFired, _ := store.Create(ctx, sampleReminder(now.Add(-2*24*time.Hour)))
	_ = store.MarkFired(ctx, recentFired, now.Add(-2*24*time.Hour))

	pending, _ := store.Create(ctx, sampleReminder(now.Add(1*time.Hour)))

	cutoff := now.Add(-30 * 24 * time.Hour)
	rCount, uCount, err := store.PurgeOld(ctx, cutoff, cutoff)
	if err != nil {
		t.Fatalf("PurgeOld: %v", err)
	}
	if rCount != 1 {
		t.Errorf("purged reminders: got %d want 1", rCount)
	}
	_ = uCount

	if _, err := store.GetByID(ctx, oldFired); !errors.Is(err, ErrNotFound) {
		t.Errorf("oldFired should be purged, got %v", err)
	}
	if _, err := store.GetByID(ctx, recentFired); err != nil {
		t.Errorf("recentFired should remain: %v", err)
	}
	if _, err := store.GetByID(ctx, pending); err != nil {
		t.Errorf("pending should remain: %v", err)
	}
}

func TestOldSchemaMigratesToTelegramPlatform(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "old.db")

	// A database as created before multi-platform support: no platform column.
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE reminders (
			id INTEGER PRIMARY KEY AUTOINCREMENT, chat_id TEXT NOT NULL, user_id TEXT NOT NULL,
			user_name TEXT NOT NULL, original_msg TEXT NOT NULL, original_msg_id INTEGER NOT NULL,
			task TEXT NOT NULL, fire_at DATETIME NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			fired_at DATETIME, canceled_at DATETIME, attempts INTEGER NOT NULL DEFAULT 0)`,
		`INSERT INTO reminders (chat_id, user_id, user_name, original_msg, original_msg_id, task, fire_at)
		 VALUES ('-100123', '42', 'Doan', 'nhắc tao ăn cơm', 777, 'ăn cơm', '2026-04-17 12:00:00')`,
	} {
		if _, err := old.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	_ = old.Close()

	for i := 0; i < 2; i++ { // second open proves the migration is idempotent
		store, err := OpenStore(path)
		if err != nil {
			t.Fatalf("OpenStore #%d: %v", i, err)
		}
		r, err := store.GetByID(ctx, 1)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if r.Platform != PlatformTelegram {
			t.Errorf("platform = %q, want telegram", r.Platform)
		}
		if i == 0 {
			if list, err := store.ListForChat(ctx, "-100123"); err != nil || len(list) != 1 {
				t.Fatalf("ListForChat: %v %v", list, err)
			}
			// ...and it fires to Telegram, not Zalo.
			tg, zl := &stubSender{}, &stubSender{}
			svc := NewService(store, nil, tg, time.Now).WithSender(PlatformZalo, zl)
			if err := svc.Sweep(ctx, time.Date(2026, 4, 17, 13, 0, 0, 0, time.UTC)); err != nil {
				t.Fatal(err)
			}
			if len(tg.sent) != 1 || tg.sent[0].ChatID != "-100123" || len(zl.sent) != 0 {
				t.Errorf("telegram=%v zalo=%v", tg.sent, zl.sent)
			}
		}
		_ = store.Close()
	}
}

func TestListAndCancelScopedByPlatformAndChat(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	fireAt := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)

	tg := sampleReminder(fireAt)
	tg.ChatID = "same"
	zl := sampleReminder(fireAt)
	zl.ChatID = "same"
	zl.Platform = PlatformZalo
	tgID, _ := store.Create(ctx, tg)
	zlID, _ := store.Create(ctx, zl)

	if l, _ := store.ListForPlatformChat(ctx, PlatformZalo, "same"); len(l) != 1 || l[0].ID != zlID {
		t.Errorf("zalo list = %+v", l)
	}
	if l, _ := store.ListForPlatformChat(ctx, PlatformTelegram, "same"); len(l) != 1 || l[0].ID != tgID {
		t.Errorf("telegram list = %+v", l)
	}
	if err := store.CancelForChat(ctx, PlatformTelegram, "same", zlID); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-platform cancel: %v", err)
	}
	if err := store.CancelForChat(ctx, PlatformZalo, "other", zlID); !errors.Is(err, ErrNotFound) {
		t.Errorf("zalo cross-chat cancel: %v", err)
	}
	// Telegram keeps its historical semantics: any chat may cancel any telegram reminder.
	if err := store.CancelForChat(ctx, PlatformTelegram, "some-other-chat", tgID); err != nil {
		t.Errorf("telegram cross-chat cancel must still work: %v", err)
	}
	if err := store.CancelForChat(ctx, PlatformZalo, "same", zlID); err != nil {
		t.Errorf("own cancel: %v", err)
	}
}
