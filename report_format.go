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

// wrapWords greedily wraps s into lines of at most width; an over-long word is split by rune.
func wrapWords(s string, width int) []string {
	var lines []string
	cur := ""
	flush := func() {
		if cur != "" {
			lines = append(lines, cur)
			cur = ""
		}
	}
	for _, word := range strings.Fields(s) {
		for displayWidth(word) > width {
			flush()
			cut := 0
			for _, c := range clusters(word) {
				if displayWidth(word[:cut+len(c)]) > width {
					break
				}
				cut += len(c)
			}
			if cut == 0 {
				cut = len(clusters(word)[0])
			}
			lines = append(lines, word[:cut])
			word = word[cut:]
		}
		switch {
		case cur == "":
			cur = word
		case displayWidth(cur)+1+displayWidth(word) <= width:
			cur += " " + word
		default:
			flush()
			cur = word
		}
	}
	flush()
	return lines
}

// bullet renders name as "• name", wrapping long names under the text.
func bullet(name string) []string {
	ls := wrapWords(name, maxReportWidth-2)
	if len(ls) == 0 {
		ls = []string{"-"}
	}
	out := make([]string, len(ls))
	for i, l := range ls {
		if i == 0 {
			out[i] = "• " + l
		} else {
			out[i] = nameIndent + l
		}
	}
	return out
}

func sectionHeader(name string, n int) []string {
	count := fmt.Sprintf("(%d items)", n)
	ls := wrapWords(name, maxReportWidth)
	if len(ls) == 0 {
		ls = []string{"-"}
	}
	last := len(ls) - 1
	out := make([]string, 0, len(ls)+1)
	for i, l := range ls {
		if i == last && displayWidth(l)+1+displayWidth(count) <= maxReportWidth {
			out = append(out, "*"+l+"* "+count)
			return out
		}
		out = append(out, "*"+l+"*")
	}
	return append(out, count)
}

func changeText(v float64) string {
	switch {
	case v > 0:
		return fmt.Sprintf(" 📈+%.1f%%", v)
	case v < 0:
		return fmt.Sprintf(" 📉%.1f%%", v)
	}
	return ""
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

func unitLine(currency, unit string) string {
	s := currency
	if f := currencyMap[currency]; f != "" {
		s = f + " " + currency
	}
	if unit != "" {
		s += " · " + unit
	}
	return s
}

func goldRowLines(p pricetracker.Price, c pricetracker.PriceChange) []string {
	lines := bullet(reportName(p.Type))
	buy := "*" + formatPrice(p.BuyPrice) + "*"
	sell := "*" + formatPrice(p.SellPrice) + "*"
	sellChg := ""
	if c.SellChange != 0 && c.SellChange != c.BuyChange {
		sellChg = changeText(c.SellChange)
	}
	single := detailIndent + "Mua " + buy + changeText(c.BuyChange) + " / Bán " + sell + sellChg
	if displayWidth(single) <= maxReportWidth {
		return append(lines, single)
	}
	return append(lines,
		detailIndent+"Mua "+buy+changeText(c.BuyChange),
		detailIndent+"Bán "+sell+changeText(c.SellChange))
}

func cryptoRowLines(p pricetracker.Price, c pricetracker.PriceChange, curSuffix string) []string {
	price := "*" + formatPrice(p.BuyPrice) + "*" + curSuffix + changeText(c.BuyChange)
	name := reportName(p.Type)
	if single := "• " + name + " " + price; !strings.Contains(name, "  ") && displayWidth(single) <= maxReportWidth {
		return []string{single}
	}
	return append(bullet(name), detailIndent+price)
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

func goldSection(b *strings.Builder, source string, prices []pricetracker.Price, changes map[string][]pricetracker.PriceChange) {
	if len(prices) == 0 {
		fmt.Fprintf(b, "*%s*\n• N/A\n\n", source)
		return
	}
	b.WriteString(strings.Join(sectionHeader(source, len(prices)), "\n") + "\n")
	idx := changeIndex(changes, source)
	prev := ""
	for _, p := range prices {
		if !showGold(source, p) {
			continue
		}
		if u := unitLine(p.Currency, goldUnit(source, p)); u != prev {
			b.WriteString(u + "\n")
			prev = u
		}
		b.WriteString(strings.Join(goldRowLines(p, idx[p.Type]), "\n") + "\n")
	}
	b.WriteString("\n")
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

func cryptoSection(b *strings.Builder, source string, prices []pricetracker.Price, changes map[string][]pricetracker.PriceChange, vndRate float64) {
	if len(prices) == 0 {
		fmt.Fprintf(b, "*%s*\n• N/A\n\n", source)
		return
	}
	hdr := sectionHeader(source, len(prices))
	common := prices[0].Currency
	for _, p := range prices {
		if p.Currency != common {
			common = ""
		}
	}
	if common != "" {
		hdr = append(hdr, unitLine(common, ""))
	}
	b.WriteString(strings.Join(hdr, "\n") + "\n")
	idx := changeIndex(changes, source)
	vndShown := false
	for _, p := range prices {
		if !showCrypto(p) {
			continue
		}
		suffix := ""
		if common == "" {
			suffix = " " + p.Currency
		}
		b.WriteString(strings.Join(cryptoRowLines(p, idx[p.Type], suffix), "\n") + "\n")
		if vndRate > 0 && p.Currency == "USD" {
			if v := pricetracker.FormatVNDCompact(p.BuyPrice, vndRate); v != "" {
				b.WriteString(detailIndent + v + "\n")
				vndShown = true
			}
		}
	}
	if vndShown {
		b.WriteString(vndFooter + "\n")
	}
	b.WriteString("\n")
}
