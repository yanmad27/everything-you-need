package main

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

// tableBudget is the widest table row (runes, monospace) that fits a Telegram
// iOS code block on a 375-pt iPhone: see docs in the PR/CANDIDATE derivation
// (~29-31 columns; 28 keeps a safety margin).
const tableBudget = 28

const changeLegend = "+ tăng, - giảm so với trước"

type tableCol struct {
	header string
	cells  []string
}

func runeLen(s string) int { return utf8.RuneCountInString(s) }

func padRight(s string, w int) string {
	if n := runeLen(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

func padLeft(s string, w int) string {
	if n := runeLen(s); n < w {
		return strings.Repeat(" ", w-n) + s
	}
	return s
}

// nameBudget is how many runes the name column may take next to cols.
func nameBudget(cols []tableCol) int {
	w := tableBudget
	for _, c := range cols {
		w -= 1 + colWidth(c)
	}
	return w
}

func colWidth(c tableCol) int {
	w := runeLen(c.header)
	for _, s := range c.cells {
		if n := runeLen(s); n > w {
			w = n
		}
	}
	return w
}

// The opening fence is immediately followed by a newline: the legacy parser looks
// for a language token right after ``` before it consumes that newline (tdlib
// MessageEntity.cpp), so the title on the next line is never read as a language.
// TestFenceFollowedByNewline pins this.
//
// renderTable renders one Telegram legacy-Markdown pre block: title, header row
// and one row per name, every line padded to the same rune width. Numeric
// columns are right-aligned.
func renderTable(title string, names []string, cols []tableCol) string {
	title = truncRunes(cleanName(title), tableBudget)
	nameW := 0
	for _, n := range names {
		if l := runeLen(n); l > nameW {
			nameW = l
		}
	}
	rows := make([]string, 0, len(names)+2)
	hdr := strings.Repeat(" ", nameW)
	for _, c := range cols {
		hdr += " " + padLeft(c.header, colWidth(c))
	}
	for i, n := range names {
		row := padRight(n, nameW)
		for _, c := range cols {
			row += " " + padLeft(c.cells[i], colWidth(c))
		}
		rows = append(rows, row)
	}
	width := runeLen(title)
	if l := runeLen(hdr); l > width {
		width = l
	}
	lines := append([]string{title, hdr}, rows...)
	for i, l := range lines {
		lines[i] = padRight(l, width)
	}
	return "```\n" + strings.Join(lines, "\n") + "\n```\n"
}

// minNameWidth is the narrowest name column a table may degrade to.
const minNameWidth = 3

// pendingTable is one table whose columns were chosen to fit tableBudget; its
// names are assigned later so they can be unique across the whole source section.
type pendingTable struct {
	title   string
	raws    []string
	cols    []tableCol
	nameMax int
	note    string
}

// fitTable picks the first degradation level whose columns leave the name
// column at least minNameWidth runes (the last level always does):
// build(level) returns the columns for that level, noteAt the note for it.
func fitTable(title string, raws []string, levels int, build func(level int) []tableCol, noteAt func(level int) string) pendingTable {
	for lv := 0; ; lv++ {
		cols := build(lv)
		if nb := nameBudget(cols); nb >= minNameWidth || lv >= levels-1 {
			if nb < 1 {
				nb = 1
			}
			return pendingTable{title: title, raws: raws, cols: cols, nameMax: nb, note: noteAt(lv)}
		}
	}
}

// renderPending names every row (unique across all tables of the section) and
// renders each table, followed by its note and sep.
func renderPending(ts []pendingTable, sep string) []string {
	var raws []string
	var maxes []int
	for _, t := range ts {
		for _, r := range t.raws {
			raws, maxes = append(raws, r), append(maxes, t.nameMax)
		}
	}
	names := uniqueNamesEach(raws, maxes)
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		s := renderTable(t.title, names[:len(t.raws)], t.cols)
		names = names[len(t.raws):]
		if t.note != "" {
			s += t.note + "\n"
		}
		out = append(out, s+sep)
	}
	return out
}

// compactNumber is a ≤7-rune rendering of any float: whole numbers below 1000,
// then k / tr / tỷ with 3 significant digits and a decimal comma, ">999tỷ" beyond.
func compactNumber(v float64) string {
	if math.IsNaN(v) {
		return "-"
	}
	sign, a := "", math.Abs(v)
	if v < 0 {
		sign = "-"
	}
	if a >= 1e12 {
		if v < 0 {
			return "<-999tỷ"
		}
		return ">999tỷ"
	}
	if a < 1e3 {
		return sign + strconv.FormatFloat(math.Round(a), 'f', 0, 64)
	}
	units := []struct {
		div  float64
		name string
	}{{1e3, "k"}, {1e6, "tr"}, {1e9, "tỷ"}}
	i := 0
	for i < len(units)-1 && a >= units[i+1].div {
		i++
	}
	for {
		x := a / units[i].div
		d := 2
		if x >= 100 {
			d = 0
		} else if x >= 10 {
			d = 1
		}
		r := math.Round(x*math.Pow10(d)) / math.Pow10(d)
		if r >= 1000 && i < len(units)-1 {
			i++
			continue
		}
		return sign + strings.ReplaceAll(strconv.FormatFloat(r, 'f', d, 64), ".", ",") + units[i].name
	}
}
