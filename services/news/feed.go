package news

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// rssFeed captures both RSS (channel>item) and Atom (entry) shapes so a single
// decode handles either format.
type rssFeed struct {
	Items   []rssItem   `xml:"channel>item"`
	Entries []atomEntry `xml:"entry"`
}

type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Description string `xml:"description"`
	PubDate     string `xml:"pubDate"`
	Date        string `xml:"date"` // dublin core fallback
}

type atomEntry struct {
	Title string `xml:"title"`
	Links []struct {
		Href string `xml:"href,attr"`
		Rel  string `xml:"rel,attr"`
	} `xml:"link"`
	Summary   string `xml:"summary"`
	Content   string `xml:"content"`
	Updated   string `xml:"updated"`
	Published string `xml:"published"`
}

var dateLayouts = []string{
	time.RFC1123Z,
	time.RFC1123,
	time.RFC3339,
	"Mon, 02 Jan 2006 15:04:05 -0700",
	"Mon, 02 Jan 2006 15:04:05 MST",
	"2006-01-02T15:04:05Z07:00",
	"2006-01-02 15:04:05",
}

func parseFeedDate(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	for _, layout := range dateLayouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func hostOf(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		return rawURL
	}
	return strings.TrimPrefix(parsed.Host, "www.")
}

// FetchFeeds pulls all feeds concurrently and returns the combined items.
// Failed feeds are skipped (best-effort); their URLs are returned for logging.
func FetchFeeds(ctx context.Context, feedURLs []string, timeout time.Duration) ([]FeedItem, []string) {
	if timeout == 0 {
		timeout = 20 * time.Second
	}
	client := &http.Client{Timeout: timeout}

	var (
		mu     sync.Mutex
		items  []FeedItem
		failed []string
		wg     sync.WaitGroup
	)

	for _, feedURL := range feedURLs {
		wg.Add(1)
		go func(feedURL string) {
			defer wg.Done()
			fetched, err := fetchOne(ctx, client, feedURL)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failed = append(failed, fmt.Sprintf("%s: %v", feedURL, err))
				return
			}
			items = append(items, fetched...)
		}(feedURL)
	}
	wg.Wait()
	return items, failed
}

func fetchOne(ctx context.Context, client *http.Client, feedURL string) ([]FeedItem, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", feedURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "everything-you-need-newsbot/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20)) // cap 5MB
	if err != nil {
		return nil, err
	}
	return ParseFeed(raw)
}

// ParseFeed decodes RSS or Atom bytes into normalized FeedItems.
func ParseFeed(raw []byte) ([]FeedItem, error) {
	var feed rssFeed
	decoder := xml.NewDecoder(strings.NewReader(string(raw)))
	decoder.Strict = false
	decoder.CharsetReader = func(_ string, input io.Reader) (io.Reader, error) { return input, nil }
	if err := decoder.Decode(&feed); err != nil {
		return nil, fmt.Errorf("decode feed: %w", err)
	}

	var out []FeedItem
	for _, item := range feed.Items {
		publishedAt, hasDate := parseFeedDate(item.PubDate)
		if !hasDate {
			publishedAt, hasDate = parseFeedDate(item.Date)
		}
		link := strings.TrimSpace(item.Link)
		title := strings.TrimSpace(item.Title)
		if link == "" || title == "" {
			continue
		}
		out = append(out, FeedItem{
			Title:       title,
			URL:         link,
			Description: cleanText(item.Description),
			Source:      hostOf(link),
			PublishedAt: publishedAt,
			HasDate:     hasDate,
		})
	}
	for _, entry := range feed.Entries {
		publishedAt, hasDate := parseFeedDate(entry.Published)
		if !hasDate {
			publishedAt, hasDate = parseFeedDate(entry.Updated)
		}
		link := atomLink(entry)
		title := strings.TrimSpace(entry.Title)
		if link == "" || title == "" {
			continue
		}
		desc := entry.Summary
		if desc == "" {
			desc = entry.Content
		}
		out = append(out, FeedItem{
			Title:       title,
			URL:         link,
			Description: cleanText(desc),
			Source:      hostOf(link),
			PublishedAt: publishedAt,
			HasDate:     hasDate,
		})
	}
	return out, nil
}

func atomLink(entry atomEntry) string {
	// Prefer rel="alternate" (the human page), else first link with an href.
	for _, link := range entry.Links {
		if link.Rel == "alternate" && link.Href != "" {
			return link.Href
		}
	}
	for _, link := range entry.Links {
		if link.Href != "" {
			return link.Href
		}
	}
	return ""
}

var tagStripper = strings.NewReplacer("\n", " ", "\r", " ", "\t", " ")

// cleanText strips HTML tags and collapses whitespace from a feed description.
func cleanText(text string) string {
	text = tagStripper.Replace(text)
	// crude tag strip: drop everything between < and >
	var builder strings.Builder
	inTag := false
	for _, r := range text {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag:
			builder.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(builder.String()), " ")
}

// FilterRecent keeps items published within the window. Items without a
// parseable date are kept (best-effort), since some feeds omit dates.
func FilterRecent(items []FeedItem, now time.Time, window time.Duration) []FeedItem {
	cutoff := now.Add(-window)
	var out []FeedItem
	for _, item := range items {
		if !item.HasDate || !item.PublishedAt.Before(cutoff) {
			out = append(out, item)
		}
	}
	return out
}
