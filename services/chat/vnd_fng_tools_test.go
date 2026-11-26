package chat

import (
	"context"
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
