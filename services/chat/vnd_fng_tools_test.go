package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

type cryptoPrices struct{}

func (cryptoPrices) Sources() []string { return []string{"CoinGecko"} }
func (cryptoPrices) Get(context.Context, string) ([]PriceQuote, time.Time, error) {
	return []PriceQuote{{Type: "Bitcoin", Buy: 68000, Sell: 68000, Unit: "BTC", Currency: "USD"}}, time.Unix(0, 0), nil
}

type fakeRate struct {
	v  float64
	ok bool
}

func (f fakeRate) Rate(context.Context) (float64, bool) { return f.v, f.ok }

func fmtVND(usd, rate float64) string { return fmt.Sprintf("≈ %.0f ₫ (USDT/VND P2P)", usd*rate) }

func withCrypto(rd USDVNDReader) func(*Tools) {
	return func(tl *Tools) { tl.Prices, tl.USDVND, tl.FormatVND = cryptoPrices{}, rd, fmtVND }
}

func TestGetPricesAddsVNDWhenRateAvailable(t *testing.T) {
	r := newRig(t, withCrypto(fakeRate{26434, true}), toolStep(call("1", "get_prices", `{"source":"coingecko"}`)), textStep("ok"))
	r.ask("giá btc")
	got := r.toolResult(1)
	if !strings.Contains(got, "mua 68000, bán 68000 USD/BTC — ≈ 1797512000 ₫ (USDT/VND P2P)") {
		t.Fatalf("%q", got)
	}
	t.Logf("tool output with VND:\n%s", got)
}

func TestGetPricesUSDOnlyWhenRateUnavailable(t *testing.T) {
	for name, rd := range map[string]USDVNDReader{"unavailable": fakeRate{0, false}, "zero": fakeRate{0, true}, "nil": nil} {
		r := newRig(t, withCrypto(rd), toolStep(call("1", "get_prices", `{"source":"coingecko"}`)), textStep("ok"))
		r.ask("giá btc")
		got := r.toolResult(1)
		if strings.Contains(got, "₫") || strings.Contains(got, "≈") || !strings.Contains(got, "- Bitcoin: mua 68000, bán 68000 USD/BTC\n") {
			t.Errorf("%s: %q", name, got)
		}
		if name == "unavailable" {
			t.Logf("tool output USD only:\n%s", got)
		}
	}
}

func TestGetPricesVNDNeverOnVNDRows(t *testing.T) {
	r := newRig(t, func(tl *Tools) { tl.USDVND, tl.FormatVND = fakeRate{26434, true}, fmtVND }, toolStep(call("1", "get_prices", `{"source":"doji"}`)), textStep("ok"))
	r.ask("giá vàng")
	if got := r.toolResult(1); strings.Contains(got, "₫") {
		t.Fatalf("%q", got)
	}
}

type countingRate struct {
	calls int
	block bool
}

func (c *countingRate) Rate(ctx context.Context) (float64, bool) {
	c.calls++
	if c.block {
		<-ctx.Done()
		return 0, false
	}
	return 26434, true
}

func TestGoldOnlyLookupNeverCallsRate(t *testing.T) {
	cr := &countingRate{}
	r := newRig(t, func(tl *Tools) { tl.USDVND, tl.FormatVND = cr, fmtVND }, toolStep(call("1", "get_prices", `{"source":"doji"}`)), textStep("ok"))
	r.ask("giá vàng")
	if cr.calls != 0 {
		t.Fatalf("rate called %d times for gold-only quotes", cr.calls)
	}
}

func TestStalledRateStillReturnsUSDInTime(t *testing.T) {
	cr := &countingRate{block: true}
	r := newRig(t, withCrypto(cr), toolStep(call("1", "get_prices", `{"source":"coingecko"}`)), textStep("ok"))
	start := time.Now()
	r.ask("giá btc")
	got := r.toolResult(1)
	if time.Since(start) > vndRateWait+2*time.Second || cr.calls != 1 || strings.Contains(got, "₫") || !strings.Contains(got, "mua 68000, bán 68000 USD/BTC") {
		t.Fatalf("elapsed=%v calls=%d %q", time.Since(start), cr.calls, got)
	}
}

// Price retrieval eats most of the tool deadline and the rate source stalls:
// the tool must still return the USD quotes, not a timeout.
type slowPrices struct{ cryptoPrices }

func (slowPrices) Get(ctx context.Context, s string) ([]PriceQuote, time.Time, error) {
	time.Sleep(300 * time.Millisecond)
	return cryptoPrices{}.Get(ctx, s)
}

func TestVNDWaitReservesTimeBeforeDeadline(t *testing.T) {
	if got := vndWait(context.Background()); got != vndRateWait {
		t.Fatalf("no deadline: %v", got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	if got := vndWait(ctx); got <= 0 || got > 500*time.Millisecond {
		t.Fatalf("1.5s left: %v", got)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 900*time.Millisecond)
	defer cancel2()
	if got := vndWait(ctx2); got > 0 {
		t.Fatalf("less than the margin left must skip: %v", got)
	}
}

func TestStalledRateNearDeadlineStillReturnsUSD(t *testing.T) {
	cr := &countingRate{block: true}
	tl := Tools{Prices: slowPrices{}, USDVND: cr, FormatVND: fmtVND}
	var run func(context.Context, *turn, json.RawMessage) (string, error)
	for _, ti := range (&Service{tools: tl}).buildTools() {
		if ti.def.Name == "get_prices" {
			run = ti.run
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond) // ~900ms left after the 300ms fetch: below the margin
	defer cancel()
	got, err := run(ctx, nil, json.RawMessage(`{"source":"coingecko"}`))
	if err != nil || strings.Contains(got, "₫") || !strings.Contains(got, "mua 68000, bán 68000 USD/BTC") || cr.calls != 0 {
		t.Fatalf("err=%v calls=%d %q", err, cr.calls, got)
	}
}
