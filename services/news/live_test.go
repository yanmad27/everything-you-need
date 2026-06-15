package news

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLiveFetch hits the real default feeds. Gated by NEWS_LIVE=1 so it never
// runs in normal CI.
func TestLiveFetch(t *testing.T) {
	if os.Getenv("NEWS_LIVE") != "1" {
		t.Skip("set NEWS_LIVE=1 to run live feed fetch")
	}
	feeds := []string{
		"https://news.ycombinator.com/rss",
		"https://techcrunch.com/feed/",
		"https://www.theverge.com/rss/index.xml",
		"http://feeds.arstechnica.com/arstechnica/index",
		"https://vnexpress.net/rss/so-hoa.rss",
		"https://vnexpress.net/rss/tin-moi-nhat.rss",
	}
	items, failed := FetchFeeds(context.Background(), feeds, 20*time.Second)
	t.Logf("fetched %d items, %d feeds failed: %v", len(items), len(failed), failed)
	for _, item := range items[:min(5, len(items))] {
		t.Logf("- [%s] %s (date=%v) %s", item.Source, item.Title, item.HasDate, item.URL)
	}
	if len(items) == 0 {
		t.Fatal("no items fetched from any live feed")
	}
}
