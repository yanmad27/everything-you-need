package news

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Searcher fetches news items via the Serper.dev Google News search API.
type Searcher struct {
	apiKey  string
	queries []string
	client  *http.Client
}

func NewSearcher(apiKey string, queries []string) *Searcher {
	return &Searcher{
		apiKey:  apiKey,
		queries: queries,
		client:  &http.Client{Timeout: 15 * time.Second},
	}
}

type serperRequest struct {
	Query string `json:"q"`
	GL    string `json:"gl"`
	HL    string `json:"hl"`
	Num   int    `json:"num"`
}

type serperResponse struct {
	News []serperNewsItem `json:"news"`
}

type serperNewsItem struct {
	Title   string `json:"title"`
	Link    string `json:"link"`
	Snippet string `json:"snippet"`
	Source  string `json:"source"`
	Date    string `json:"date"`
}

// FetchAll runs all configured queries and returns deduplicated FeedItems.
func (s *Searcher) FetchAll(ctx context.Context) []FeedItem {
	if s.apiKey == "" || len(s.queries) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	var items []FeedItem
	for _, query := range s.queries {
		results, err := s.search(ctx, query)
		if err != nil {
			continue
		}
		for _, r := range results {
			if seen[r.URL] {
				continue
			}
			seen[r.URL] = true
			items = append(items, r)
		}
	}
	return items
}

func (s *Searcher) search(ctx context.Context, query string) ([]FeedItem, error) {
	body, _ := json.Marshal(serperRequest{Query: query, GL: "vn", HL: "vi", Num: 10})
	req, err := http.NewRequestWithContext(ctx, "POST", "https://google.serper.dev/news", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-API-KEY", s.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("serper request: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("serper status %d: %s", resp.StatusCode, string(raw))
	}

	var result serperResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("serper decode: %w", err)
	}

	items := make([]FeedItem, 0, len(result.News))
	for _, n := range result.News {
		items = append(items, FeedItem{
			Title:       n.Title,
			URL:         n.Link,
			Description: n.Snippet,
			Source:      n.Source,
			PublishedAt: time.Now(),
			HasDate:     false,
		})
	}
	return items, nil
}
