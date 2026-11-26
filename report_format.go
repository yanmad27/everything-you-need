package main

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	pricetracker "everything-you-need/m/services/price-tracker"
)

// maxReportWidth is the widest visible line (emoji = 2) that fits an iPhone in portrait.
const maxReportWidth = 30

const (
	detailIndent = "   "
	nameIndent   = "  "
	vndFooter    = "≈ ₫ theo " + pricetracker.VNDLabel
)

func isJoiner(r rune) bool { return r == 0x200D }

func isRegional(r rune) bool { return r >= 0x1F1E6 && r <= 0x1F1FF }

// isCombining is zero-width and glues to the previous rune: VS16, keycap,
// skin-tone modifiers and combining marks.
func isCombining(r rune) bool {
	return r == 0xFE0F || r == 0x20E3 || (r >= 0x1F3FB && r <= 0x1F3FF) || unicode.Is(unicode.Mn, r)
}

// displayWidth is the on-screen width of s: Markdown markers are ignored; a display
// unit (see clusters) counts 2 when it holds VS16 or a keycap, is a flag, or its
// base is an emoji / East Asian wide rune, otherwise 1.
func displayWidth(s string) int {
	w := 0
	for _, c := range clusters(strings.NewReplacer("*", "", "_", "").Replace(s)) {
		w += clusterWidth(c)
	}
	return w
}

func clusterWidth(c string) int {
	if strings.ContainsRune(c, 0xFE0F) || strings.ContainsRune(c, 0x20E3) {
		return 2
	}
	r := []rune(c)[0]
	if isRegional(r) || isWide(r) {
		return 2
	}
	return 1
}

// clusters splits s into display units that must not be broken apart: a base
// rune with its combining runes, ZWJ-joined sequences and flag pairs.
func clusters(s string) []string {
	var out []string
	joined, prevRegional := false, false
	for _, r := range s {
		if len(out) > 0 && (isCombining(r) || isJoiner(r) || joined || (isRegional(r) && prevRegional)) {
			out[len(out)-1] += string(r)
			joined = isJoiner(r)
			prevRegional = false
			continue
		}
		out = append(out, string(r))
		joined = false
		prevRegional = isRegional(r)
	}
	return out
}

func isWide(r rune) bool {
	switch {
	case r >= 0x1100 && r <= 0x115F, r >= 0x2E80 && r <= 0xA4CF, r >= 0xAC00 && r <= 0xD7A3,
		r >= 0xF900 && r <= 0xFAFF, r >= 0xFE30 && r <= 0xFE6F, r >= 0xFF00 && r <= 0xFF60,
		r >= 0xFFE0 && r <= 0xFFE6, r >= 0x1F000 && r <= 0x1FAFF, r >= 0x2600 && r <= 0x27BF,
		r >= 0x2B00 && r <= 0x2BFF, r >= 0x20000 && r <= 0x3FFFD:
		return true
	}
	return false
}

// guardWidth is the final guarantee: any line still wider than maxReportWidth is
// broken at spaces (or between display clusters for one huge token), keeps its
// indent, and closes/reopens bold across the break so every line stays valid.
func guardWidth(msg string) string {
	var out []string
	for _, line := range strings.Split(msg, "\n") {
		if displayWidth(line) <= maxReportWidth {
			out = append(out, line)
			continue
		}
		indent := line[:len(line)-len(strings.TrimLeft(line, " "))]
		if strings.HasPrefix(line, "• ") {
			indent = nameIndent
		}
		bold := false
		cur, lastSpace, spaceW, w := "", -1, 0, 0
		boldAtSpace := false
		flush := func(upTo int, rest string, open bool) {
			head := cur[:upTo]
			if open {
				head += "*"
			}
			out = append(out, strings.TrimRight(head, " "))
			cur = indent
			if open {
				cur += "*"
			}
			cur += strings.TrimLeft(rest, " ")
			lastSpace = -1
			w = displayWidth(cur)
		}
		for _, c := range clusters(line) {
			if c == "*" {
				bold = !bold
				cur += c
				continue
			}
			cw := clusterWidth(c)
			if w+cw > maxReportWidth {
				if lastSpace >= 0 && spaceW > displayWidth(indent) {
					flush(lastSpace, cur[lastSpace:], boldAtSpace)
				} else {
					flush(len(cur), "", bold)
				}
			}
			if c == " " {
				lastSpace, spaceW, boldAtSpace = len(cur), w, bold
			}
			cur += c
			w += cw
		}
		out = append(out, cur)
	}
	return strings.Join(out, "\n")
}

