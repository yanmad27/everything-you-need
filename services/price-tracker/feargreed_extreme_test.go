package pricetracker

import (
	"strings"
	"testing"
)

func TestFearGreedExtremeBoundaries(t *testing.T) {
	for v := 0; v <= 100; v++ {
		f := FearGreed{Value: v, Classification: "Neutral"}
		want := v <= 25 || v >= 75
		if f.Extreme() != want {
			t.Fatalf("%d: extreme=%v want %v", v, f.Extreme(), want)
		}
		plain, tg := f.Line(), f.TelegramMarkdownLine()
		base := f.baseLine()
		if want {
			if plain != WarnEmoji+" "+base || tg != WarnEmoji+" *"+base+"*" {
				t.Fatalf("%d extreme rendering: %q | %q", v, plain, tg)
			}
		} else if plain != base || tg != base || strings.Contains(plain+tg, WarnEmoji) || strings.ContainsAny(tg, "*") {
			t.Fatalf("%d must render exactly as today: %q | %q", v, plain, tg)
		}
	}
}

// Telegram legacy Markdown: bold must be balanced and the surrounding text
// free of _ [ ` so it cannot open an unintended entity.
func TestFearGreedTelegramLegacyMarkdownValid(t *testing.T) {
	for _, f := range []FearGreed{
		{Value: 20, Classification: "Extreme Fear", HasPrev: true, Change: -4},
		{Value: 80, Classification: "Extreme Greed", HasPrev: true, Change: 2},
		{Value: 0, Classification: "bad_*[`label"}, {Value: 100, Classification: "Extreme Greed"},
		{Value: 50, Classification: "Neutral", HasPrev: true},
	} {
		l := f.TelegramMarkdownLine()
		if strings.ContainsAny(f.baseLine(), "_*[`") {
			t.Errorf("base text carries markdown specials: %q", f.baseLine())
		}
		if n := strings.Count(l, "*"); n != 0 && n != 2 {
			t.Errorf("unbalanced bold: %q", l)
		}
		if f.Extreme() && (!strings.Contains(l, " *") || !strings.HasSuffix(l, "*")) {
			t.Errorf("bold must wrap the line: %q", l)
		}
	}
}
