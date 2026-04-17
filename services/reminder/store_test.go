package reminder

import (
	"context"
	"errors"
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
		ChatID:         "-100123",
		UserID:         "42",
		UserName:       "Doan",
		OriginalMsg:    "2h nữa nhắc tao ăn cơm",
		OriginalMsgID:  777,
		Task:           "ăn cơm",
		FireAt:         fireAt.UTC(),
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
	if err := store.Cancel(ctx, canceledID, "42"); err != nil {
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

	if err := store.Cancel(ctx, 9999, "42"); !errors.Is(err, ErrNotFound) {
		t.Errorf("cancel missing: want ErrNotFound, got %v", err)
	}

	if err := store.MarkFired(ctx, id, now); err != nil {
		t.Fatalf("MarkFired: %v", err)
	}
	if err := store.Cancel(ctx, id, "42"); !errors.Is(err, ErrAlreadyFired) {
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
