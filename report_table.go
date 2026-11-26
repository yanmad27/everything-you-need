package main

import (
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
