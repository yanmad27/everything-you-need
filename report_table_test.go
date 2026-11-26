package main

import (
	"math"
	"math/rand"
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

func TestTableRowsNeverExceedBudgetProperty(t *testing.T) {
	vals := []float64{0, 1, 598, 12950, 999999, 1e7, 1e8, 100000000, 1e9, 1e12, 2.65e12, 1e15, 1e300, -5, -123456789, math.MaxFloat64, math.Inf(1), math.NaN()}
	chgs := []float64{0, 0.3, -1.1, 100, 999.94, 1000, -1000, 123456, math.Inf(-1), math.NaN()}
	rates := []float64{0, 1, 26500, 1e6, 1e12}
	rnd := rand.New(rand.NewSource(11))
	for iter := 0; iter < 600; iter++ {
		pick := func() float64 { return vals[rnd.Intn(len(vals))] }
		n := 1 + rnd.Intn(4)
		var gold, crypto []pricetracker.Price
		var gch, cch []pricetracker.PriceChange
		for i := 0; i < n; i++ {
			name := []string{"SJC", "Bitcoin", "NHẪN TRÒN 9999 HƯNG THỊNH VƯỢNG", "Token 9999 Supercalifragilistic"}[rnd.Intn(4)]
			g := pricetracker.Price{Type: name, Currency: "VND", Unit: "nghìn/chỉ", BuyPrice: pick(), SellPrice: pick()}
			c := pricetracker.Price{Type: name, Currency: "USD", Unit: []string{"BTC", "", "WRAPPED-LONG-UNIT"}[rnd.Intn(3)], BuyPrice: pick()}
			gold, crypto = append(gold, g), append(crypto, c)
			gch = append(gch, pricetracker.PriceChange{Price: g, BuyChange: chgs[rnd.Intn(len(chgs))], SellChange: chgs[rnd.Intn(len(chgs))]})
			cch = append(cch, pricetracker.PriceChange{Price: c, BuyChange: chgs[rnd.Intn(len(chgs))]})
		}
		m := generatePriceNotificationVND(
			map[string][]pricetracker.Price{"Doji": gold, "CoinGecko": crypto},
			map[string][]pricetracker.PriceChange{"Doji": gch, "CoinGecko": cch},
			nil, compactFearGreed(fgAt(20, "Extreme Fear")), rates[rnd.Intn(len(rates))])
		if assertCompact(t, "prop", m); t.Failed() {
			t.Fatalf("iteration %d:\n%s", iter, m)
		}
	}
	// the exact case from review: BTC 100.000.000 +100.0% with 2650tỷ
	m := generatePriceNotificationVND(map[string][]pricetracker.Price{"CoinGecko": {{Type: "Bitcoin", Currency: "USD", Unit: "BTC", BuyPrice: 1e8}}},
		map[string][]pricetracker.PriceChange{"CoinGecko": {{Price: pricetracker.Price{Type: "Bitcoin"}, BuyChange: 100}}}, nil, "", 26500)
	assertCompact(t, "review case", m)
	if !strings.Contains(m, "BTC") {
		t.Errorf("%s", m)
	}
}

func TestCompactNumber(t *testing.T) {
	for v, want := range map[float64]string{0: "0", 598: "598", 12950: "13,0k", 999999: "1,00tr", 1.61e9: "1,61tỷ", 2.65e12: ">999tỷ", -5: "-5", -2.65e12: "<-999tỷ", 123456789: "123tr"} {
		if got := compactNumber(v); got != want {
			t.Errorf("%v: %q want %q", v, got, want)
		}
	}
	for _, v := range []float64{math.Inf(1), math.Inf(-1), math.NaN(), math.MaxFloat64, -1e11} {
		if runeLen(compactNumber(v)) > 7 {
			t.Errorf("%v too wide: %q", v, compactNumber(v))
		}
	}
	if signedPercent(1000) != ">999%" || signedPercent(-5000) != "<-999%" || signedPercent(-0.04) != "0.0%" {
		t.Error("signedPercent caps")
	}
}

func TestFitTableDegradesDeterministically(t *testing.T) {
	wide := func(w int) tableCol { return tableCol{header: "h", cells: []string{strings.Repeat("9", w)}} }
	levels := map[int][]tableCol{0: {wide(12), wide(12)}, 1: {wide(13), wide(13)}, 2: {wide(5)}}
	pt := fitTable("T · x", []string{"A"}, 3, func(l int) []tableCol { return levels[l] }, func(l int) string {
		if l == 2 {
			return vndDroppedNote
		}
		return ""
	})
	if len(pt.cols) != 1 || pt.note != vndDroppedNote || pt.nameMax != tableBudget-6 {
		t.Errorf("%+v", pt)
	}
	// review case: BTC 100.000.000 +100.0% with a huge VND value stays within budget
	m := generatePriceNotificationVND(map[string][]pricetracker.Price{"CoinGecko": {{Type: "Bitcoin", Currency: "USD", Unit: "BTC", BuyPrice: 1e8}}},
		map[string][]pricetracker.PriceChange{"CoinGecko": {{Price: pricetracker.Price{Type: "Bitcoin"}, BuyChange: 100}}}, nil, "", 26500)
	assertCompact(t, "review", m)
	if !strings.Contains(m, "+100.0%") || !strings.Contains(m, "100tr") {
		t.Errorf("%s", m)
	}
}

func TestUniqueAcrossSection(t *testing.T) {
	// same shortened prefix lands in two tables (two units) of one source
	var prices []pricetracker.Price
	for _, u := range []string{"nghìn/chỉ", "nghìn/lượng"} {
		for _, n := range []string{"Token 9999 alpha", "Token 9999 beta"} {
			prices = append(prices, pricetracker.Price{Type: n, Currency: "VND", Unit: u, BuyPrice: 1, SellPrice: 2})
		}
	}
	m := generatePriceNotificationVND(map[string][]pricetracker.Price{"Doji": prices}, nil, nil, "", 0)
	assertCompact(t, "section", m)
	seen := map[string]bool{}
	for _, b := range regexp.MustCompile("(?s)```\n(.*?)\n```").FindAllStringSubmatch(m, -1) {
		for _, l := range strings.Split(b[1], "\n")[2:] {
			n := regexp.MustCompile(`^(.+?)\s+1\s`).FindStringSubmatch(l)[1]
			if seen[n] {
				t.Errorf("duplicate label %q across tables:\n%s", n, m)
			}
			seen[n] = true
		}
	}
	if len(seen) != 4 {
		t.Errorf("labels %v", seen)
	}
}

func TestUniqueNamesManyCollisionsNoPanic(t *testing.T) {
	for _, max := range []int{1, 2, 3, 4, 6, 12} {
		raws := make([]string, 150)
		for i := range raws {
			raws[i] = "Supercalifragilistic variant"
		}
		got := uniqueNames(raws, max)
		seen := map[string]bool{}
		for _, n := range got {
			if runeLen(n) > max {
				t.Errorf("max %d: %q too wide", max, n)
			}
			seen[n] = true
		}
		if capacity := math.Pow(10, float64(max)); capacity > 1000 && len(seen) != 150 {
			t.Errorf("max %d: %d distinct of 150", max, len(seen))
		}
	}
	if got := truncRunes("abc", -1); got != "" {
		t.Errorf("negative bound: %q", got)
	}
}

func TestGuardWidthSkipsPreBlocks(t *testing.T) {
	long := strings.Repeat("x", 60)
	in := "short\n```\n" + long + "  " + long + "\n```\n" + strings.Repeat("word ", 20)
	out := guardWidth(in)
	if !strings.Contains(out, "```\n"+long+"  "+long+"\n```") {
		t.Errorf("pre content was altered:\n%s", out)
	}
	if strings.Count(out, "\n") <= strings.Count(in, "\n") {
		t.Errorf("text outside the block was not wrapped:\n%s", out)
	}
}

func TestFenceFollowedByNewline(t *testing.T) {
	p, c := productionFixture()
	for _, rate := range []float64{0, 26500} {
		m := generatePriceNotificationVND(p, c, nil, "", rate)
		if strings.Count(m, "```") != 8 {
			t.Fatalf("fences:\n%s", m)
		}
		for i := 0; ; {
			j := strings.Index(m[i:], "```")
			if j < 0 {
				break
			}
			end := i + j + 3
			if end >= len(m) || m[end] != '\n' {
				t.Errorf("fence at %d not followed by newline: %q", i+j, m[i+j:min(len(m), end+10)])
			}
			i = end
		}
	}
}
