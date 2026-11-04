package pricetracker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"everything-you-need/m/services/netguard"
)

const fngJSON = `{"name":"Fear and Greed Index","data":[` +
	`{"value":"25","value_classification":"Fear","timestamp":"1762128000","time_until_update":"3600"},` +
	`{"value":"28","value_classification":"Fear","timestamp":"1762041600"}],"metadata":{"error":null}}`

func TestParseFearGreed(t *testing.T) {
	f, err := parseFearGreed([]byte(fngJSON))
	if err != nil {
		t.Fatal(err)
	}
	if f.Value != 25 || f.Classification != "Fear" || !f.HasPrev || f.Change != -3 || f.Date.UTC().Format("2006-01-02") != "2025-11-03" {
		t.Fatalf("%+v", f)
	}
	if got, want := f.Line(), "😨 Fear & Greed: 25 — Sợ hãi (Fear) · ▼3 so với hôm qua"; got != want {
		t.Fatalf("line %q want %q", got, want)
	}
	t.Logf("sample line: %s", f.Line())
}

func TestFearGreedClassificationMapping(t *testing.T) {
	for en, vn := range map[string]string{"Extreme Fear": "Sợ hãi tột độ", "Fear": "Sợ hãi", "Neutral": "Trung lập", "Greed": "Tham lam", "Extreme Greed": "Tham lam tột độ"} {
		if got := (FearGreed{Classification: en}).ClassificationVN(); got != vn {
			t.Errorf("%s -> %q want %q", en, got, vn)
		}
	}
	if (FearGreed{Classification: "Weird"}).ClassificationVN() != "" {
		t.Error("unknown label must have no VN mapping")
	}
	if l := (FearGreed{Value: 50, Classification: "Weird"}).Line(); strings.Contains(l, "Weird") || !strings.Contains(l, UnknownLabel) {
		t.Errorf("unknown label not neutralised: %q", l)
	}
}

func TestFearGreedChangeCalculation(t *testing.T) {
	up, _ := parseFearGreed([]byte(`{"data":[{"value":"70","value_classification":"Greed","timestamp":"1762128000"},{"value":"60","value_classification":"Greed","timestamp":"1762041600"}]}`))
	if up.Change != 10 || !strings.Contains(up.Line(), "▲10") {
		t.Errorf("up: %+v %s", up, up.Line())
	}
	same, _ := parseFearGreed([]byte(`{"data":[{"value":"50","value_classification":"Neutral","timestamp":"1762128000"},{"value":"50","value_classification":"Neutral","timestamp":"1762041600"}]}`))
	if !strings.Contains(same.Line(), "không đổi") {
		t.Errorf("same: %s", same.Line())
	}
	// out-of-order entries: newest timestamp is today
	ooo, _ := parseFearGreed([]byte(`{"data":[{"value":"10","value_classification":"Extreme Fear","timestamp":"1762041600"},{"value":"15","value_classification":"Extreme Fear","timestamp":"1762128000"}]}`))
	if ooo.Value != 15 || ooo.Change != 5 {
		t.Errorf("ooo: %+v", ooo)
	}
}

func TestFearGreedMissingYesterday(t *testing.T) {
	f, err := parseFearGreed([]byte(`{"data":[{"value":"25","value_classification":"Fear","timestamp":"1762128000"}]}`))
	if err != nil || f.HasPrev {
		t.Fatalf("%+v %v", f, err)
	}
	if got := f.Line(); strings.Contains(got, "hôm qua") || got != "😨 Fear & Greed: 25 — Sợ hãi (Fear)" {
		t.Errorf("line %q", got)
	}
	// an unparsable yesterday is treated as missing
	f, err = parseFearGreed([]byte(`{"data":[{"value":"25","value_classification":"Fear","timestamp":"1762128000"},{"value":"x","value_classification":"Fear","timestamp":"1762041600"}]}`))
	if err != nil || f.HasPrev {
		t.Fatalf("%+v %v", f, err)
	}
}

