package pricetracker

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func fixture(t *testing.T, side string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/p2p/binance_" + side + ".json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func adsBody(prices ...string) string {
	var parts []string
	for _, p := range prices {
		parts = append(parts, fmt.Sprintf(`{"adv":{"price":%q,"tradableQuantity":"10"}}`, p))
	}
	return `{"code":"000000","data":[` + strings.Join(parts, ",") + `]}`
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newTestReader(url string) (*P2PRateReader, *clock) {
	c := &clock{t: time.Date(2026, 11, 26, 8, 0, 0, 0, time.UTC)}
	r := NewP2PRateReaderURL(url)
	r.now = c.now
	return r, c
}

func fixtureServer(t *testing.T, hits *atomic.Int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		hits.Add(1)
		b, _ := io.ReadAll(req.Body)
		if req.Method != http.MethodPost || !strings.Contains(string(b), `"fiat":"VND"`) || !strings.Contains(string(b), `"asset":"USDT"`) {
			http.Error(w, "bad request", 400)
			return
		}
		side := "BUY"
		if strings.Contains(string(b), `"tradeType":"SELL"`) {
			side = "SELL"
		}
		w.Write(fixture(t, side))
	}))
}

func TestParseP2PSideFixtures(t *testing.T) {
	buy, err := parseP2PSide(fixture(t, "BUY"))
	if err != nil {
		t.Fatal(err)
	}
	sell, err := parseP2PSide(fixture(t, "SELL"))
	if err != nil {
		t.Fatal(err)
	}
	if buy != 26435 || sell != 26433 { // even count: mean of the two middle ads
		t.Fatalf("buy=%v sell=%v", buy, sell)
	}
	t.Logf("fixture medians: buy=%v sell=%v mid=%v", buy, sell, (buy+sell)/2)
}

func TestParseP2PSideFilteringAndMedian(t *testing.T) {
	odd, err := parseP2PSide([]byte(adsBody("26000", "26100", "26200", "26300", "26400")))
	if err != nil || odd != 26200 {
		t.Fatalf("odd: %v %v", odd, err)
	}
	even, err := parseP2PSide([]byte(adsBody("26000", "26100", "26200", "26300", "26400", "26500")))
	if err != nil || even != 26250 {
		t.Fatalf("even: %v %v", even, err)
	}
	// invalid ads are dropped: zero, negative, out of range, junk, zero quantity
	mixed := `{"code":"000000","data":[
	 {"adv":{"price":"0","tradableQuantity":"1"}},{"adv":{"price":"-5","tradableQuantity":"1"}},
	 {"adv":{"price":"999999","tradableQuantity":"1"}},
	 {"adv":{"price":"abc","tradableQuantity":"1"}},
	 {"adv":{"price":"26000","tradableQuantity":"1"}},{"adv":{"price":"26100","tradableQuantity":"1"}},
	 {"adv":{"price":"26200","tradableQuantity":"2"}},{"adv":{"price":"26300","tradableQuantity":"1"}},{"adv":{"price":"26400","tradableQuantity":"1"}}]}`
	if v, err := parseP2PSide([]byte(mixed)); err != nil || v != 26200 {
		t.Fatalf("mixed: %v %v", v, err)
	}
}

func TestParseP2PSideRejects(t *testing.T) {
	for name, body := range map[string]string{
		"empty":      ``,
		"malformed":  `{"code":`,
		"no data":    `{"code":"000000","data":[]}`,
		"null data":  `{"code":"000000","data":null}`,
		"bad code":   `{"code":"100001","data":[]}`,
		"too few":    adsBody("26000", "26100", "26200", "26300"),
		"all junk":   adsBody("x", "0", "-1", "1", "5"),
		"html":       `<html>captcha</html>`,
		"wrong type": `{"code":"000000","data":{"adv":1}}`,
	} {
		if v, err := parseP2PSide([]byte(body)); err == nil || v != 0 {
			t.Errorf("%s: want error, got %v", name, v)
		}
	}
}

