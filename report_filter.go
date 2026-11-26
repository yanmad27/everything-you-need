package main

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

var goldExcludePhrases = []string{"nl", "nguyen lieu", "nu trang", "trang suc", "bac"}

var goldIncludeTokens = map[string]bool{
	"sjc": true, "9999": true, "999.9": true, "99.99": true, "99": true, "98": true, "97": true,
	"985": true, "980": true, "975": true, "970": true,
}

// goldTokens NFC-normalizes, lowercases, strips diacritics (đ→d) and splits on
// anything that is not a letter or digit; a '.' between two digits stays part
// of the token so "999.9" survives while "99%" yields "99".
func goldTokens(s string) []string {
	var b strings.Builder
	for _, r := range norm.NFD.String(norm.NFC.String(strings.ToLower(s))) {
		switch {
		case unicode.Is(unicode.Mn, r):
		case r == 'đ':
			b.WriteRune('d')
		default:
			b.WriteRune(r)
		}
	}
	rs := []rune(b.String())
	var toks []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			toks = append(toks, string(cur))
			cur = cur[:0]
		}
	}
	for i, r := range rs {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			cur = append(cur, r)
		case r == '.' && i > 0 && i+1 < len(rs) && unicode.IsDigit(rs[i-1]) && unicode.IsDigit(rs[i+1]):
			cur = append(cur, r)
		default:
			flush()
		}
	}
	flush()
	return toks
}

// goldRowWanted is the Telegram-report gold row filter: exclusions (raw
// material, jewelry, silver) first, then SJC or a 97–99.99 purity token.
func goldRowWanted(name string) bool {
	toks := goldTokens(name)
	joined := " " + strings.Join(toks, " ") + " "
	for _, ex := range goldExcludePhrases {
		if strings.Contains(joined, " "+ex+" ") {
			return false
		}
	}
	for _, t := range toks {
		if goldIncludeTokens[t] {
			return true
		}
	}
	return false
}
