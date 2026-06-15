// Package news fetches RSS feeds, ranks the day's hottest stories with Gemini,
// and posts a bilingual (English + Vietnamese) digest to Telegram.
package news

import "time"

// FeedItem is a single normalized story pulled from an RSS/Atom feed.
type FeedItem struct {
	Title       string
	URL         string
	Description string
	Source      string    // feed host, e.g. "techcrunch.com"
	PublishedAt time.Time // zero if the feed omitted a parseable date
	HasDate     bool
}

// RankedItem is one story selected and summarized by Gemini for the digest.
type RankedItem struct {
	URL               string `json:"-"`
	EnglishTitle      string `json:"en_title"`
	EnglishSummary    string `json:"en_summary"`
	VietnameseTitle   string `json:"vi_title"`
	VietnameseSummary string `json:"vi_summary"`
	SourceIndex       int    `json:"index"` // index into the candidate slice sent to Gemini
}
