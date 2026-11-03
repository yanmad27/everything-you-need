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
	Classification string // original label, one of the five known ones; "" when upstream sent anything else
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

// UnknownLabel is shown when the upstream label is not one of the five known
// ones. Upstream text is never rendered.
const UnknownLabel = "Không rõ"

func (f FearGreed) label() string {
	if vn := f.ClassificationVN(); vn != "" {
		return fmt.Sprintf("%s (%s)", vn, f.Classification)
	}
	return UnknownLabel
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

func utcDay(unix int64) int64 { return time.Unix(unix, 0).UTC().Unix() / 86400 }

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
	cls := es[0].cls
	if _, ok := fearGreedVN[cls]; !ok {
		cls = "" // never carry upstream text further
	}
	out := FearGreed{Value: es[0].v, Classification: cls, Date: time.Unix(es[0].unix, 0).UTC()}
	// "Yesterday" means the previous UTC calendar day (entries are UTC midnight).
	today := utcDay(es[0].unix)
	for _, e := range es[1:] {
		if utcDay(e.unix) == today-1 {
			out.HasPrev, out.Change = true, es[0].v-e.v
			break
		}
	}
	return out, nil
}

type fgFlight struct {
	done chan struct{}
	val  FearGreed
	err  error
}

// FearGreedSource fetches and caches the index. Safe for concurrent use: at
// most one upstream fetch runs at a time and every waiter honours its own ctx.
type FearGreedSource struct {
	url     string
	client  *http.Client
	ttl     time.Duration
	now     func() time.Time
	fetchFn func(ctx context.Context) (FearGreed, error) // test seam; defaults to s.fetch

	mu       sync.Mutex
	val      FearGreed
	at       time.Time
	valid    bool
	inflight *fgFlight
}

func NewFearGreedSource() *FearGreedSource {
	return newFearGreedSource(FearGreedURL, netguard.NewSafeClient(fearGreedTimeout))
}

func newFearGreedSource(url string, c *http.Client) *FearGreedSource {
	s := &FearGreedSource{url: url, client: c, ttl: fearGreedTTL, now: time.Now}
	s.fetchFn = s.fetch
	return s
}

// Get returns the index from cache (TTL 1h). When a refresh fails it serves the
// last value if it is under 24h old (measured when the fetch finished),
// otherwise the error. ctx bounds only this caller's wait.
func (s *FearGreedSource) Get(ctx context.Context) (FearGreed, error) {
	s.mu.Lock()
	if s.valid && s.now().Sub(s.at) < s.ttl {
		v := s.val
		s.mu.Unlock()
		return v, nil
	}
	f := s.inflight
	if f == nil {
		f = &fgFlight{done: make(chan struct{})}
		s.inflight = f
		go s.run(f)
	}
	s.mu.Unlock()

	select {
	case <-f.done:
		return f.val, f.err
	case <-ctx.Done():
		return FearGreed{}, ctx.Err()
	}
}

func (s *FearGreedSource) run(f *fgFlight) {
	var v FearGreed
	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				err = errors.New("fear&greed: fetch panic")
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), fearGreedTimeout)
		defer cancel()
		v, err = s.fetchFn(ctx)
	}()
	s.mu.Lock()
	now := s.now()
	switch {
	case err == nil:
		s.val, s.at, s.valid = v, now, true
		f.val = v
	case s.valid && now.Sub(s.at) < fearGreedMaxStale:
		log.Printf("fear&greed refresh failed, serving stale value: %v", err)
		f.val = s.val
	default:
		f.err = err
	}
	s.inflight = nil
	s.mu.Unlock()
	close(f.done)
}

func (s *FearGreedSource) fetch(ctx context.Context) (FearGreed, error) {
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
