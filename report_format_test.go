package main

import (
	"math/rand"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	pricetracker "everything-you-need/m/services/price-tracker"
)

// visibleWidth is the test oracle for the 30-column target: Markdown markers are
// ignored, emoji / flags / East Asian wide runes count 2, accents count 1.
func visibleWidth(line string) int {
	total, cur := 0, 0 // cur: width of the unit being built
	joined, prevRI := false, false
	for _, r := range strings.NewReplacer("*", "", "_", "").Replace(line) {
		ri := r >= 0x1F1E6 && r <= 0x1F1FF
		switch {
		case r == 0xFE0F || r == 0x20E3:
			total += 2 - cur
			cur = 2
		case r == 0x200D:
			joined = true
		case unicode.Is(unicode.Mn, r) || (r >= 0x1F3FB && r <= 0x1F3FF) || joined:
			joined = false
		case ri && prevRI:
			prevRI = false
			continue
		default:
			cur = 1
			if ri || r >= 0x1F000 && r <= 0x1FAFF || r >= 0x2600 && r <= 0x27BF || r >= 0x2B00 && r <= 0x2BFF ||
				r >= 0x1100 && r <= 0x115F || r >= 0x2E80 && r <= 0xA4CF || r >= 0xAC00 && r <= 0xD7A3 ||
				r >= 0xF900 && r <= 0xFAFF || r >= 0xFE30 && r <= 0xFE6F || r >= 0xFF00 && r <= 0xFF60 ||
				r >= 0xFFE0 && r <= 0xFFE6 || r >= 0x20000 && r <= 0x3FFFD {
				cur = 2
			}
			total += cur
		}
		prevRI = ri
	}
	return total
}

func TestVisibleWidthOracle(t *testing.T) {
	for in, want := range map[string]int{
		"abc": 3, "*abc* _x_": 5, "Vàng nhẫn Tròn Trơn": 19, "📈+1.2%": 7, "⚠️ *😱 F&G*": 9, "💰 VND": 6, "🇻🇳": 2, "↔️": 2, "↔": 1, "1️⃣": 2, "#️⃣": 2, "©️": 2, "©": 1, "ệ": 1, "e\u0323\u0302": 1, "20x" + strings.Repeat("↔️", 20): 43, "🟠": 2, "👨‍👩‍👧": 2, "👍🏽": 2, "a👨‍👩‍👧b": 4, "✨⬛": 4, "": 0,
	} {
		if got := visibleWidth(in); got != want {
			t.Errorf("%q: %d want %d", in, got, want)
		}
	}
}

// assertCompact checks a full report: text lines stay within 30 visible columns
// with balanced Markdown; lines inside ``` blocks stay within tableBudget runes,
// hold no backtick/backslash/emoji, and every line of one block has equal width.
// It returns the widest text line.
func assertCompact(t *testing.T, name, msg string) int {
	t.Helper()
	max, inBlock, blockW := 0, false, -1
	for _, l := range strings.Split(msg, "\n") {
		if l == "```" {
			inBlock, blockW = !inBlock, -1
			continue
		}
		if inBlock {
			n := utf8.RuneCountInString(l)
			if n > tableBudget {
				t.Errorf("%s: table row %d > %d: %q", name, n, tableBudget, l)
			}
			if blockW >= 0 && n != blockW {
				t.Errorf("%s: unequal row width %d vs %d: %q", name, n, blockW, l)
			}
			blockW = n
			if visibleWidth(l) != n || strings.ContainsAny(l, "`\\") {
				t.Errorf("%s: wide/special rune in table row %q", name, l)
			}
			continue
		}
		w := visibleWidth(l)
		if w > max {
			max = w
		}
		if w > 30 {
			t.Errorf("%s: width %d > 30: %q", name, w, l)
		}
		if strings.Count(l, "*")%2 != 0 || strings.Count(l, "_")%2 != 0 {
			t.Errorf("%s: unbalanced Markdown: %q", name, l)
		}
		if strings.ContainsAny(l, "[`\\") {
			t.Errorf("%s: Markdown special in %q", name, l)
		}
	}
	if inBlock {
		t.Errorf("%s: unclosed code block", name)
	}
	return max
}

