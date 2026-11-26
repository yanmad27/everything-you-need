package main

import (
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	pricetracker "everything-you-need/m/services/price-tracker"
	"golang.org/x/text/unicode/norm"
)

func TestAbbreviateCuratedAndFallback(t *testing.T) {
	for in, want := range map[string]string{
		"NHẪN TRÒN 9999 HƯNG THỊNH VƯỢNG":    "Nhẫn 9999",
		"VÀNG MIẾNG SJC":                     "SJC miếng",
		"Vàng miếng SJC (Vàng SJC)":          "SJC miếng",
		"KIM TT/AVPL":                        "Kim TT/AVPL",
		"VÀNG NGUYÊN LIỆU 9999":              "Vàng NL 9999",
		"NỮ TRANG 9999":                      "Trang 9999",
		"VÀNG MIẾNG 985":                     "Miếng 985",
		"KIM KHOA 999.9 ÉP VĨ":               "KK 999.9 vĩ",
		"NHẪN 97%":                           "Nhẫn 97%",
		"BẠC 999 (MIẾNG - THANH - THỎI)":     "Bạc 999",
		"Supercalifragilisticexpialidocious": "Supercalifra",
		"":                                   "-",
	} {
		if got := abbreviate(in, 12); got != want {
			t.Errorf("abbreviate(%q)=%q want %q", in, got, want)
		}
	}
	// decomposed input (NFD) maps to the same label as NFC input
	if got := abbreviate(norm.NFD.String("NHẪN TRÒN 9999 HƯNG THỊNH VƯỢNG"), 12); got != "Nhẫn 9999" {
		t.Errorf("NFD input: %q", got)
	}
}

func TestCleanNameStripsUnsafeRunes(t *testing.T) {
	got := cleanName("Vàng `9999`\\ *x*_y_ [z] 🔥 🇻🇳 👨‍👩‍👧 ệ")
	if strings.ContainsAny(got, "`\\*_[]🔥🇻🇳") || got != norm.NFC.String(got) {
		t.Errorf("%q", got)
	}
	if !strings.Contains(got, "ệ") {
		t.Errorf("precomposed letter lost: %q", got)
	}
	for _, r := range got {
		if r > 0x2000 {
			t.Errorf("wide/odd rune %U in %q", r, got)
		}
	}
}

func TestUniqueNames(t *testing.T) {
	in := []string{"VÀNG MIẾNG SJC", "Vàng miếng SJC (x)", "VÀNG MIẾNG SJC", "Supercalifragilistic A", "Supercalifragilistic B", "Supercalifragilistic C"}
	got := uniqueNames(in, 12)
	want := []string{"SJC miếng", "SJC miếng2", "SJC miếng3", "Supercalifra", "Supercalifr2", "Supercalifr3"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%d: %q want %q (%v)", i, got[i], want[i], got)
		}
	}
	seen := map[string]bool{}
	for _, n := range uniqueNames(strings.Split(strings.Repeat("Token 9999,", 15), ","), 6) {
		if seen[n] || utf8.RuneCountInString(n) > 6 {
			t.Errorf("duplicate or too long: %q", n)
		}
		seen[n] = true
	}
}

func TestMarkdownValidityBackticksAndBold(t *testing.T) {
	prices := map[string][]pricetracker.Price{
		"Doji":      {{Type: "SJC `x` *bold* _it_ \\ [l](u)", Currency: "VND", Unit: "nghìn/`chỉ`", BuyPrice: 1000, SellPrice: 2000}},
		"CoinGecko": {{Type: "Bitcoin `BTC`", Currency: "USD", Unit: "BT`C", BuyPrice: 5}},
	}
	m := generatePriceNotificationVND(prices, nil, nil, compactFearGreed(fgAt(20, "Extreme Fear")), 26500)
	assertCompact(t, "markdown", m)
	if strings.Count(m, "```")%2 != 0 || strings.Count(m, "```") != 4 {
		t.Errorf("fences:\n%s", m)
	}
	// strip the pre blocks: what remains is plain legacy Markdown with balanced bold/italic
	rest := regexp.MustCompile("(?s)```.*?```").ReplaceAllString(m, "")
	if strings.Contains(rest, "`") || strings.Count(rest, "*")%2 != 0 || strings.Count(rest, "_")%2 != 0 {
		t.Errorf("outside text invalid:\n%s", rest)
	}
	if !strings.Contains(m, "⚠️ *😱 F&G: 20 — Extreme Fear*") || !strings.HasSuffix(m, "🤖 _Automated update_") {
		t.Errorf("F&G/footer changed:\n%s", m)
	}
}

func TestProductionTables(t *testing.T) {
	p, c := productionFixture()
	fg := fearGreedLine(stubFG{f: fgAt(20, "Extreme Fear")})
	with := generatePriceNotificationVND(p, c, nil, fg, 26500)
	without := generatePriceNotificationVND(p, c, nil, fg, 0)
	if assertCompact(t, "with", with); t.Failed() {
		t.Fatal(with)
	}
	assertCompact(t, "without", without)

	blocks := func(m string) [][]string {
		var out [][]string
		for _, b := range regexp.MustCompile("(?s)```\n(.*?)\n```").FindAllStringSubmatch(m, -1) {
			out = append(out, strings.Split(b[1], "\n"))
		}
		return out
	}
	bw, wb := blocks(with), blocks(without)
	if len(bw) != 4 || len(wb) != 4 {
		t.Fatalf("tables with=%d without=%d", len(bw), len(wb))
	}
	wantRows := []int{4, 4, 2, 3} // Doji, Kim Khoa, Mi Hồng, CoinGecko
	for i, b := range bw {
		if len(b)-2 != wantRows[i] {
			t.Errorf("table %d rows %d want %d", i, len(b)-2, wantRows[i])
		}
		names := map[string]bool{}
		for _, l := range b[2:] {
			n := strings.TrimSpace(l[:utf8.RuneCountInString(l)-utf8.RuneCountInString(strings.TrimLeft(l[strings.IndexByte(l, ' '):], " "))])
			if i < 3 {
				n = strings.TrimSpace(l[:12])
			}
			if names[n] {
				t.Errorf("duplicate name %q in table %d", n, i)
			}
			names[n] = true
		}
		t.Logf("table %d (%s): max row width %d, rows %d", i, strings.TrimSpace(b[0]), utf8.RuneCountInString(b[1]), len(b)-2)
	}
	if !strings.HasPrefix(strings.TrimSpace(bw[3][1]), "price") || !strings.Contains(bw[3][1], "VND") || strings.Contains(wb[3][1], "VND") {
		t.Errorf("crypto headers: %q / %q", bw[3][1], wb[3][1])
	}
	if strings.Count(with, "USDT/VND P2P") != 1 || strings.Contains(without, "₫") || strings.Contains(without, "P2P") {
		t.Error("P2P footer once with VND, never without")
	}
	for _, b := range bw[:3] {
		if !strings.Contains(b[1], "Mua") || !strings.Contains(b[1], "Bán") {
			t.Errorf("gold header %q", b[1])
		}
	}
	for _, gone := range []string{"items)", "📈", "📉", "💰", "💵"} {
		if strings.Contains(with, gone) {
			t.Errorf("leaked %q", gone)
		}
	}
	if !strings.Contains(with, "*Bảo Tín Minh Châu*\n• N/A\n") {
		t.Error("N/A section changed")
	}
	if a, b := strings.Index(with, "Doji"), strings.Index(with, "CoinGecko"); a > b || strings.Index(with, "N/A") > a {
		t.Error("section order changed")
	}
}
