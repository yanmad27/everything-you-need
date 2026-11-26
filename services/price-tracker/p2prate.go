package pricetracker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// USDT/VND P2P rate used to show an approximate VND value next to crypto USD
// prices.
//
// Semantics (fixed, no configuration):
//   - Source: Binance P2P public advert search (POST, no auth), asset USDT,
//     fiat VND, page 1, the first p2pRows adverts of each side.
//   - Sides: both. The BUY query lists adverts a user can buy USDT from (the
//     user's cost), the SELL query lists adverts a user can sell USDT to (the
//     user's proceeds). Each side's rate is the median of its valid adverts and
//     the published rate is the midpoint of the two medians, so the value is
//     neither the buy nor the sell price.
//   - Only the first p2pRows adverts returned are examined. Valid advert: price
//     parses inside [p2pMinPrice, p2pMaxPrice] VND per USDT and tradableQuantity
//     is present and positive. Invalid adverts are dropped, not repaired.
//   - A side needs at least p2pMinAds valid adverts, otherwise the whole rate
//     is unavailable (never a one-sided or partial rate).
//   - Median of an even count is the mean of the two middle values.
//   - Freshness: a rate is usable only while younger than p2pFreshTTL (10 min).
//     An older one is refreshed; if the refresh fails no VND is produced at
//     all (USD only). There is no stale window.
//   - A failed fetch starts a p2pFailBackoff (p2pRateLimitBackoff after HTTP
//     429) during which no new request is made.
const (
	P2PRateURL = "https://p2p.binance.com/bapi/c2c/v2/friendly/c2c/adv/search"

	p2pRows             = 10
	p2pMinAds           = 5
	p2pMinPrice         = 15000.0
	p2pMaxPrice         = 40000.0
	p2pFreshTTL         = 10 * time.Minute
	p2pFailBackoff      = 2 * time.Minute
	p2pRateLimitBackoff = 5 * time.Minute
	p2pTimeout          = 8 * time.Second
	p2pMaxBody          = 1 << 20
)

var errP2PRateLimited = errors.New("p2p rate limited")

// P2PRateReader is the single shared, cached USDT/VND reader.
type P2PRateReader struct {
	url    string
	client *http.Client
	now    func() time.Time

	mu        sync.Mutex
	rate      float64
	fetchedAt time.Time
	retryAt   time.Time
	inflight  *p2pFetch
}

type p2pFetch struct{ done chan struct{} }

func NewP2PRateReader() *P2PRateReader { return NewP2PRateReaderURL(P2PRateURL) }

func NewP2PRateReaderURL(url string) *P2PRateReader {
	return &P2PRateReader{url: url, client: &http.Client{Timeout: p2pTimeout}, now: time.Now}
}

