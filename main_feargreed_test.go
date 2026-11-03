package main

import (
	"context"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"everything-you-need/m/services/botconfig"
	"everything-you-need/m/services/chat"
	"everything-you-need/m/services/config"
	"everything-you-need/m/services/convo"
	pricetracker "everything-you-need/m/services/price-tracker"
)

type stubFG struct {
	f   pricetracker.FearGreed
	err error
}

func (s stubFG) Get(context.Context) (pricetracker.FearGreed, error) { return s.f, s.err }

func sampleFG() pricetracker.FearGreed {
	return pricetracker.FearGreed{Value: 25, Classification: "Fear", Date: time.Date(2025, 11, 3, 0, 0, 0, 0, time.UTC), HasPrev: true, Change: -3}
}

func cryptoPrices() map[string][]pricetracker.Price {
	return map[string][]pricetracker.Price{"CoinGecko": {
		{Type: "Bitcoin", Currency: "USD", BuyPrice: 68000, SellPrice: 68000, Unit: "BTC"},
		{Type: "Ethereum", Currency: "USD", BuyPrice: 3400, SellPrice: 3400, Unit: "ETH"},
	}}
}

func TestPriceNotificationFearGreedLineAndFallback(t *testing.T) {
	line := sampleFG().Line()
	with := generatePriceNotificationFG(cryptoPrices(), nil, nil, line)
	if n := strings.Count(with, line); n != 1 {
		t.Fatalf("F&G line appears %d times:\n%s", n, with)
	}
	if strings.Index(with, line) < strings.Index(with, "Bitcoin") || !strings.Contains(with, "Automated update") {
		t.Fatalf("F&G line must follow the crypto prices:\n%s", with)
	}

	// Source failure: the message still renders, without the line, one log line.
	var logged strings.Builder
	oldOut := logSink(&logged)
	defer oldOut()
	got := fearGreedLine(stubFG{err: errors.New("upstream detail")})
	without := generatePriceNotificationFG(cryptoPrices(), nil, nil, got)
	if got != "" || strings.Contains(without, "Fear & Greed") || !strings.Contains(without, "Bitcoin") {
		t.Fatalf("fallback wrong (line=%q):\n%s", got, without)
	}
	if n := strings.Count(strings.TrimSpace(logged.String()), "\n") + 1; n != 1 || !strings.Contains(logged.String(), "fear&greed unavailable") {
		t.Fatalf("want exactly one log line, got %q", logged.String())
	}
	// Gold-only messages never get the crypto index line.
	gold := map[string][]pricetracker.Price{"Doji": {{Type: "SJC 1L", Currency: "VND", BuyPrice: 1, SellPrice: 2}}}
	if m := generatePriceNotificationFG(gold, nil, nil, line); strings.Contains(m, "Fear & Greed") {
		t.Fatalf("gold-only message got the line:\n%s", m)
	}
	// Old entry point unchanged.
	if m := generatePriceNotification(cryptoPrices(), nil, nil); strings.Contains(m, "Fear & Greed") {
		t.Fatal("generatePriceNotification must not add the line")
	}
	t.Logf("=== price message WITH F&G line ===\n%s\n=== price message, F&G source failed ===\n%s", with, without)
}

// item 4
func TestFearGreedCommandDetection(t *testing.T) {
	for _, alias := range []string{"/feargreed", "/fng"} {
		for _, c := range []struct {
			text, user string
			want       bool
		}{
			{alias, "", true}, {alias, "PinkBot", true}, {alias + " now", "PinkBot", true},
			{alias + "@PinkBot", "pinkbot", true}, {alias + "@pinkbot", "@PinkBot", true}, // own suffix, case-insensitive
			{alias + "@OtherBot", "PinkBot", false}, // foreign suffix
			{alias + "@PinkBot", "", false},         // username unknown: suffixed commands ignored
			{alias + "@", "PinkBot", false},
		} {
			if got := fearGreedCommand(c.text, c.user); got != c.want {
				t.Errorf("%q (bot %q) -> %v want %v", c.text, c.user, got, c.want)
			}
		}
	}
	for _, in := range []string{"feargreed", "hello /feargreed", "/feargreeds", "/fngx", ""} {
		if fearGreedCommand(in, "PinkBot") {
			t.Errorf("%q matched", in)
		}
	}
}