func TestRateFreshConversion(t *testing.T) {
	var hits atomic.Int32
	srv := fixtureServer(t, &hits)
	defer srv.Close()
	r, _ := newTestReader(srv.URL)
	v, ok := r.Rate(context.Background())
	if !ok || v != 26434 {
		t.Fatalf("rate=%v ok=%v", v, ok)
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("one refresh = BUY+SELL, got %d requests", got)
	}
	if _, ok := r.Rate(context.Background()); !ok || hits.Load() != 2 {
		t.Fatalf("fresh rate must be served from cache (hits=%d)", hits.Load())
	}
	if got := FormatVND(68000, v); !strings.HasPrefix(got, "≈ 1.797.512.000 ₫") || !strings.HasSuffix(got, " ₫ (USDT/VND P2P)") {
		t.Fatalf("format: %s", got)
	}
}

func TestRateExpiryAndFailedRefresh(t *testing.T) {
	var fail atomic.Bool
	var hits atomic.Int32
	inner := fixtureServer(t, &hits)
	defer inner.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if fail.Load() {
			http.Error(w, "down", 503)
			return
		}
		inner.Config.Handler.ServeHTTP(w, req)
	}))
	defer srv.Close()
	r, clk := newTestReader(srv.URL)
	if _, ok := r.Rate(context.Background()); !ok {
		t.Fatal("initial fetch failed")
	}
	fail.Store(true)

	clk.add(p2pFreshTTL - time.Second) // still younger than the TTL: served
	if _, ok := r.Rate(context.Background()); !ok {
		t.Fatal("rate younger than the TTL must be served")
	}
	clk.add(time.Second) // exactly at the TTL, refresh fails: USD only
	if v, ok := r.Rate(context.Background()); ok || v != 0 {
		t.Fatalf("stale rate leaked: %v %v", v, ok)
	}
	clk.add(time.Minute) // inside the backoff, still no stale rate
	if _, ok := r.Rate(context.Background()); ok {
		t.Fatal("stale rate leaked during backoff")
	}
	fail.Store(false)
	clk.add(p2pFailBackoff + time.Second)
	if _, ok := r.Rate(context.Background()); !ok {
		t.Fatal("recovery after backoff failed")
	}
}

func TestRateBackoffAndRateLimit(t *testing.T) {
	var hits atomic.Int32
	status := atomic.Int32{}
	status.Store(429)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		http.Error(w, "x", int(status.Load()))
	}))
	defer srv.Close()
	r, clk := newTestReader(srv.URL)
	for i := 0; i < 3; i++ {
		if _, ok := r.Rate(context.Background()); ok {
			t.Fatal("429 must yield no rate")
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("negative cache: want 1 request, got %d", hits.Load())
	}
	clk.add(p2pFailBackoff + time.Second) // 429 backs off longer than a plain failure
	r.Rate(context.Background())
	if hits.Load() != 1 {
		t.Fatalf("429 backoff too short: %d", hits.Load())
	}
	clk.add(p2pRateLimitBackoff)
	status.Store(500)
	r.Rate(context.Background())
	if hits.Load() != 2 {
		t.Fatalf("retry after backoff: %d", hits.Load())
	}
}

func TestRateTooFewAdsAndMalformed(t *testing.T) {
	for name, body := range map[string]string{"few": adsBody("26000", "26100"), "junk": "not json", "empty": ""} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, body) }))
		r, _ := newTestReader(srv.URL)
		if v, ok := r.Rate(context.Background()); ok || v != 0 {
			t.Errorf("%s: got %v", name, v)
		}
		srv.Close()
	}
}

func TestRateOneSidedIsUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		if strings.Contains(string(b), "SELL") {
			io.WriteString(w, adsBody("26000"))
			return
		}
		io.WriteString(w, adsBody("26000", "26100", "26200", "26300", "26400"))
	}))
	defer srv.Close()
	r, _ := newTestReader(srv.URL)
	if _, ok := r.Rate(context.Background()); ok {
		t.Fatal("a one-sided rate must not be published")
	}
}