// Rate returns VND per USDT, or ok=false when no fresh-enough rate exists. It
// never blocks past ctx and never returns a rate older than p2pFreshTTL.
func (r *P2PRateReader) Rate(ctx context.Context) (float64, bool) {
	if r == nil || ctx.Err() != nil {
		return 0, false
	}
	r.mu.Lock()
	if v, ok := r.usableLocked(p2pFreshTTL); ok {
		r.mu.Unlock()
		return v, true
	}
	f := r.inflight
	if f == nil && r.now().Before(r.retryAt) {
		v, ok := r.usableLocked(p2pFreshTTL)
		r.mu.Unlock()
		return v, ok
	}
	if f == nil {
		f = &p2pFetch{done: make(chan struct{})}
		r.inflight = f
		go r.refresh(f)
	}
	r.mu.Unlock()

	select {
	case <-f.done:
	case <-ctx.Done():
		return 0, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.usableLocked(p2pFreshTTL)
}

func (r *P2PRateReader) usableLocked(maxAge time.Duration) (float64, bool) {
	if r.rate <= 0 || r.now().Sub(r.fetchedAt) >= maxAge {
		return 0, false
	}
	return r.rate, true
}

// refresh runs detached from any caller's context so one caller giving up does
// not fail the others; it is bounded by the HTTP timeout.
func (r *P2PRateReader) refresh(f *p2pFetch) {
	defer close(f.done)
	ctx, cancel := context.WithTimeout(context.Background(), p2pTimeout)
	defer cancel()
	v, err := r.fetch(ctx)
	r.mu.Lock()
	if err != nil {
		backoff := p2pFailBackoff
		if errors.Is(err, errP2PRateLimited) {
			backoff = p2pRateLimitBackoff
		}
		r.retryAt = r.now().Add(backoff)
	} else {
		r.rate, r.fetchedAt, r.retryAt = v, r.now(), time.Time{}
	}
	r.inflight = nil
	r.mu.Unlock()
}

func (r *P2PRateReader) fetch(ctx context.Context) (float64, error) {
	buy, err := r.side(ctx, "BUY")
	if err != nil {
		return 0, err
	}
	sell, err := r.side(ctx, "SELL")
	if err != nil {
		return 0, err
	}
	return (buy + sell) / 2, nil
}

func (r *P2PRateReader) side(ctx context.Context, tradeType string) (float64, error) {
	payload, _ := json.Marshal(map[string]any{
		"fiat": "VND", "asset": "USDT", "tradeType": tradeType, "page": 1, "rows": p2pRows,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.url, bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36")
	resp, err := r.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("p2p rate request: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return 0, errP2PRateLimited
	case resp.StatusCode != http.StatusOK:
		return 0, fmt.Errorf("p2p rate status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, p2pMaxBody))
	if err != nil {
		return 0, fmt.Errorf("p2p rate body: %w", err)
	}
	return parseP2PSide(body)
}

type p2pResponse struct {
	Code string `json:"code"`
	Data []struct {
		Adv struct {
			Price            string `json:"price"`
			TradableQuantity string `json:"tradableQuantity"`
		} `json:"adv"`
	} `json:"data"`
}

// parseP2PSide returns the median price of the valid adverts of one side.
func parseP2PSide(body []byte) (float64, error) {
	var r p2pResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return 0, fmt.Errorf("p2p rate parse: %w", err)
	}
	if r.Code != "000000" {
		return 0, errors.New("p2p rate: unexpected response code")
	}
	var prices []float64
	for i, d := range r.Data {
		if i == p2pRows {
			break
		}
		p, err := strconv.ParseFloat(strings.TrimSpace(d.Adv.Price), 64)
		if err != nil || !(p >= p2pMinPrice && p <= p2pMaxPrice) {
			continue
		}
		if qv, err := strconv.ParseFloat(strings.TrimSpace(d.Adv.TradableQuantity), 64); err != nil || !(qv > 0) {
			continue
		}
		prices = append(prices, p)
	}
	if len(prices) < p2pMinAds {
		log.Printf("p2p rate: only %d of %d adverts valid (min %d); schema or market change?", len(prices), len(r.Data), p2pMinAds)
		return 0, fmt.Errorf("p2p rate: only %d valid adverts", len(prices))
	}
	return median(prices), nil
}

func median(v []float64) float64 {
	sort.Float64s(v)
	n := len(v)
	if n%2 == 1 {
		return v[n/2]
	}
	return (v[n/2-1] + v[n/2]) / 2
}

// VNDLabel is the fixed approximation label appended to converted values.
const VNDLabel = "USDT/VND P2P"

// FormatVND renders usd*rate as "≈ 2.508.000.000 ₫ (USDT/VND P2P)" with dot
// thousands separators, rounded to whole dong.
// It returns "" unless the product is finite and at least 1 dong.
func FormatVND(usd, rate float64) string {
	v := usd * rate
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 1 {
		return ""
	}
	s := strconv.FormatFloat(v, 'f', 0, 64)
	var parts []string
	for i := len(s); i > 0; i -= 3 {
		start := i - 3
		if start < 0 {
			start = 0
		}
		parts = append([]string{s[start:i]}, parts...)
	}
	return fmt.Sprintf("≈ %s ₫ (%s)", strings.Join(parts, "."), VNDLabel)
}
