package news

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "news_test.db")
	store, err := OpenStore(path)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestStoreFilterUnseenAndMark(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	now := time.Now()

	items := []FeedItem{
		{Title: "a", URL: "https://a.com/1"},
		{Title: "b", URL: "https://b.com/2"},
	}

	unseen, err := store.FilterUnseen(ctx, items, now.Add(-7*24*time.Hour))
	if err != nil {
		t.Fatalf("FilterUnseen: %v", err)
	}
	if len(unseen) != 2 {
		t.Fatalf("expected 2 unseen initially, got %d", len(unseen))
	}

	if err := store.MarkPosted(ctx, items[:1]); err != nil {
		t.Fatalf("MarkPosted: %v", err)
	}

	unseen, err = store.FilterUnseen(ctx, items, now.Add(-7*24*time.Hour))
	if err != nil {
		t.Fatalf("FilterUnseen 2: %v", err)
	}
	if len(unseen) != 1 || unseen[0].URL != "https://b.com/2" {
		t.Fatalf("expected only b.com unseen, got %+v", unseen)
	}
}

func TestStorePurgeOld(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	if err := store.MarkPosted(ctx, []FeedItem{{Title: "old", URL: "https://old.com/1"}}); err != nil {
		t.Fatalf("MarkPosted: %v", err)
	}
	// purge with a future cutoff removes everything
	purged, err := store.PurgeOld(ctx, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("PurgeOld: %v", err)
	}
	if purged != 1 {
		t.Errorf("expected 1 purged, got %d", purged)
	}
}
