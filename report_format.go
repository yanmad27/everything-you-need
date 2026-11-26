package main

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	pricetracker "everything-you-need/m/services/price-tracker"
)

// maxReportWidth is the widest visible line (emoji = 2) that fits an iPhone in portrait.
const maxReportWidth = 30

const (
	detailIndent = "   "
	nameIndent   = "  "
	vndFooter    = "≈ ₫ theo " + pricetracker.VNDLabel
)

// displayWidth is the on-screen width of s: Markdown markers are ignored, emoji,
// flags and East Asian wide runes count 2, combining marks 0, everything else 1.
func displayWidth(s string) int {
	rs := []rune(s)
	w := 0
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case r == '*' || r == '_' || r == 0xFE0F || unicode.Is(unicode.Mn, r):
		case r >= 0x1F1E6 && r <= 0x1F1FF:
			w += 2
			if i+1 < len(rs) && rs[i+1] >= 0x1F1E6 && rs[i+1] <= 0x1F1FF {
				i++
			}
		case i+1 < len(rs) && rs[i+1] == 0xFE0F, isWide(r):
			w += 2
		default:
			w++
		}
	}
	return w
}

func isWide(r rune) bool {
	switch {
	case r >= 0x1100 && r <= 0x115F, r >= 0x2E80 && r <= 0xA4CF, r >= 0xAC00 && r <= 0xD7A3,
		r >= 0xF900 && r <= 0xFAFF, r >= 0xFE30 && r <= 0xFE6F, r >= 0xFF00 && r <= 0xFF60,
		r >= 0xFFE0 && r <= 0xFFE6, r >= 0x1F300 && r <= 0x1F64F, r >= 0x1F680 && r <= 0x1F6FF,
		r >= 0x1F900 && r <= 0x1F9FF, r >= 0x1FA70 && r <= 0x1FAFF, r >= 0x20000 && r <= 0x3FFFD:
		return true
	}
	return false
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
			for i, r := range word {
				if displayWidth(word[:i+utf8.RuneLen(r)]) > width {
					break
				}
				cut = i + utf8.RuneLen(r)
			}
			if cut == 0 {
				break
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
		if u := unitLine(p.Currency, p.Unit); u != prev {
			b.WriteString(u + "\n")
			prev = u
		}
		b.WriteString(strings.Join(goldRowLines(p, idx[p.Type]), "\n") + "\n")
	}
	b.WriteString("\n")
}

func showGold(source string, p pricetracker.Price) bool {
	if source == pricetracker.KimKhoaSourceName {
		return true
	}
	t := strings.ToLower(p.Type)
	for _, k := range []string{"9999", "tròn trơn", "sjc"} {
		if strings.Contains(t, k) {
			return true
		}
	}
	return false
}

func showCrypto(p pricetracker.Price) bool {
	t := strings.ToLower(p.Type)
	for _, k := range []string{"bitcoin", "ethereum", "bnb"} {
		if strings.Contains(t, k) {
			return true
		}
	}
	return false
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
