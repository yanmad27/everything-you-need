package pricetracker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"everything-you-need/m/services/netguard"
)

// FearGreedURL is the free, keyless crypto Fear & Greed endpoint (today + yesterday).
// It is fetched server-side only, through the SSRF-safe netguard client; it is
// deliberately NOT part of the model-driven fetch_url allowlist.
const FearGreedURL = "https://api.alternative.me/fng/?limit=2"

const (
	fearGreedTTL      = time.Hour
	fearGreedMaxStale = 24 * time.Hour
	fearGreedTimeout  = 10 * time.Second
	fearGreedMaxBody  = 64 << 10
)

// FearGreed is the index for the latest day, with the previous day when known.
type FearGreed struct {
	Value          int
	Classification string // original label, e.g. "Extreme Fear"
	Date           time.Time
	HasPrev        bool
	Change         int // Value - yesterday's value; meaningful only when HasPrev
}

// ClassificationVN is the Vietnamese label ("" for an unknown label).
func (f FearGreed) ClassificationVN() string { return fearGreedVN[f.Classification] }

var fearGreedVN = map[string]string{
	"Extreme Fear":  "Sợ hãi tột độ",
	"Fear":          "Sợ hãi",
	"Neutral":       "Trung lập",
	"Greed":         "Tham lam",
	"Extreme Greed": "Tham lam tột độ",
}

var fearGreedEmoji = map[string]string{
	"Extreme Fear": "😱", "Fear": "😨", "Neutral": "😐", "Greed": "😀", "Extreme Greed": "🤑",
}

func (f FearGreed) changeText() string {
	switch {
	case !f.HasPrev:
		return ""
	case f.Change > 0:
		return fmt.Sprintf(" · ▲%d so với hôm qua", f.Change)
	case f.Change < 0:
		return fmt.Sprintf(" · ▼%d so với hôm qua", -f.Change)
	}
	return " · không đổi so với hôm qua"
}

func (f FearGreed) label() string {
	if vn := f.ClassificationVN(); vn != "" {
		return fmt.Sprintf("%s (%s)", vn, f.Classification)
	}
	return f.Classification
}

// Line is the one-line Telegram rendering.
func (f FearGreed) Line() string {
	e := fearGreedEmoji[f.Classification]
	if e == "" {
		e = "📉"
	}
	return fmt.Sprintf("%s Fear & Greed: %d — %s%s", e, f.Value, f.label(), f.changeText())
}

// Text is the multi-field rendering used by the chat tool.
func (f FearGreed) Text() string {
	s := fmt.Sprintf("Chỉ số Fear & Greed (crypto): %d/100 — %s, ngày %s", f.Value, f.label(), f.Date.UTC().Format("2006-01-02"))
	if f.HasPrev {
		s += fmt.Sprintf("; thay đổi so với hôm qua: %+d", f.Change)
	}
	return s
}

type fngResponse struct {
	Data []struct {
		Value          string `json:"value"`
		Classification string `json:"value_classification"`
		Timestamp      string `json:"timestamp"`
	} `json:"data"`
}

// parseFearGreed decodes the alternative.me payload. The latest entry is today;
// the next one, if any and valid, is yesterday.
func parseFearGreed(body []byte) (FearGreed, error) {
	var r fngResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return FearGreed{}, fmt.Errorf("parse fear&greed: %w", err)
	}
	type entry struct {
		v    int
		cls  string
		unix int64
	}
	var es []entry
	for _, d := range r.Data {
		v, err1 := strconv.Atoi(d.Value)
		ts, err2 := strconv.ParseInt(d.Timestamp, 10, 64)
		if err1 != nil || err2 != nil || v < 0 || v > 100 {
			continue
		}
		es = append(es, entry{v, d.Classification, ts})
	}
	if len(es) == 0 {
		return FearGreed{}, errors.New("fear&greed: no valid data")
	}
	sort.SliceStable(es, func(i, j int) bool { return es[i].unix > es[j].unix })
	out := FearGreed{Value: es[0].v, Classification: es[0].cls, Date: time.Unix(es[0].unix, 0).UTC()}
	if len(es) > 1 {
		out.HasPrev, out.Change = true, es[0].v-es[1].v
	}
	return out, nil
}

// FearGreedSource fetches and caches the index. Safe for concurrent use.
type FearGreedSource struct {
	url    string
	client *http.Client
	ttl    time.Duration
	now    func() time.Time

	mu    sync.Mutex // also serialises fetches: one live request at a time
	val   FearGreed
	at    time.Time
	valid bool
}

func NewFearGreedSource() *FearGreedSource {
	return newFearGreedSource(FearGreedURL, netguard.NewSafeClient(fearGreedTimeout))
}

func newFearGreedSource(url string, c *http.Client) *FearGreedSource {
	return &FearGreedSource{url: url, client: c, ttl: fearGreedTTL, now: time.Now}
}

// Get returns the index from cache (TTL 1h). When a refresh fails it serves the
// last value if it is under 24h old, otherwise the error. ctx bounds the wait.
func (s *FearGreedSource) Get(ctx context.Context) (FearGreed, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if s.valid && now.Sub(s.at) < s.ttl {
		return s.val, nil
	}
	v, err := s.fetch(ctx)
	if err != nil {
		if s.valid && now.Sub(s.at) < fearGreedMaxStale {
			log.Printf("fear&greed refresh failed, serving stale value: %v", err)
			return s.val, nil
		}
		return FearGreed{}, err
	}
	s.val, s.at, s.valid = v, now, true
	return v, nil
}

func (s *FearGreedSource) fetch(ctx context.Context) (FearGreed, error) {
	ctx, cancel := context.WithTimeout(ctx, fearGreedTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return FearGreed{}, errors.New("fear&greed: bad request")
	}
	resp, err := s.client.Do(req)
	if err != nil {
		if errors.Is(err, netguard.ErrBlocked) {
			return FearGreed{}, netguard.ErrBlocked
		}
		return FearGreed{}, errors.New("fear&greed: request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return FearGreed{}, fmt.Errorf("fear&greed: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, fearGreedMaxBody))
	if err != nil {
		return FearGreed{}, errors.New("fear&greed: read failed")
	}
	return parseFearGreed(body)
}
