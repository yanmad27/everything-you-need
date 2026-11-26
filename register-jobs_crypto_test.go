package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	pricetracker "everything-you-need/m/services/price-tracker"
	telebot "everything-you-need/m/services/tele-bot"
)

type tgCapture struct{ bodies []map[string]any }

func (c *tgCapture) RoundTrip(r *http.Request) (*http.Response, error) {
	b, _ := io.ReadAll(r.Body)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	c.bodies = append(c.bodies, m)
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), Header: http.Header{}, Request: r}, nil
}

func newTG(c *tgCapture) *telebot.TeleBotService {
	return telebot.NewTeleBotServiceWithClient("TOKEN", &http.Client{Transport: c})
}

func fgAt(v int, class string) pricetracker.FearGreed {
	return pricetracker.FearGreed{Value: v, Classification: class, Date: time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC), HasPrev: true, Change: 2}
}

func stripTime(m string) string {
	l := strings.Split(m, "\n")
	l[1] = "<time>"
	return strings.Join(l, "\n")
}

func TestCryptoNotificationVNDTelegramPayload(t *testing.T) {
	c := &tgCapture{}
	tg := newTG(c)
	fg := fgAt(50, "Neutral")
	withVND := generatePriceNotificationVND(cryptoPrices(), nil, nil, fg.Line(), 26434)
	usdOnly := generatePriceNotificationVND(cryptoPrices(), nil, nil, fg.Line(), 0)
	for _, m := range []string{withVND, usdOnly} {
		if err := tg.SendToChannelWithMarkdown("-100", m); err != nil {
			t.Fatal(err)
		}
	}
	if len(c.bodies) != 2 || c.bodies[0]["parse_mode"] != "Markdown" || c.bodies[1]["parse_mode"] != "Markdown" {
		t.Fatalf("%v", c.bodies)
	}
	got, usd := c.bodies[0]["text"].(string), c.bodies[1]["text"].(string)
	if !strings.Contains(got, "BTC 68.000        1,80tỷ\n") || !strings.Contains(got, "ETH  3.400        89,9tr\n```\n≈ ₫ theo USDT/VND P2P\n") || strings.Count(got, "USDT/VND P2P") != 1 {
		t.Fatalf("%s", got)
	}
	if strings.Contains(usd, "₫") || strings.Contains(usd, "≈") {
		t.Fatalf("USD-only leaked VND:\n%s", usd)
	}
	// no VND for gold rows even with a rate
	gold := map[string][]pricetracker.Price{"Doji": {{Type: "SJC 1L", Currency: "VND", BuyPrice: 1000, SellPrice: 2000}}}
	if m := generatePriceNotificationVND(gold, nil, nil, "", 26434); strings.Contains(m, "≈") {
		t.Fatalf("gold got VND:\n%s", m)
	}
	// USD side stays byte-identical to the pre-change renderer
	if stripTime(usdOnly) != stripTime(generatePriceNotificationFG(cryptoPrices(), nil, nil, fg.Line())) {
		t.Fatal("rate 0 must equal the old renderer")
	}
	t.Logf("=== Telegram payload: crypto report WITH VND (parse_mode=Markdown) ===\n%s\n=== Telegram payload: rate unavailable (USD only) ===\n%s", stripTime(got), stripTime(usd))
}
