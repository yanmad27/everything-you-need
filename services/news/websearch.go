package news

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ErrNotSupported is returned when the underlying data cannot answer a call.
var ErrNotSupported = errors.New("not supported")

const (
	serperBaseURL    = "https://google.serper.dev"
	maxWebQueryRunes = 200
)

// WebResult is one web or news search hit.
type WebResult struct{ Title, URL, Snippet, Source, Date string }

// WebSearcher runs ad-hoc Serper.dev searches, independent of the digest.
type WebSearcher struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

func NewWebSearcher(apiKey string) *WebSearcher {
	return &WebSearcher{apiKey: apiKey, baseURL: serperBaseURL, client: &http.Client{Timeout: 10 * time.Second}}
}

type serperWebResponse struct {
	Organic []serperNewsItem `json:"organic"`
	News    []serperNewsItem `json:"news"`
}

// Search queries Serper /search (organic results).
func (w *WebSearcher) Search(ctx context.Context, q string, n int) ([]WebResult, error) {
	return w.do(ctx, "/search", q, n)
}

// SearchNews queries Serper /news.
func (w *WebSearcher) SearchNews(ctx context.Context, q string, n int) ([]WebResult, error) {
	return w.do(ctx, "/news", q, n)
}

func (w *WebSearcher) do(ctx context.Context, path, q string, n int) ([]WebResult, error) {
	if w.apiKey == "" {
		return nil, errors.New("web search: api key not configured")
	}
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, errors.New("web search: empty query")
	}
	if r := []rune(q); len(r) > maxWebQueryRunes {
		q = string(r[:maxWebQueryRunes])
	}
	if n < 1 {
		n = 1
	}
	if n > 5 {
		n = 5
	}

	body, _ := json.Marshal(serperRequest{Query: q, GL: "vn", HL: "vi", Num: n})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, w.redact(err)
	}
	req.Header.Set("X-API-KEY", w.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := w.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("serper request: %w", w.redact(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// Never echo the response body: it may carry request details.
		return nil, fmt.Errorf("serper status %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("serper read: %w", w.redact(err))
	}
	var parsed serperWebResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, errors.New("serper decode: invalid response")
	}
	items := parsed.Organic
	if path == "/news" {
		items = parsed.News
	}
	if len(items) > n {
		items = items[:n]
	}
	out := make([]WebResult, 0, len(items))
	for _, it := range items {
		out = append(out, WebResult{Title: it.Title, URL: it.Link, Snippet: it.Snippet, Source: it.Source, Date: it.Date})
	}
	return out, nil
}

// redactedError carries a key-free message while preserving the cause chain.
type redactedError struct {
	msg string
	err error
}

func (e *redactedError) Error() string { return e.msg }
func (e *redactedError) Unwrap() error { return e.err }

// redact strips the API key from an error message, keeping errors.Is/As working.
func (w *WebSearcher) redact(err error) error {
	return &redactedError{msg: strings.ReplaceAll(err.Error(), w.apiKey, "[redacted]"), err: err}
}

// RecentPosted returns the most recently posted digest items (title + URL).
func (s *Store) RecentPosted(ctx context.Context, limit int) ([]WebResult, error) {
	if limit < 1 {
		limit = 1
	}
	if limit > 50 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT title, url, posted_at FROM posted_news ORDER BY posted_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("recent posted: %w", err)
	}
	defer rows.Close()
	var out []WebResult
	for rows.Next() {
		var title, url string
		var at time.Time
		if err := rows.Scan(&title, &url, &at); err != nil {
			return nil, fmt.Errorf("recent posted: %w", err)
		}
		out = append(out, WebResult{Title: title, URL: url, Date: at.UTC().Format(time.RFC3339)})
	}
	return out, rows.Err()
}
