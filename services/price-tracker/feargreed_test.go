package pricetracker

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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
	if l := (FearGreed{Value: 50, Classification: "Weird"}).Line(); !strings.Contains(l, "Weird") {
		t.Errorf("unknown label dropped: %q", l)
	}
}

func TestFearGreedChangeCalculation(t *testing.T) {
	up, _ := parseFearGreed([]byte(`{"data":[{"value":"70","value_classification":"Greed","timestamp":"200"},{"value":"60","value_classification":"Greed","timestamp":"100"}]}`))
	if up.Change != 10 || !strings.Contains(up.Line(), "▲10") {
		t.Errorf("up: %+v %s", up, up.Line())
	}
	same, _ := parseFearGreed([]byte(`{"data":[{"value":"50","value_classification":"Neutral","timestamp":"200"},{"value":"50","value_classification":"Neutral","timestamp":"100"}]}`))
	if !strings.Contains(same.Line(), "không đổi") {
		t.Errorf("same: %s", same.Line())
	}
	// out-of-order entries: newest timestamp is today
	ooo, _ := parseFearGreed([]byte(`{"data":[{"value":"10","value_classification":"Extreme Fear","timestamp":"100"},{"value":"15","value_classification":"Extreme Fear","timestamp":"200"}]}`))
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
	f, err = parseFearGreed([]byte(`{"data":[{"value":"25","value_classification":"Fear","timestamp":"200"},{"value":"x","value_classification":"Fear","timestamp":"100"}]}`))
	if err != nil || f.HasPrev {
		t.Fatalf("%+v %v", f, err)
	}
}

func TestParseFearGreedRejectsGarbage(t *testing.T) {
	for _, in := range []string{``, `{}`, `{"data":[]}`, `{"data":[{"value":"abc","timestamp":"1"}]}`, `{"data":[{"value":"250","timestamp":"1"}]}`, `<html>`} {
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
	r.fail.Store(false) // failures are not cached
	if f, err := r.src.Get(context.Background()); err != nil || f.Value != 25 {
		t.Fatalf("%+v %v", f, err)
	}
}

func TestFearGreedHonoursContext(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { <-release }))
	defer srv.Close()
	defer close(release)
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
