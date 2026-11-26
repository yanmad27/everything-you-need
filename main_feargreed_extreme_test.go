package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	zalobot "everything-you-need/m/services/zalo-bot"
)

func TestFearGreedNotificationTelegramPayload(t *testing.T) {
	for _, c := range []struct {
		v     int
		class string
		warn  bool
	}{{50, "Neutral", false}, {26, "Fear", false}, {74, "Greed", false}, {20, "Extreme Fear", true}, {25, "Extreme Fear", true}, {75, "Extreme Greed", true}, {80, "Extreme Greed", true}} {
		cap := &tgCapture{}
		line := fearGreedLine(stubFG{f: fgAt(c.v, c.class)})
		if err := newTG(cap).SendToChannelWithMarkdown("-100", generatePriceNotificationVND(cryptoPrices(), nil, nil, line, 26434)); err != nil {
			t.Fatal(err)
		}
		text := cap.bodies[0]["text"].(string)
		fgLine := ""
		for _, l := range strings.Split(text, "\n") {
			if strings.Contains(l, "Fear & Greed") {
				fgLine = l
			}
		}
		if cap.bodies[0]["parse_mode"] != "Markdown" || strings.Contains(fgLine, "⚠️") != c.warn || strings.HasPrefix(fgLine, "⚠️ *") != c.warn || strings.Count(fgLine, "*") != map[bool]int{true: 2, false: 0}[c.warn] {
			t.Fatalf("%d: %q", c.v, fgLine)
		}
		if strings.ContainsAny(fgLine, "_[`") {
			t.Fatalf("legacy Markdown specials in %q", fgLine)
		}
		if c.v == 50 || c.v == 20 || c.v == 80 {
			t.Logf("=== Telegram notification payload F&G=%d ===\n%s", c.v, stripTime(text))
		}
	}
}

func TestFearGreedDirectCommandTelegramPayload(t *testing.T) {
	for _, c := range []struct {
		v     int
		class string
		warn  bool
	}{{50, "Neutral", false}, {26, "Fear", false}, {74, "Greed", false}, {20, "Extreme Fear", true}, {75, "Extreme Greed", true}, {80, "Extreme Greed", true}} {
		cap := &tgCapture{}
		tg := newTG(cap)
		replyFearGreed(context.Background(), stubFG{f: fgAt(c.v, c.class)}, func(s string) error { return tg.SendReply(context.Background(), "42", 7, s) })
		b := cap.bodies[0]
		text := b["text"].(string)
		if _, has := b["parse_mode"]; has && b["parse_mode"] != "" || strings.Contains(text, "*") || strings.HasPrefix(text, "⚠️ ") != c.warn {
			t.Fatalf("%d: %v", c.v, b)
		}
		if c.v == 50 || c.v == 20 || c.v == 80 {
			t.Logf("=== Telegram /fng payload F&G=%d (plain, no parse_mode) ===\n%v", c.v, b)
		}
	}
}

func TestZaloMarkdownTransportKeepsExtremeEmphasis(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = nil
		_ = json.NewDecoder(bytes.NewReader(b)).Decode(&body)
		io.WriteString(w, `{"ok":true,"result":{}}`)
	}))
	defer srv.Close()
	z := zalobot.NewClient("TOKEN", srv.URL)
	msg := "⚠️ **Fear & Greed: 20/100 — Sợ hãi tột độ (Extreme Fear)**"
	if err := z.SendMarkdown(context.Background(), "chat", msg); err != nil {
		t.Fatal(err)
	}
	if body["parse_mode"] != "markdown" || body["text"] != msg {
		t.Fatalf("%v", body)
	}
	t.Logf("=== Zalo payload (extreme F&G) ===\n%v", body)
}
