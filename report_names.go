package main

import (
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// reportNameAbbrev maps the product names the sources emit (upper-case, NFC,
// parentheses removed) to short table labels.
var reportNameAbbrev = map[string]string{
	"VÀNG MIẾNG SJC":                               "SJC miếng",
	"VÀNG MIẾNG SJC 999.9":                         "SJC 999.9",
	"KIM TT/AVPL":                                  "Kim TT/AVPL",
	"NHẪN TRÒN 9999 HƯNG THỊNH VƯỢNG":              "Nhẫn 9999",
	"NHẪN TRÒN TRƠN BTMC 24K 9999 RỒNG THĂNG LONG": "BTMC 9999",
	"KIM KHOA 999.9 ÉP VĨ":                         "KK 999.9 vĩ",
	"KIM KHOA 999.9":                               "KK 999.9",
}

// reportNamePrefixes shorten the common product-family prefixes; the rest of the
// name is kept.
var reportNamePrefixes = [][2]string{
	{"VÀNG MIẾNG ", "Miếng "},
	{"VÀNG NGUYÊN LIỆU ", "Vàng NL "},
	{"NỮ TRANG ", "Trang "},
	{"KIM KHOA ", "KK "},
	{"NHẪN ", "Nhẫn "},
}

var reportAcronyms = map[string]bool{"SJC": true, "AVPL": true, "TT": true, "NL": true, "KK": true, "BTMC": true, "DOJI": true, "PNJ": true, "BNB": true, "BTC": true, "ETH": true}

// cleanName is the deterministic table-safe form of a product name: parentheses
// removed, NFC, Markdown/pre-special characters, controls, emoji, wide and
// leftover combining runes dropped, whitespace collapsed.
func cleanName(s string) string {
	s = norm.NFC.String(reportName(s))
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '`' || r == '\\' || r == '*' || r == '_' || r == '[' || r == ']':
			return -1
		case unicode.IsSpace(r):
			return ' '
		case unicode.IsControl(r) || isCombining(r) || isJoiner(r) || isRegional(r) || isWide(r) || r == 0xFFFD:
			return -1
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

// sentenceCase lowers an all-upper-case name (keeping known acronyms and
// digit-bearing tokens) and capitalises its first letter; mixed-case input is kept.
func sentenceCase(s string) string {
	if s != strings.ToUpper(s) {
		return s
	}
	words := strings.Fields(s)
	for i, w := range words {
		if !reportAcronyms[w] && !strings.ContainsAny(w, "0123456789") {
			words[i] = strings.ToLower(w)
		}
	}
	out := strings.Join(words, " ")
	r := []rune(out)
	if len(r) > 0 {
		r[0] = unicode.ToUpper(r[0])
	}
	return string(r)
}

func truncRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if r := []rune(s); len(r) > n {
		return strings.TrimSpace(string(r[:n]))
	}
	return s
}

// abbreviate returns the curated label, else prefix-rule + sentence case, cut to max runes.
func abbreviate(raw string, max int) string {
	c := cleanName(raw)
	key := norm.NFC.String(strings.ToUpper(c))
	if v, ok := reportNameAbbrev[key]; ok {
		return truncRunes(v, max)
	}
	for _, p := range reportNamePrefixes {
		if strings.HasPrefix(key, p[0]) && len(key) > len(p[0]) {
			return truncRunes(p[1]+sentenceCase(key[len(p[0]):]), max)
		}
	}
	if c == "" {
		return "-"
	}
	return truncRunes(sentenceCase(c), max)
}

// uniqueNames abbreviates every raw name to at most max runes and makes the
// results unique by suffixing 2, 3, … (cutting the base to make room).
func uniqueNames(raws []string, max int) []string {
	maxes := make([]int, len(raws))
	for i := range maxes {
		maxes[i] = max
	}
	return uniqueNamesEach(raws, maxes)
}

// uniqueNamesEach is uniqueNames with a per-name width, so one set of names can
// span several tables (one source section) yet stay distinct. A suffix that is
// wider than the width keeps only its last digits; labels stay distinct while
// fewer than 10^width names collide.
func uniqueNamesEach(raws []string, maxes []int) []string {
	out := make([]string, len(raws))
	used := map[string]bool{}
	for i, raw := range raws {
		max := maxes[i]
		if max < 1 {
			max = 1
		}
		base := abbreviate(raw, max)
		name := base
		for k := 2; used[name] && k < 1<<20; k++ {
			suf := strconv.Itoa(k)
			if len(suf) > max {
				suf = suf[len(suf)-max:]
			}
			name = truncRunes(base, max-len(suf)) + suf
		}
		used[name] = true
		out[i] = name
	}
	return out
}