func sortedKeys(m map[string][]pricetracker.Price) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func changeIndex(changes map[string][]pricetracker.PriceChange, source string) map[string]pricetracker.PriceChange {
	idx := make(map[string]pricetracker.PriceChange)
	for _, c := range changes[source] {
		idx[c.Price.Type] = c
	}
	return idx
}

func reportName(typeStr string) string {
	return strings.Join(strings.Fields(formatType(typeStr)), " ")
}

// compactFearGreed is the Telegram (legacy Markdown) Fear & Greed block. Line 1
// carries the emoji, value and English label (bold with ⚠️ at the extremes);
// indented lines carry the Vietnamese label and the day-over-day delta.
func compactFearGreed(f pricetracker.FearGreed) string {
	emoji := map[string]string{"Extreme Fear": "😱", "Fear": "😨", "Neutral": "😐", "Greed": "😀", "Extreme Greed": "🤑"}[f.Classification]
	if emoji == "" {
		emoji = "📉"
	}
	head := fmt.Sprintf("%s F&G: %d", emoji, f.Value)
	vn := pricetracker.UnknownLabel
	if f.Classification != "" && f.ClassificationVN() != "" {
		head += " — " + f.Classification
		vn = f.ClassificationVN()
	}
	if f.Extreme() {
		head = pricetracker.WarnEmoji + " *" + head + "*"
	}
	lines := []string{head, detailIndent + vn}
	if f.HasPrev {
		switch {
		case f.Change > 0:
			lines = append(lines, fmt.Sprintf("%s▲%d so với hôm qua", detailIndent, f.Change))
		case f.Change < 0:
			lines = append(lines, fmt.Sprintf("%s▼%d so với hôm qua", detailIndent, -f.Change))
		default:
			lines = append(lines, detailIndent+"không đổi so với hôm qua")
		}
	}
	return strings.Join(lines, "\n")
}

// tableSourceAlias shortens source names that would crowd out the unit in a title.
var tableSourceAlias = map[string]string{pricetracker.KimKhoaSourceName: "Kim Khoa"}

// tableTitle is "Source · unit"; it always holds a space so Telegram never reads
// the first pre line as a code-language tag.
func tableTitle(source, unit, currency string) string {
	if unit == "" {
		unit = currency
	}
	suffix := " · " + cleanName(unit)
	if a, ok := tableSourceAlias[source]; ok {
		source = a
	}
	return truncRunes(cleanName(source), tableBudget-runeLen(suffix)) + suffix
}

func changeMark(v float64) string {
	switch {
	case v > 0:
		return "+"
	case v < 0:
		return "-"
	}
	return " "
}

// goldSection renders each (unit, currency) run of shown rows as one table; it
// reports whether any change mark was printed.
func goldSection(b *strings.Builder, source string, prices []pricetracker.Price, changes map[string][]pricetracker.PriceChange) bool {
	if len(prices) == 0 {
		fmt.Fprintf(b, "*%s*\n• N/A\n\n", source)
		return false
	}
	idx := changeIndex(changes, source)
	marks := false
	flush := func(title string, group []pricetracker.Price) {
		if len(group) == 0 {
			return
		}
		buy, sell := tableCol{header: "Mua"}, tableCol{header: "Bán"}
		raws := make([]string, len(group))
		for i, p := range group {
			c := idx[p.Type]
			bm, sm := changeMark(c.BuyChange), changeMark(c.SellChange)
			marks = marks || bm != " " || sm != " "
			raws[i] = p.Type
			buy.cells = append(buy.cells, formatPrice(p.BuyPrice)+bm)
			sell.cells = append(sell.cells, formatPrice(p.SellPrice)+sm)
		}
		cols := []tableCol{buy, sell}
		b.WriteString(renderTable(title, uniqueNames(raws, nameBudget(cols)), cols) + "\n")
	}
	var group []pricetracker.Price
	prev := ""
	for _, p := range prices {
		if !showGold(source, p) {
			continue
		}
		t := tableTitle(source, goldUnit(source, p), p.Currency)
		if t != prev {
			flush(prev, group)
			group, prev = nil, t
		}
		group = append(group, p)
	}
	flush(prev, group)
	return marks
}