func TestParseFearGreedRejectsGarbage(t *testing.T) {
	for _, in := range []string{``, `{}`, `{"data":[]}`, `{"data":[{"value":"abc","timestamp":"1762128000"}]}`, `{"data":[{"value":"250","timestamp":"1762128000"}]}`, `<html>`} {
		if _, err := parseFearGreed([]byte(in)); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
}

// fgRig serves fngJSON from httptest, with a controllable clock and failure switch.
type fgRig struct {
	src   *FearGreedSource
	hits  atomic.Int32
	fail  atomic.Bool
	clock atomic.Int64 // unix seconds
}

func newFGRig(t *testing.T) *fgRig {
	t.Helper()
	r := &fgRig{}
	r.clock.Store(1_000_000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.hits.Add(1)
		if req.URL.Query().Get("limit") != "2" {
			t.Errorf("unexpected query %q", req.URL.RawQuery)
		}
		if r.fail.Load() {
			http.Error(w, "boom secret-body", http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(fngJSON))
	}))
	t.Cleanup(srv.Close)
	r.src = newFearGreedSource(srv.URL+"/fng/?limit=2", srv.Client())
	r.src.now = func() time.Time { return time.Unix(r.clock.Load(), 0) }
	return r
}

func TestFearGreedCacheHitAndTTL(t *testing.T) {
	r := newFGRig(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if f, err := r.src.Get(ctx); err != nil || f.Value != 25 {
			t.Fatalf("%+v %v", f, err)
		}
	}
	if r.hits.Load() != 1 {
		t.Fatalf("hits=%d, want 1 (cache)", r.hits.Load())
	}
	r.clock.Add(int64(fearGreedTTL/time.Second) - 1)
	_, _ = r.src.Get(ctx)
	if r.hits.Load() != 1 {
		t.Fatal("refetched before TTL")
	}
	r.clock.Add(2)
	_, _ = r.src.Get(ctx)
	if r.hits.Load() != 2 {
		t.Fatalf("hits=%d, want refetch after TTL", r.hits.Load())
	}
}

func TestFearGreedStaleOnError(t *testing.T) {
	r := newFGRig(t)
	ctx := context.Background()
	if _, err := r.src.Get(ctx); err != nil {
		t.Fatal(err)
	}
	r.fail.Store(true)
	r.clock.Add(int64(2 * fearGreedTTL / time.Second)) // expired, still within the stale window
	f, err := r.src.Get(ctx)
	if err != nil || f.Value != 25 {
		t.Fatalf("stale not served: %+v %v", f, err)
	}
	r.clock.Add(int64(fearGreedMaxStale / time.Second)) // too old to serve
	if _, err := r.src.Get(ctx); err == nil || strings.Contains(err.Error(), "secret-body") {
		t.Fatalf("want sanitized error, got %v", err)
	}
}

func TestFearGreedErrorWithoutCache(t *testing.T) {
	r := newFGRig(t)
	r.fail.Store(true)
	if _, err := r.src.Get(context.Background()); err == nil {
		t.Fatal("want error with nothing cached")
	}
	r.fail.Store(false) // failures are cached only for the short negative window
	r.clock.Add(int64(fearGreedNegTTL/time.Second) + 1)
	if f, err := r.src.Get(context.Background()); err != nil || f.Value != 25 {
		t.Fatalf("%+v %v", f, err)
	}
}

func TestFearGreedHonoursContext(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { <-release }))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) }) // runs before srv.Close (LIFO): a failed assertion cannot hang it
	s := newFearGreedSource(srv.URL, srv.Client())
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := s.Get(ctx); err == nil {
		t.Fatal("want error on cancelled context")
	}
}

// The production source goes through netguard, so it can never reach loopback
// or private space; the host is pinned by a constant, not by caller input.
func TestFearGreedProductionClientIsNetguarded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(fngJSON)) }))
	defer srv.Close()
	s := newFearGreedSource(srv.URL, NewFearGreedSource().client) // loopback target, production client
	if _, err := s.Get(context.Background()); !errors.Is(err, netguard.ErrBlocked) {
		t.Fatalf("loopback must be blocked by netguard, got %v", err)
	}
	if !strings.HasPrefix(FearGreedURL, "https://api.alternative.me/") {
		t.Fatalf("pinned URL changed: %s", FearGreedURL)
	}
}

func fngDays(vals ...[2]string) []byte { // {value, unix}
	out := `{"data":[`
	for i, v := range vals {
		if i > 0 {
			out += ","
		}
		out += `{"value":"` + v[0] + `","value_classification":"Fear","timestamp":"` + v[1] + `"}`
	}
	return []byte(out + `]}`)
}

