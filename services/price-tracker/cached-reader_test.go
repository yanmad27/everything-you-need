package pricetracker

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCachedReaderUnknownSourceAndSources(t *testing.T) {
	c := newCachedPriceReader([]string{"A", "B"}, func(string) ([]Price, error) { return nil, nil }, time.Minute)
	if _, _, err := c.Get(context.Background(), "nope"); !errors.Is(err, ErrUnknownSource) {
		t.Fatalf("err=%v want ErrUnknownSource", err)
	}
	if got := c.Sources(); len(got) != 2 || got[0] != "A" || got[1] != "B" {
		t.Fatalf("sources=%v", got)
	}
}

func TestCachedReaderTTL(t *testing.T) {
	var calls int32
	c := newCachedPriceReader([]string{"A"}, func(string) ([]Price, error) {
		atomic.AddInt32(&calls, 1)
		return []Price{{Type: "x"}}, nil
	}, time.Minute)
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return clock }
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, _, err := c.Get(ctx, "A"); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("calls=%d want 1", calls)
	}
	clock = clock.Add(2 * time.Minute)
	if _, _, err := c.Get(ctx, "A"); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls=%d want 2 after expiry", calls)
	}
}

func TestCachedReaderSingleflightAndErrorNotCached(t *testing.T) {
	var calls int32
	release := make(chan struct{})
	c := newCachedPriceReader([]string{"A"}, func(string) ([]Price, error) {
		atomic.AddInt32(&calls, 1)
		<-release
		return []Price{{Type: "x"}}, nil
	}, time.Minute)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := c.Get(context.Background(), "A"); err != nil {
				t.Error(err)
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	if calls != 1 {
		t.Fatalf("calls=%d want 1", calls)
	}

	boom := errors.New("boom")
	var n int32
	c2 := newCachedPriceReader([]string{"A"}, func(string) ([]Price, error) {
		if atomic.AddInt32(&n, 1) == 1 {
			return nil, boom
		}
		return []Price{{}}, nil
	}, time.Minute)
	if _, _, err := c2.Get(context.Background(), "A"); !errors.Is(err, boom) {
		t.Fatalf("err=%v", err)
	}
	if _, _, err := c2.Get(context.Background(), "A"); err != nil {
		t.Fatalf("retry should refetch: %v", err)
	}
}

func TestCachedReaderContextCancel(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	c := newCachedPriceReader([]string{"A"}, func(string) ([]Price, error) { <-block; return nil, nil }, time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, _, err := c.Get(ctx, "A"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v", err)
	}
}