func goldFixtures() map[string][]pricetracker.Price {
	row := func(name string, buy, sell float64, unit string) pricetracker.Price {
		return pricetracker.Price{Type: name, BuyPrice: buy, SellPrice: sell, Unit: unit, Currency: "VND"}
	}
	kk := kimKhoaFixture()
	kk = append(kk, row("KIM KHOA 999.9 NHẪN TRÒN TRƠN ÉP VĨ ĐẶC BIỆT", 11670, 11800, "nghìn/chỉ"))
	return map[string][]pricetracker.Price{
		pricetracker.KimKhoaSourceName: kk,
		"Bảo Tín Minh Châu": {
			row("Vàng miếng SJC (Bảo Tín Minh Châu) 999.9", 15350000, 15550000, "VND/chỉ"),
			row("Nhẫn tròn trơn BTMC 24K 9999 Rồng Thăng Long", 14950000, 15250000, "VND/chỉ"),
			row("Không liên quan", 1, 2, "VND/chỉ"),
		},
		"Doji": {row("SJC 1L", 15350, 15550, "nghìn/chỉ"), row("Vàng 9999", 1900, 2000, "nghìn/lượng")},
	}
}

func goldChanges() map[string][]pricetracker.PriceChange {
	return map[string][]pricetracker.PriceChange{
		"Bảo Tín Minh Châu": {
			{Price: pricetracker.Price{Type: "Vàng miếng SJC (Bảo Tín Minh Châu) 999.9"}, BuyChange: 1.2, SellChange: -0.8},
			{Price: pricetracker.Price{Type: "Nhẫn tròn trơn BTMC 24K 9999 Rồng Thăng Long"}, BuyChange: 2.5, SellChange: 2.5},
		},
		"Doji": {{Price: pricetracker.Price{Type: "SJC 1L"}, BuyChange: -1.1, SellChange: -1.1}},
	}
}

func cryptoChanges() map[string][]pricetracker.PriceChange {
	return map[string][]pricetracker.PriceChange{"CoinGecko": {
		{Price: pricetracker.Price{Type: "Bitcoin"}, BuyChange: 1.2},
		{Price: pricetracker.Price{Type: "Ethereum"}, BuyChange: -2.3},
	}}
}