// item 3
func TestFearGreedPrevMustBePreviousUTCDay(t *testing.T) {
	const nov3, nov2, nov1 = "1762128000", "1762041600", "1761955200"
	f, _ := parseFearGreed(fngDays([2]string{"30", nov3}, [2]string{"20", nov2}))
	if !f.HasPrev || f.Change != 10 {
		t.Errorf("Nov3+Nov2: %+v", f)
	}
	f, _ = parseFearGreed(fngDays([2]string{"30", nov3}, [2]string{"20", nov1}))
	if f.HasPrev {
		t.Errorf("Nov3+Nov1 (gap) must have no change: %+v", f)
	}
	f, _ = parseFearGreed(fngDays([2]string{"30", nov3}, [2]string{"20", "1762128000"}))
	if f.HasPrev {
		t.Errorf("two Nov3 entries must have no change: %+v", f)
	}
	f, _ = parseFearGreed(fngDays([2]string{"30", nov3}, [2]string{"20", "1762171199"})) // same UTC day, later hour
	if f.HasPrev {
		t.Errorf("same-day entry: %+v", f)
	}
}

// item 1
func TestFearGreedUnknownLabelNeverRendered(t *testing.T) {
	evil := "_x_ *y* `z` [l](http://evil) " + strings.Repeat("A", 5000)
	body := []byte(`{"data":[{"value":"40","value_classification":` + jsonQuote(evil) + `,"timestamp":"1762128000"}]}`)
	f, err := parseFearGreed(body)
	if err != nil {
		t.Fatal(err)
	}
	for _, out := range []string{f.Line(), f.Text(), f.Classification, f.ClassificationVN()} {
		if strings.Contains(out, "evil") || strings.Contains(out, "AAAA") || strings.Contains(out, "_x_") {
			t.Fatalf("upstream text leaked: %q", out)
		}
	}
	if got, want := f.Line(), "📉 Fear & Greed: 40 — Không rõ"; got != want {
		t.Fatalf("line %q want %q", got, want)
	}
	t.Logf("unknown-label sample: %s", f.Line())
}

func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// item 2
func TestFearGreedWaitersHonourOwnContextAndShareOneFetch(t *testing.T) {
	var fetches atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	s := newFearGreedSource("x", nil)
	s.fetchFn = func(context.Context) (FearGreed, error) {
		if fetches.Add(1) == 1 {
			close(entered)
		}
		<-release
		return FearGreed{Value: 7, Classification: "Fear"}, nil
	}
	aDone := make(chan FearGreed, 1)
	go func() { f, _ := s.Get(context.Background()); aDone <- f }()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := s.Get(ctx); !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 2*time.Second {
		t.Fatalf("B err=%v after %v", err, time.Since(start))
	}
	close(release)
	if f := <-aDone; f.Value != 7 {
		t.Fatalf("A got %+v", f)
	}
	if fetches.Load() != 1 {
		t.Fatalf("fetches=%d, want exactly 1", fetches.Load())
	}
}

func TestFearGreedFailureSharedByQueuedCallers(t *testing.T) {
	const callers = 5
	var fetches atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	joined := make(chan struct{}, callers)
	s := newFearGreedSource("x", nil)
	s.onJoin = func() { joined <- struct{}{} }
	s.fetchFn = func(context.Context) (FearGreed, error) {
		if fetches.Add(1) == 1 {
			close(entered)
		}
		<-release
		return FearGreed{}, errors.New("down")
	}
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	errs := make(chan error, callers)
	for i := 0; i < callers; i++ {
		go func() { _, err := s.Get(context.Background()); errs <- err }()
	}
	<-entered
	for i := 0; i < callers-1; i++ { // every other caller has attached to the flight
		<-joined
	}
	once.Do(func() { close(release) })
	for i := 0; i < callers; i++ {
		if err := <-errs; err == nil {
			t.Fatal("want error")
		}
	}
	if n := fetches.Load(); n != 1 {
		t.Fatalf("fetches=%d, want 1 shared", n)
	}
}