func TestRateBodyBound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"code":"000000","data":[`)
		io.WriteString(w, strings.Repeat(" ", p2pMaxBody+10))
	}))
	defer srv.Close()
	r, _ := newTestReader(srv.URL)
	if _, ok := r.Rate(context.Background()); ok {
		t.Fatal("oversized body must fail to parse")
	}
}

func TestRateContextCancellation(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
		io.WriteString(w, adsBody("26000", "26100", "26200", "26300", "26400"))
	}))
	defer srv.Close()
	defer close(release)
	r, _ := newTestReader(srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, ok := r.Rate(ctx); ok || time.Since(start) > 2*time.Second {
		t.Fatalf("cancelled caller must return promptly with no rate (%v)", time.Since(start))
	}
	cancelled, c2 := context.WithCancel(context.Background())
	c2()
	if _, ok := r.Rate(cancelled); ok {
		t.Fatal("already-cancelled context must yield no rate")
	}
}

func TestRateConcurrentCallersSingleFetch(t *testing.T) {
	var hits atomic.Int32
	gate := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		hits.Add(1)
		<-gate
		io.WriteString(w, adsBody("26000", "26100", "26200", "26300", "26400"))
	}))
	defer srv.Close()
	r, _ := newTestReader(srv.URL)
	var wg sync.WaitGroup
	var okN atomic.Int32
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if v, ok := r.Rate(context.Background()); ok && v == 26200 {
				okN.Add(1)
			}
		}()
	}
	time.Sleep(100 * time.Millisecond)
	close(gate)
	wg.Wait()
	if okN.Load() != 20 || hits.Load() != 2 { // one shared refresh = BUY + SELL
		t.Fatalf("ok=%d requests=%d", okN.Load(), hits.Load())
	}
}

func TestRateNilReader(t *testing.T) {
	var r *P2PRateReader
	if _, ok := r.Rate(context.Background()); ok {
		t.Fatal("nil reader")
	}
}

func TestParseP2PSideExaminesOnlyFirstTenAndRequiresQuantity(t *testing.T) {
	// 10 invalid ads first, then 5 valid ones: the valid ones are beyond the cap.
	bad := strings.Repeat(`{"adv":{"price":"1","tradableQuantity":"1"}},`, 10)
	body := `{"code":"000000","data":[` + bad + `{"adv":{"price":"26000","tradableQuantity":"1"}},{"adv":{"price":"26100","tradableQuantity":"1"}},{"adv":{"price":"26200","tradableQuantity":"1"}},{"adv":{"price":"26300","tradableQuantity":"1"}},{"adv":{"price":"26400","tradableQuantity":"1"}}]}`
	if _, err := parseP2PSide([]byte(body)); err == nil {
		t.Fatal("ads beyond the first 10 must not be examined")
	}
	noQty := `{"code":"000000","data":[{"adv":{"price":"26000"}},{"adv":{"price":"26100"}},{"adv":{"price":"26200","tradableQuantity":"2"}},{"adv":{"price":"26300"}},{"adv":{"price":"26400"}}]}`
	if _, err := parseP2PSide([]byte(noQty)); err == nil {
		t.Fatal("missing tradableQuantity must invalidate the ad")
	}
}

func TestFormatVNDGuards(t *testing.T) {
	inf, nan := math.Inf(1), math.NaN()
	for _, c := range [][2]float64{{inf, 26000}, {68000, inf}, {nan, 1}, {1, nan}, {-1, 26000}, {68000, -1}, {0, 26000}, {0.00001, 26000}, {1, 0.4}, {1, 0.5}} {
		if got := FormatVND(c[0], c[1]); got != "" {
			t.Errorf("%v: %q", c, got)
		}
	}
	if got := FormatVND(1, 1); got == "" {
		t.Error("1 dong is the lower bound")
	}
}

func TestRequestHeaders(t *testing.T) {
	var ua, accept atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ua.Store(req.Header.Get("User-Agent"))
		accept.Store(req.Header.Get("Accept"))
		io.WriteString(w, adsBody("26000", "26100", "26200", "26300", "26400"))
	}))
	defer srv.Close()
	r, _ := newTestReader(srv.URL)
	r.Rate(context.Background())
	if !strings.HasPrefix(ua.Load().(string), "Mozilla/5.0") || accept.Load() != "application/json" {
		t.Fatalf("%v %v", ua.Load(), accept.Load())
	}
}
