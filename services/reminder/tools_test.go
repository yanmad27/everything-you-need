package reminder

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCreateForValidationCapScoping(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t, &stubParser{}, &stubSender{})
	future := svc.now().Add(time.Hour)

	if _, err := svc.CreateFor(ctx, "zalo", "c1", "u", "n", "  ", future); err == nil {
		t.Error("empty task accepted")
	}
	if _, err := svc.CreateFor(ctx, "zalo", "c1", "u", "n", strings.Repeat("x", 501), future); err == nil {
		t.Error("long task accepted")
	}
	if _, err := svc.CreateFor(ctx, "zalo", "c1", "u", "n", "t", svc.now().Add(-time.Minute)); err == nil {
		t.Error("past time accepted")
	}

	id, err := svc.CreateFor(ctx, "zalo", "c1", "u", "n", "ăn cơm", future)
	if err != nil || id == 0 {
		t.Fatalf("id=%d err=%v", id, err)
	}
	for i := 1; i < 50; i++ {
		if _, err := svc.CreateFor(ctx, "zalo", "c1", "u", "n", fmt.Sprint("t", i), future); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}
	if _, err := svc.CreateFor(ctx, "zalo", "c1", "u", "n", "over", future); !errors.Is(err, ErrTooMany) {
		t.Fatalf("err=%v want ErrTooMany", err)
	}
	// Other chat / platform unaffected by the cap.
	if _, err := svc.CreateFor(ctx, "zalo", "c2", "u", "n", "x", future); err != nil {
		t.Fatal(err)
	}
	tid, err := svc.CreateFor(ctx, "", "c1", "u", "n", "tg", future)
	if err != nil {
		t.Fatal(err)
	}

	if l, _ := svc.ListFor(ctx, "zalo", "c1"); len(l) != 50 {
		t.Errorf("zalo c1 list=%d", len(l))
	}
	if l, _ := svc.ListFor(ctx, "zalo", "c2"); len(l) != 1 {
		t.Errorf("zalo c2 list=%d", len(l))
	}
	if l, _ := svc.ListFor(ctx, "telegram", "c1"); len(l) != 1 || l[0].ID != tid {
		t.Errorf("telegram list=%+v", l)
	}

	if err := svc.CancelFor(ctx, "zalo", "c2", id); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-chat cancel err=%v", err)
	}
	if err := svc.CancelFor(ctx, "telegram", "c1", id); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-platform cancel err=%v", err)
	}
	if err := svc.CancelFor(ctx, "zalo", "c1", id); err != nil {
		t.Errorf("cancel err=%v", err)
	}
	if l, _ := svc.ListFor(ctx, "zalo", "c1"); len(l) != 49 {
		t.Errorf("after cancel list=%d", len(l))
	}
}

func TestCancelForStrictScopeAllPlatforms(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t, &stubParser{}, &stubSender{})
	future := svc.now().Add(time.Hour)
	id, err := svc.CreateFor(ctx, "telegram", "c1", "u", "n", "x", future)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.CancelFor(ctx, "telegram", "other", id); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-chat telegram cancel err=%v", err)
	}
	if err := svc.CancelFor(ctx, "telegram", "c1", 9999); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing err=%v", err)
	}
	if l, _ := svc.ListFor(ctx, "telegram", "c1"); len(l) != 1 {
		t.Fatalf("reminder was cancelled: %d", len(l))
	}
	if err := svc.CancelFor(ctx, "telegram", "c1", id); err != nil {
		t.Errorf("own cancel err=%v", err)
	}
}

func TestCreateForConcurrentCapPlatformHorizonNormalize(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t, &stubParser{}, &stubSender{})
	future := svc.now().Add(time.Hour)
	var wg sync.WaitGroup
	var ok, many int32
	for i := 0; i < 80; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.CreateFor(ctx, "zalo", "c", "u", "n", "t", future)
			switch {
			case err == nil:
				atomic.AddInt32(&ok, 1)
			case errors.Is(err, ErrTooMany):
				atomic.AddInt32(&many, 1)
			default:
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if ok != 50 || many != 30 {
		t.Fatalf("ok=%d many=%d", ok, many)
	}
	if _, err := svc.CreateFor(ctx, "slack", "c", "u", "n", "t", future); err == nil {
		t.Error("bad platform accepted")
	}
	if _, err := svc.CreateFor(ctx, "telegram", "c", "u", "n", "t", svc.now().Add(400*24*time.Hour)); err == nil {
		t.Error(">1y accepted")
	}
	id, err := svc.CreateFor(ctx, "telegram", "c", "u", "n", " a\nb\r\n c ", future)
	if err != nil {
		t.Fatal(err)
	}
	l, _ := svc.ListFor(ctx, "telegram", "c")
	for _, r := range l {
		if r.ID == id && r.Task != "a b c" {
			t.Errorf("task=%q", r.Task)
		}
	}
}
