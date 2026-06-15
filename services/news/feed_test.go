package news

import (
	"testing"
	"time"
)

const rssSample = `<?xml version="1.0"?>
<rss version="2.0"><channel>
  <title>Sample</title>
  <item>
    <title>OpenAI ships new model</title>
    <link>https://techcrunch.com/openai-model</link>
    <description>&lt;p&gt;A &lt;b&gt;big&lt;/b&gt; release.&lt;/p&gt;</description>
    <pubDate>Mon, 15 Jun 2026 10:00:00 +0000</pubDate>
  </item>
  <item>
    <title>No date item</title>
    <link>https://vnexpress.net/tin-1</link>
    <description>Tin tức</description>
  </item>
</channel></rss>`

const atomSample = `<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <entry>
    <title>The Verge story</title>
    <link rel="alternate" href="https://www.theverge.com/story-1"/>
    <summary>Summary text</summary>
    <published>2026-06-15T09:00:00Z</published>
  </entry>
</feed>`

func TestParseFeedRSS(t *testing.T) {
	items, err := ParseFeed([]byte(rssSample))
	if err != nil {
		t.Fatalf("ParseFeed RSS: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].Title != "OpenAI ships new model" {
		t.Errorf("title = %q", items[0].Title)
	}
	if items[0].Source != "techcrunch.com" {
		t.Errorf("source = %q, want techcrunch.com", items[0].Source)
	}
	if !items[0].HasDate {
		t.Errorf("expected HasDate=true for dated item")
	}
	if items[0].Description != "A big release." {
		t.Errorf("description not cleaned: %q", items[0].Description)
	}
	if items[1].HasDate {
		t.Errorf("expected HasDate=false for undated item")
	}
}

func TestParseFeedAtom(t *testing.T) {
	items, err := ParseFeed([]byte(atomSample))
	if err != nil {
		t.Fatalf("ParseFeed Atom: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(items))
	}
	if items[0].URL != "https://www.theverge.com/story-1" {
		t.Errorf("url = %q", items[0].URL)
	}
	if items[0].Source != "theverge.com" {
		t.Errorf("source = %q, want theverge.com", items[0].Source)
	}
}

func TestFilterRecent(t *testing.T) {
	now := time.Date(2026, 6, 15, 20, 0, 0, 0, time.UTC)
	items := []FeedItem{
		{Title: "fresh", URL: "u1", PublishedAt: now.Add(-2 * time.Hour), HasDate: true},
		{Title: "stale", URL: "u2", PublishedAt: now.Add(-48 * time.Hour), HasDate: true},
		{Title: "nodate", URL: "u3", HasDate: false},
	}
	got := FilterRecent(items, now, 24*time.Hour)
	if len(got) != 2 {
		t.Fatalf("expected 2 (fresh + nodate), got %d", len(got))
	}
	for _, item := range got {
		if item.Title == "stale" {
			t.Errorf("stale item should have been filtered")
		}
	}
}

func TestDedupeByURL(t *testing.T) {
	items := []FeedItem{
		{Title: "a", URL: "u1"},
		{Title: "a-dup", URL: "u1"},
		{Title: "b", URL: "u2"},
	}
	got := dedupeByURL(items)
	if len(got) != 2 {
		t.Fatalf("expected 2 unique, got %d", len(got))
	}
}

func TestParseFeedDate(t *testing.T) {
	cases := []string{
		"Mon, 15 Jun 2026 10:00:00 +0000",
		"2026-06-15T09:00:00Z",
		"2026-06-15 09:00:00",
	}
	for _, raw := range cases {
		if _, ok := parseFeedDate(raw); !ok {
			t.Errorf("failed to parse date %q", raw)
		}
	}
	if _, ok := parseFeedDate("garbage"); ok {
		t.Errorf("garbage date should not parse")
	}
}