var reportKeywords = []string{"9999", "tròn trơn", "sjc", "bitcoin", "ethereum", "bnb"}

// matchesKeyword is the original shared row filter for gold and crypto sections.
func matchesKeyword(p pricetracker.Price) bool {
	t := strings.ToLower(p.Type)
	for _, k := range reportKeywords {
		if strings.Contains(t, k) {
			return true
		}
	}
	return false
}

// showGold: Kim Khoa rows are already filtered by purity at the source.
func showGold(source string, p pricetracker.Price) bool {
	return source == pricetracker.KimKhoaSourceName || matchesKeyword(p)
}

func showCrypto(p pricetracker.Price) bool { return matchesKeyword(p) }

// goldUnit is the row's unit. BTMC and Mi Hồng leave Unit empty but divide the
// upstream prices by 1000 ("thousands VND", btmc.source.go:86, mihong.source.go:151);
// the code does not state a weight unit for them, so only "nghìn" is certain.
func goldUnit(source string, p pricetracker.Price) string {
	if p.Unit == "" && p.Currency == "VND" && (source == "Bảo Tín Minh Châu" || source == "Mi Hồng") {
		return "nghìn"
	}
	return p.Unit
}

func signedPercent(v float64) string {
	s := fmt.Sprintf("%+.1f%%", v)
	if s == "+0.0%" || s == "-0.0%" {
		return "0.0%"
	}
	return s
}

// compactVND is "≈ 1,77 tỷ ₫" without the symbols and spaces: "1,77tỷ".
func compactVND(usd, rate float64) string {
	v := pricetracker.FormatVNDCompact(usd, rate)
	v = strings.TrimSuffix(strings.TrimPrefix(v, "≈ "), " ₫")
	return strings.ReplaceAll(v, " ", "")
}

func cryptoTicker(p pricetracker.Price) string {
	u := cleanName(p.Unit)
	if u != "" && !strings.Contains(u, " ") && runeLen(u) <= 6 {
		return strings.ToUpper(u)
	}
	return p.Type
}

func cryptoSection(b *strings.Builder, source string, prices []pricetracker.Price, changes map[string][]pricetracker.PriceChange, vndRate float64) {
	if len(prices) == 0 {
		fmt.Fprintf(b, "*%s*\n• N/A\n\n", source)
		return
	}
	idx := changeIndex(changes, source)
	vndShown := false
	flush := func(cur string, group []pricetracker.Price) {
		if len(group) == 0 {
			return
		}
		price, chg, vnd := tableCol{header: "price"}, tableCol{header: "change"}, tableCol{header: "VND"}
		raws := make([]string, len(group))
		any := false
		for i, p := range group {
			raws[i] = cryptoTicker(p)
			price.cells = append(price.cells, formatPrice(p.BuyPrice))
			c := ""
			if v := idx[p.Type].BuyChange; v != 0 {
				c = signedPercent(v)
			}
			chg.cells = append(chg.cells, c)
			v := ""
			if vndRate > 0 && cur == "USD" {
				v = compactVND(p.BuyPrice, vndRate)
			}
			any = any || v != ""
			vnd.cells = append(vnd.cells, v)
		}
		cols := []tableCol{price, chg}
		if any {
			cols = append(cols, vnd)
			vndShown = true
		}
		b.WriteString(renderTable(tableTitle(source, "", cur), uniqueNames(raws, nameBudget(cols)), cols))
	}
	var group []pricetracker.Price
	cur := ""
	for _, p := range prices {
		if !showCrypto(p) {
			continue
		}
		if p.Currency != cur {
			flush(cur, group)
			group, cur = nil, p.Currency
		}
		group = append(group, p)
	}
	flush(cur, group)
	if vndShown {
		b.WriteString(vndFooter + "\n")
	}
	b.WriteString("\n")
}