func mergePrices(ms ...map[string][]pricetracker.Price) map[string][]pricetracker.Price {
	out := map[string][]pricetracker.Price{}
	for _, m := range ms {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}

func TestCompactReportWidthAndMarkdown(t *testing.T) {
	longCrypto := map[string][]pricetracker.Price{"CoinGecko": {
		{Type: "Bitcoin", Currency: "USD", BuyPrice: 123456, Unit: "BTC"},
		{Type: "BNB Smart Chain Wrapped Token", Currency: "USD", BuyPrice: 1234, Unit: "BNB"},
	}}
	extremes := []pricetracker.FearGreed{fgAt(50, "Neutral"), fgAt(20, "Extreme Fear"), fgAt(80, "Extreme Greed"), fgAt(26, "Fear")}
	unknown := fgAt(30, "")
	unknown.Change = -3
	flat := fgAt(60, "Greed")
	flat.Change = 0
	noPrev := fgAt(75, "Extreme Greed")
	noPrev.HasPrev = false
	extremes = append(extremes, unknown, flat, noPrev)

	var samples = map[string]string{}
	samples["gold"] = generatePriceNotificationVND(goldFixtures(), goldChanges(), nil, "", 0)
	samples["crypto+VND"] = generatePriceNotificationVND(cryptoPrices(), cryptoChanges(), nil, "", 26434)
	samples["crypto long names"] = generatePriceNotificationVND(longCrypto, nil, nil, "", 26434)
	samples["crypto no VND"] = generatePriceNotificationVND(cryptoPrices(), cryptoChanges(), nil, "", 0)
	for i, f := range extremes {
		fg := fearGreedLine(stubFG{f: f})
		samples["fg"+string(rune('0'+i))] = generatePriceNotificationVND(mergePrices(goldFixtures(), cryptoPrices()), goldChanges(), nil, fg, 26434)
	}
	for name, m := range samples {
		assertCompact(t, name, m)
	}

	if m := samples["crypto no VND"]; strings.Contains(m, "₫") || strings.Contains(m, "≈") || strings.Contains(m, "P2P") {
		t.Errorf("no-VND report leaked VND/footer:\n%s", m)
	}
	if m := samples["crypto+VND"]; strings.Count(m, "USDT/VND P2P") != 1 || !strings.Contains(m, "1,80tỷ") || !strings.Contains(m, "89,9tr") || !strings.Contains(m, "\n≈ ₫ theo USDT/VND P2P\n") {
		t.Errorf("VND report:\n%s", m)
	}
	for _, l := range strings.Split(samples["gold"], "\n") {
		if strings.Contains(l, "USDT") {
			t.Errorf("gold got VND footer: %q", l)
		}
	}
	g := samples["gold"]
	for _, want := range []string{"Doji · nghìn/chỉ", "Doji · nghìn/lượng", "Bảo Tín Minh Châu · VND/chỉ", "Kim Khoa · nghìn/chỉ", "Mua", "Bán", "%"} {
		if !strings.Contains(g, want) {
			t.Errorf("gold missing %q:\n%s", want, g)
		}
	}
	if strings.Contains(g, "Không liên quan") {
		t.Errorf("non-keyword row shown:\n%s", g)
	}
	// every Kim Khoa row survives (5 rows = 4 fixture rows + the long name)
	if strings.Contains(g, "tăng") {
		t.Errorf("legend leaked:\n%s", g)
	}
	if n := strings.Count(g, "KK "); n != 3 {
		t.Errorf("Kim Khoa rows: %d\n%s", n, g)
	}
	for _, bad := range []string{"(N items)", " items)", "📈", "📉", "💰", "💵", "•  "} {
		if strings.Contains(g, bad) {
			t.Errorf("old format leaked %q:\n%s", bad, g)
		}
	}

	fgLines := func(i int) string {
		m := samples["fg"+string(rune('0'+i))]
		a := strings.Index(m, "\n⚠️")
		if a < 0 {
			a = strings.Index(m, " F&G:")
			a = strings.LastIndex(m[:a], "\n")
		}
		return m[a+1 : strings.Index(m, "\n\n🤖")]
	}
	for i, want := range []string{
		"😐 F&G: 50 — Neutral\n   Trung lập\n   ▲2 so với hôm qua",
		"⚠️ *😱 F&G: 20 — Extreme Fear*\n   Sợ hãi tột độ\n   ▲2 so với hôm qua",
		"⚠️ *🤑 F&G: 80 — Extreme Greed*\n   Tham lam tột độ\n   ▲2 so với hôm qua",
		"😨 F&G: 26 — Fear\n   Sợ hãi\n   ▲2 so với hôm qua",
		"📉 F&G: 30\n   Không rõ\n   ▼3 so với hôm qua",
		"😀 F&G: 60 — Greed\n   Tham lam\n   không đổi so với hôm qua",
		"⚠️ *🤑 F&G: 75 — Extreme Greed*\n   Tham lam tột độ",
	} {
		if got := fgLines(i); got != want {
			t.Errorf("fg%d:\n%q\nwant\n%q", i, got, want)
		}
	}
	// emphasis only at the extremes
	if strings.Contains(samples["fg0"], "⚠️") || strings.Contains(samples["fg3"], "⚠️") || strings.Contains(samples["fg5"], "⚠️") {
		t.Error("non-extreme got emphasis")
	}
	// F&G stays out of gold-only reports
	if m := generatePriceNotificationVND(goldFixtures(), nil, nil, compactFearGreed(fgAt(20, "Extreme Fear")), 0); strings.Contains(m, "F&G") {
		t.Error("gold-only report got F&G")
	}
}

func TestCompactReportUSDOnlyMatchesNoRate(t *testing.T) {
	a := generatePriceNotificationVND(cryptoPrices(), nil, nil, "", 0)
	b := generatePriceNotificationFG(cryptoPrices(), nil, nil, "")
	if stripTime(a) != stripTime(b) {
		t.Fatal("rate 0 must equal the FG entry point")
	}
}

func TestFormatVNDCompact(t *testing.T) {
	for _, c := range []struct {
		v    float64
		want string
	}{
		{1, "≈ 1 ₫"}, {999, "≈ 999 ₫"}, {999.4, "≈ 999 ₫"}, {999.5, "≈ 1,00 nghìn ₫"},
		{1000, "≈ 1,00 nghìn ₫"}, {1234, "≈ 1,23 nghìn ₫"}, {9996, "≈ 10,0 nghìn ₫"}, {12345, "≈ 12,3 nghìn ₫"},
		{99950, "≈ 100 nghìn ₫"}, {123456, "≈ 123 nghìn ₫"}, {999499, "≈ 999 nghìn ₫"}, {999500, "≈ 1,00 tr ₫"},
		{1e6, "≈ 1,00 tr ₫"}, {91.3e6, "≈ 91,3 tr ₫"}, {89875600, "≈ 89,9 tr ₫"}, {99.95e6, "≈ 100 tr ₫"},
		{999.4e6, "≈ 999 tr ₫"}, {999.5e6, "≈ 1,00 tỷ ₫"}, {1e9, "≈ 1,00 tỷ ₫"}, {1.774e9, "≈ 1,77 tỷ ₫"},
		{1797512000, "≈ 1,80 tỷ ₫"}, {12.34e9, "≈ 12,3 tỷ ₫"}, {123.4e9, "≈ 123 tỷ ₫"}, {1234e9, "≈ 1234 tỷ ₫"},
	} {
		if got := pricetracker.FormatVNDCompact(c.v, 1); got != c.want {
			t.Errorf("%v: %q want %q", c.v, got, c.want)
		}
	}
	for _, v := range []float64{0, -5, 0.4} {
		if got := pricetracker.FormatVNDCompact(v, 1); got != "" {
			t.Errorf("%v: %q", v, got)
		}
	}
	if pricetracker.FormatVNDCompact(1, 0) != "" || pricetracker.FormatVNDCompact(1e308, 1e308) != "" {
		t.Error("guards")
	}
}

// The chat tool and /fng share FormatVND, Line and Text; they must not move.
func TestSharedChatHelpersUnchanged(t *testing.T) {
	if got, want := pricetracker.FormatVND(68000, 26434), "≈ 1.797.512.000 ₫ (USDT/VND P2P)"; got != want {
		t.Errorf("FormatVND %q", got)
	}
	f := fgAt(20, "Extreme Fear")
	if got, want := f.Line(), "⚠️ 😱 Fear & Greed: 20 — Sợ hãi tột độ (Extreme Fear) · ▲2 so với hôm qua"; got != want {
		t.Errorf("Line %q", got)
	}
	if got, want := f.Text(), "Chỉ số Fear & Greed (crypto): 20/100 — Sợ hãi tột độ (Extreme Fear), ngày 2026-11-26; thay đổi so với hôm qua: +2"; got != want {
		t.Errorf("Text %q", got)
	}
	if got, want := f.TelegramMarkdownLine(), "⚠️ *😱 Fear & Greed: 20 — Sợ hãi tột độ (Extreme Fear) · ▲2 so với hôm qua*"; got != want {
		t.Errorf("TelegramMarkdownLine %q", got)
	}
}

func TestSampleReports(t *testing.T) {
	all := mergePrices(goldFixtures(), cryptoPrices())
	ch := goldChanges()
	ch["CoinGecko"] = cryptoChanges()["CoinGecko"]
	mh := func(n string, b, sl float64) pricetracker.Price {
		return pricetracker.Price{Type: n, BuyPrice: b, SellPrice: sl, Currency: "VND"}
	}
	all["Bảo Tín Minh Châu"] = []pricetracker.Price{mh("VÀNG MIẾNG SJC (Vàng SJC)", 15350, 15550)}
	all["Mi Hồng"] = []pricetracker.Price{mh("VÀNG MIẾNG 9999", 15000, 15300)}
	fg := fgAt(20, "Extreme Fear")
	for name, m := range map[string]string{
		"WITH VND":    generatePriceNotificationVND(all, ch, nil, fearGreedLine(stubFG{f: fg}), 26434),
		"WITHOUT VND": generatePriceNotificationVND(all, ch, nil, fearGreedLine(stubFG{f: fg}), 0),
	} {
		t.Logf("=== AFTER %s (max width %d) ===\n%s", name, assertCompact(t, name, m), stripTime(m))
	}
}

func TestBTMCMiHongUnitFallback(t *testing.T) {
	mk := func(name string, b, sl float64) pricetracker.Price {
		return pricetracker.Price{Type: name, BuyPrice: b, SellPrice: sl, Currency: "VND"} // Unit empty, as the sources emit
	}
	m := generatePriceNotificationVND(map[string][]pricetracker.Price{
		"Bảo Tín Minh Châu": {mk("VÀNG MIẾNG SJC (Vàng SJC)", 15350, 15550), mk("NHẪN TRÒN TRƠN 9999", 15100, 15400)},
		"Mi Hồng":           {mk("VÀNG MIẾNG SJC", 15340, 15540), mk("VÀNG MIẾNG 9999", 15000, 15300)},
		"Other":             {mk("SJC 1L", 1, 2)},
	}, nil, nil, "", 0)
	if strings.Count(m, "Bảo Tín Minh Châu · nghìn") != 1 || strings.Count(m, "Mi Hồng · nghìn") != 1 {
		t.Errorf("want unit line for BTMC and Mi Hồng:\n%s", m)
	}
	if strings.Count(m, "Other · VND") != 1 {
		t.Errorf("unknown source must stay unit-less:\n%s", m)
	}
	assertCompact(t, "btmc/mihong", m)
	t.Logf("%s", stripTime(m))
}

func TestRowFilterMatchesOriginal(t *testing.T) {
	old := func(source string, p pricetracker.Price) bool { // original predicate, verbatim
		if source == pricetracker.KimKhoaSourceName {
			return true
		}
		for _, k := range []string{"9999", "tròn trơn", "sjc", "bitcoin", "ethereum", "bnb"} {
			if strings.Contains(strings.ToLower(p.Type), k) {
				return true
			}
		}
		return false
	}
	names := []string{"Bitcoin", "BNB", "Ethereum", "ETHEREUM CLASSIC", "Vàng 9999", "NHẪN TRÒN TRƠN", "SJC 1L", "Solana", "Nhẫn 999", "Cardano 9999", "", "bitcoin cash"}
	for _, src := range []string{"CoinGecko", "Doji", pricetracker.KimKhoaSourceName} {
		for _, n := range names {
			p := pricetracker.Price{Type: n, Currency: "VND", BuyPrice: 1, SellPrice: 2}
			if src == "CoinGecko" && showCrypto(p) != old("CoinGecko", p) {
				t.Errorf("crypto %q", n)
			}
		}
	}
	// rendered: a Bitcoin row in a gold source is now hidden; a 9999 row in CoinGecko stays
	m := generatePriceNotificationVND(map[string][]pricetracker.Price{
		"Doji":      {{Type: "Bitcoin", Currency: "VND", BuyPrice: 1, SellPrice: 2}},
		"CoinGecko": {{Type: "Token 9999", Currency: "USD", BuyPrice: 5}},
	}, nil, nil, "", 0)
	if strings.Contains(m, "Bitcoin ") || !strings.Contains(m, "Token 9999") {
		t.Errorf("rows lost:\n%s", m)
	}
}

func TestGuardWidthProperty(t *testing.T) {
	rnd := rand.New(rand.NewSource(7))
	pieces := []string{"Vàng", "miếng", "SJC", "9999", "NHẪN", "TRÒN", "TRƠN", "ĐẶC", "BIỆT", "🟠", "🔥", "👨‍👩‍👧", "🇻🇳", "👍🏽", "⚠️", "↔️", "1️⃣", "#️⃣", "©️", "ệ", "世界", "Supercalifragilisticexpialidocious-Wrapped-Token-Name", "BNB", "Bitcoin"}
	digits := func() float64 {
		n := 4 + rnd.Intn(3)
		v := float64(1 + rnd.Intn(9))
		for i := 1; i < n; i++ {
			v = v*10 + float64(rnd.Intn(10))
		}
		return v
	}
	for iter := 0; iter < 300; iter++ {
		name := ""
		for k := 1 + rnd.Intn(7); k > 0; k-- {
			name += pieces[rnd.Intn(len(pieces))] + " "
		}
		name += "9999 bitcoin sjc"
		chg := float64(rnd.Intn(19999)-9999) / 10
		p := pricetracker.Price{Type: name, Currency: "VND", Unit: []string{"", "nghìn/chỉ", "nghìn/lượng"}[rnd.Intn(3)], BuyPrice: digits(), SellPrice: digits()}
		c := pricetracker.Price{Type: name, Currency: "USD", BuyPrice: digits()}
		ch := func(pp pricetracker.Price) []pricetracker.PriceChange {
			return []pricetracker.PriceChange{{Price: pp, BuyChange: chg, SellChange: -chg}}
		}
		src := []string{"Doji", "Bảo Tín Minh Châu", "Mi Hồng", "Nguồn có tên rất dài để thử việc xuống dòng"}[rnd.Intn(4)]
		m := generatePriceNotificationVND(
			map[string][]pricetracker.Price{src: {p}, "CoinGecko": {c}},
			map[string][]pricetracker.PriceChange{src: ch(p), "CoinGecko": ch(c)},
			nil, compactFearGreed(fgAt([]int{10, 50, 90}[rnd.Intn(3)], []string{"Extreme Fear", "Neutral", "Extreme Greed"}[rnd.Intn(3)])), 26434+float64(rnd.Intn(3000)))
		assertCompact(t, "prop", m)
		if t.Failed() {
			t.Fatalf("iteration %d:\n%s", iter, m)
		}
	}
	for _, in := range []string{"   *Mua 1234567890123 +999.9% abc* tail", "*" + strings.Repeat("x", 80) + "*", strings.Repeat("🟠", 25), "• " + strings.Repeat("👨‍👩‍👧", 20)} {
		assertCompact(t, "guard", guardWidth(in))
	}
}

func TestDisplayWidthTable(t *testing.T) {
	for in, want := range map[string]int{
		"↔️": 2, "↔": 1, "1️⃣": 2, "#️⃣": 2, "©️": 2, "©": 1, "🇻🇳": 2, "👨‍👩‍👧": 2, "ệ": 1, "ệ": 1,
		"*x*": 1, strings.Repeat("↔️", 20): 40, "a👍🏽b": 4,
	} {
		if got := displayWidth(in); got != want {
			t.Errorf("displayWidth(%q)=%d want %d", in, got, want)
		}
		if got := visibleWidth(in); got != want {
			t.Errorf("visibleWidth(%q)=%d want %d", in, got, want)
		}
	}
	kk := map[string][]pricetracker.Price{pricetracker.KimKhoaSourceName: {{Type: strings.Repeat("↔️", 20) + " 1️⃣ #️⃣ ©️", Currency: "VND", Unit: "nghìn/chỉ", BuyPrice: 1, SellPrice: 2}}}
	assertCompact(t, "keycap", generatePriceNotificationVND(kk, nil, nil, "", 0))
}
