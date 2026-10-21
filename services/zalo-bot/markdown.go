package zalobot

import (
	"context"
	"errors"
	"regexp"
	"strings"
)

var urlRe = regexp.MustCompile(`https?://\S+`)

const mdSpecial = "_*~\\"

// escapeURLs backslash-escapes markdown-significant characters inside bare
// http(s) URLs so Zalo's markdown (which has no links) does not mangle them.
func escapeURLs(text string) string {
	return urlRe.ReplaceAllStringFunc(text, func(u string) string {
		var sb strings.Builder
		for _, r := range u {
			if strings.ContainsRune(mdSpecial, r) {
				sb.WriteByte('\\')
			}
			sb.WriteRune(r)
		}
		return sb.String()
	})
}

// unescapeURLs reverses escapeURLs (used for the plain-text fallback).
func unescapeURLs(text string) string {
	return urlRe.ReplaceAllStringFunc(text, func(u string) string {
		var sb strings.Builder
		rs := []rune(u)
		for i := 0; i < len(rs); i++ {
			if rs[i] == '\\' && i+1 < len(rs) && strings.ContainsRune(mdSpecial, rs[i+1]) {
				i++
			}
			sb.WriteRune(rs[i])
		}
		return sb.String()
	})
}

// openPairs scans rs and reports whether a ** or __ pair is left open.
func openPairs(rs []rune) (star, under bool) {
	for i := 0; i < len(rs); i++ {
		switch {
		case rs[i] == '\\':
			i++
		case rs[i] == '*' && i+1 < len(rs) && rs[i+1] == '*':
			star = !star
			i++
		case rs[i] == '_' && i+1 < len(rs) && rs[i+1] == '_':
			under = !under
			i++
		}
	}
	return
}

// validCut reports whether cutting before rs[cut] leaves escapes and ** / __
// markers intact.
func validCut(rs []rune, cut int) bool {
	if cut <= 0 || cut >= len(rs) {
		return true
	}
	n := 0
	for i := cut - 1; i >= 0 && rs[i] == '\\'; i-- {
		n++
	}
	if n%2 == 1 {
		return false // cut between a backslash and the char it escapes
	}
	if (rs[cut-1] == '*' || rs[cut-1] == '_') && rs[cut-1] == rs[cut] {
		return false
	}
	return true
}

// chooseCut picks a split index in (0, budget]: after a newline, else after a
// space (both in the second half), else before a URL straddling the boundary,
// else a hard cut.
func chooseCut(rs []rune, budget int) int {
	cut := 0
	for _, sep := range []rune{'\n', ' '} {
		for i := budget - 1; i > budget/2; i-- {
			if rs[i] == sep {
				cut = i + 1
				break
			}
		}
		if cut != 0 {
			break
		}
	}
	if cut == 0 {
		cut = budget
		s := string(rs)
		for _, m := range urlRe.FindAllStringIndex(s, -1) {
			start, end := len([]rune(s[:m[0]])), len([]rune(s[:m[1]]))
			if start < budget && budget < end && start > 0 {
				cut = start
				break
			}
		}
	}
	for cut > 1 && !validCut(rs, cut) {
		cut--
	}
	return cut
}

// splitMarkdown cuts text into pieces of at most limit runes. A ** or __ pair
// left open at a cut is closed there and reopened in the next piece.
func splitMarkdown(text string, limit int) []string {
	rs := []rune(text)
	var out []string
	for len(rs) > limit {
		cut := chooseCut(rs, limit-2) // room for a closing marker
		head := rs[:cut]
		star, under := openPairs(head)
		piece, reopen := string(head), ""
		if star {
			piece += "**"
			reopen += "**"
		}
		if under && !star {
			piece += "__"
			reopen += "__"
		}
		out = append(out, piece)
		rs = append([]rune(reopen), rs[cut:]...)
	}
	if len(strings.TrimSpace(string(rs))) > 0 {
		out = append(out, string(rs))
	}
	return out
}

// SendMarkdown sends text with parse_mode "markdown". URLs are escaped so
// they survive intact. A chunk the API rejects is resent as plain text, so
// formatting never drops a reply.
func (z *ZaloBotService) SendMarkdown(ctx context.Context, chatID, text string) error {
	for _, chunk := range splitMarkdown(escapeURLs(text), maxTextLen) {
		_, err := z.call(ctx, "sendMessage", sendMessageRequest{ChatID: chatID, Text: chunk, ParseMode: "markdown"})
		var ae *apiError
		if errors.As(err, &ae) {
			_, err = z.call(ctx, "sendMessage", sendMessageRequest{ChatID: chatID, Text: unescapeURLs(chunk)})
		}
		if err != nil {
			return err
		}
	}
	return nil
}