// item 6: the old renderer is pinned (time line is the only moving part).
func TestGeneratePriceNotificationPinned(t *testing.T) {
	m := generatePriceNotification(cryptoPrices(), nil, nil)
	lines := strings.Split(m, "\n")
	lines[1] = "<time>"
	want := "📊 *Price Update*\n<time>\n\n*CoinGecko* (2 items)\n• 💵 USD: *68.000* - Bitcoin\n• 💵 USD: *3.400* - Ethereum\n\n🤖 _Automated update_"
	if got := strings.Join(lines, "\n"); got != want {
		t.Fatalf("renderer changed:\n%q\nwant\n%q", got, want)
	}
	if generatePriceNotificationFG(cryptoPrices(), nil, nil, "") != generatePriceNotificationFG(cryptoPrices(), nil, nil, "") {
		t.Fatal("non-deterministic")
	}
}

// item 1 in the Telegram message.
func TestPriceNotificationUnknownLabelFallback(t *testing.T) {
	f := sampleFG()
	f.Classification = ""
	m := generatePriceNotificationFG(cryptoPrices(), nil, nil, f.Line())
	if !strings.Contains(m, "📉 Fear & Greed: 25 — Không rõ · ▼3 so với hôm qua") || !strings.Contains(m, "Bitcoin") {
		t.Fatalf("%s", m)
	}
}

func TestFearGreedCommandReply(t *testing.T) {
	var sent []string
	send := func(s string) error { sent = append(sent, s); return nil }
	replyFearGreed(context.Background(), stubFG{f: sampleFG()}, send)
	if len(sent) != 1 || sent[0] != "😨 Fear & Greed: 25 — Sợ hãi (Fear) · ▼3 so với hôm qua" {
		t.Fatalf("%q", sent)
	}
	var logged strings.Builder
	defer logSink(&logged)()
	replyFearGreed(context.Background(), stubFG{err: errors.New("upstream detail")}, send)
	if len(sent) != 2 || strings.Contains(sent[1], "upstream detail") || !strings.Contains(sent[1], "Fear & Greed") {
		t.Fatalf("%q", sent)
	}
	t.Logf("command ok: %s | command fail: %s", sent[0], sent[1])
}

func TestZaloFearGreedToggleDefaultOnAndLive(t *testing.T) {
	st, err := convo.Open(filepath.Join(t.TempDir(), "c.db"), convo.Options{NoBackground: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cfg := &config.Config{}
	cfg.Zalo.Enabled = true
	prov, err := botconfig.New(context.Background(), st, "0123456789abcdef0123456789abcdef-M", map[string]botconfig.Defaults{"zalo": botconfig.ZaloDefaults(cfg, "")})
	if err != nil {
		t.Fatal(err)
	}
	spy, snd := &toolSpy{}, &sink{}
	zl := &zaloLive{prov: prov}
	tl := chat.Tools{Platform: "zalo", FearGreed: fearGreedAdapter{pricetracker.NewFearGreedSource()}, Recorder: st}
	svc := chat.NewServiceWithCompleter(&countLLM{}, snd).WithTools(spy, tl).WithLive(zl.source)
	ctx := context.Background()
	dispatchZalo(ctx, nil, svc, nil, zmsg("xin chào"))
	if !strings.Contains(spy.defs[0], "fear_greed_index") {
		t.Fatalf("default on: %s", spy.defs[0])
	}
	if err := prov.Save(ctx, "zalo", map[string]string{botconfig.KeyToolFearGreed: "false"}, "owner"); err != nil {
		t.Fatal(err)
	}
	dispatchZalo(ctx, nil, svc, nil, zmsg("lần hai"))
	if strings.Contains(spy.defs[1], "fear_greed_index") || !strings.Contains(spy.defs[1], "lunar_date") {
		t.Fatalf("toggle off ignored or too broad: %s", spy.defs[1])
	}
}

// logSink redirects the std logger to w and returns the restore func.
func logSink(w io.Writer) func() {
	log.SetOutput(w)
	return func() { log.SetOutput(os.Stderr) }
}