// item 3: the caller that started the fetch gives up; another waiter still gets the result.
func TestFearGreedStarterCancelsOtherWaiterSucceeds(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	joined := make(chan struct{}, 1)
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	s := newFearGreedSource("x", nil)
	s.onJoin = func() { joined <- struct{}{} }
	var fetches atomic.Int32
	s.fetchFn = func(context.Context) (FearGreed, error) {
		fetches.Add(1)
		close(entered)
		<-release
		return FearGreed{Value: 9, Classification: "Fear"}, nil
	}
	ctxA, cancelA := context.WithCancel(context.Background())
	aErr := make(chan error, 1)
	go func() { _, err := s.Get(ctxA); aErr <- err }()
	<-entered
	bRes := make(chan FearGreed, 1)
	go func() { f, _ := s.Get(context.Background()); bRes <- f }()
	<-joined
	cancelA()
	if err := <-aErr; !errors.Is(err, context.Canceled) {
		t.Fatalf("A: %v", err)
	}
	once.Do(func() { close(release) })
	if f := <-bRes; f.Value != 9 {
		t.Fatalf("B got %+v", f)
	}
	if fetches.Load() != 1 {
		t.Fatalf("fetches=%d", fetches.Load())
	}
}

// item 4a
func TestFearGreedNegativeCache(t *testing.T) {
	var clock atomic.Int64
	clock.Store(1_000_000)
	var fetches atomic.Int32
	s := newFearGreedSource("x", nil)
	s.now = func() time.Time { return time.Unix(clock.Load(), 0) }
	s.fetchFn = func(context.Context) (FearGreed, error) {
		if fetches.Add(1) == 1 {
			return FearGreed{}, errors.New("down")
		}
		return FearGreed{Value: 3}, nil
	}
	_, err1 := s.Get(context.Background())
	clock.Add(30)
	_, err2 := s.Get(context.Background())
	if err1 == nil || err2 == nil || err1.Error() != err2.Error() || fetches.Load() != 1 {
		t.Fatalf("inside the window: %v %v fetches=%d (want same error, 1 fetch)", err1, err2, fetches.Load())
	}
	clock.Add(31)
	if f, err := s.Get(context.Background()); err != nil || f.Value != 3 || fetches.Load() != 2 {
		t.Fatalf("after the window: %+v %v fetches=%d", f, err, fetches.Load())
	}
}

// item 4b
func TestFearGreedRefusesRedirectToHTTP(t *testing.T) {
	var plainHits atomic.Int32
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { plainHits.Add(1); _, _ = w.Write([]byte(fngJSON)) }))
	defer plain.Close()
	tls := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ok" {
			_, _ = w.Write([]byte(fngJSON))
			return
		}
		http.Redirect(w, r, plain.URL, http.StatusFound)
	}))
	defer tls.Close()
	c := httpsOnly(tls.Client()) // accepts the test certificate
	s := newFearGreedSource(tls.URL+"/redir", c)
	if _, err := s.Get(context.Background()); err == nil || plainHits.Load() != 0 {
		t.Fatalf("redirect to http must be refused (err=%v, plain hits=%d)", err, plainHits.Load())
	}
	if f, err := newFearGreedSource(tls.URL+"/ok", c).Get(context.Background()); err != nil || f.Value != 25 {
		t.Fatalf("https still works: %+v %v", f, err)
	}
}

// item 5
func TestFearGreedStaleCeilingMeasuredAfterFetch(t *testing.T) {
	var clock atomic.Int64
	clock.Store(1_000_000)
	s := newFearGreedSource("x", nil)
	s.now = func() time.Time { return time.Unix(clock.Load(), 0) }
	calls := 0
	s.fetchFn = func(context.Context) (FearGreed, error) {
		calls++
		if calls == 1 {
			return FearGreed{Value: 25, Classification: "Fear"}, nil
		}
		clock.Add(int64(fearGreedMaxStale/time.Second) - 100) // before the fetch it is still eligible...
		clock.Add(200)                                        // ...but the failing fetch ends past 24h
		return FearGreed{}, errors.New("slow failure")
	}
	if _, err := s.Get(context.Background()); err != nil {
		t.Fatal(err)
	}
	clock.Add(int64(2 * fearGreedTTL / time.Second))
	if f, err := s.Get(context.Background()); err == nil {
		t.Fatalf("stale served past 24h: %+v", f)
	}
	// success records completion time
	s2 := newFearGreedSource("x", nil)
	var c2 atomic.Int64
	c2.Store(500)
	s2.now = func() time.Time { return time.Unix(c2.Load(), 0) }
	s2.fetchFn = func(context.Context) (FearGreed, error) { c2.Add(30); return FearGreed{Value: 1}, nil }
	_, _ = s2.Get(context.Background())
	if s2.at.Unix() != 530 {
		t.Fatalf("at=%d, want completion time 530", s2.at.Unix())
	}
}
