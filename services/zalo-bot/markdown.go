package zalobot

import (
	"context"
	"errors"
	"regexp"
	"strings"
)

var urlRe = regexp.MustCompile(`https?://\S+`)

const (
	mdSpecial = "_*~\\"
	// urlTrail are trailing characters that belong to the surrounding text or
	// markdown, not to the URL.
	urlTrail = "*_~.,)!?:;"
)

// escapeURLs backslash-escapes markdown-significant characters inside bare
// http(s) URLs so Zalo's markdown (which has no links) does not mangle them.
// Trailing markers and punctuation are left out of the URL and stay untouched.
func escapeURLs(text string) string {
	return urlRe.ReplaceAllStringFunc(text, func(u string) string {
		body := strings.TrimRight(u, urlTrail)
		tail := u[len(body):]
		var sb strings.Builder
		for _, r := range body {
			if strings.ContainsRune(mdSpecial, r) {
				sb.WriteByte('\\')
			}
			sb.WriteRune(r)
		}
		return sb.String() + tail
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

// openPairs scans rs and returns the ** / __ markers left open, in the order
// they were opened.
func openPairs(rs []rune) []string {
	var open []string
	toggle := func(m string) {
		for i, o := range open {
			if o == m {
				open = append(open[:i], open[i+1:]...)
				return
			}
		}
		open = append(open, m)
	}
	for i := 0; i < len(rs); i++ {
		switch {
		case rs[i] == '\\':
			i++
		case rs[i] == '*' && i+1 < len(rs) && rs[i+1] == '*':
			toggle("**")
			i++
		case rs[i] == '_' && i+1 < len(rs) && rs[i+1] == '_':
			toggle("__")
			i++
		}
	}
	return open
}

// validCut reports whether cutting before rs[cut] leaves escapes and ** / __
// markers intact.
// splitsEscape reports whether cutting at cut separates a backslash from the
// character it escapes.
func splitsEscape(rs []rune, cut int) bool {
	if cut <= 0 || cut >= len(rs) {
		return false
	}
	n := 0
	for i := cut - 1; i >= 0 && rs[i] == '\\'; i-- {
		n++
	}
	return n%2 == 1
}

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
	hard := cut
	for cut > 1 && !validCut(rs, cut) {
		cut--
	}
	if cut < budget/2 {
		// Long runs of markers would otherwise yield one tiny piece per rune.
		// Hard-cut at the budget, backing off only to stay rune-safe and not
		// split an escape sequence.
		cut = hard
		for cut > 1 && splitsEscape(rs, cut) {
			cut--
		}
	}
	return cut
}

// mdChunk is one markdown piece plus the same content without the synthetic
// closing/reopening markers and URL escapes (for the plain-text fallback).
type mdChunk struct{ md, plain string }

// closeAt returns the closers to append to head and the openers to prepend to
// the rest. Closers run in reverse order of opening.
func closeAt(head []rune) (closers, reopen string) {
	open := openPairs(head)
	for i := len(open) - 1; i >= 0; i-- {
		closers += open[i]
	}
	return closers, strings.Join(open, "")
}

// splitMarkdown cuts text into pieces of at most limit runes. ** / __ pairs
// left open at a cut are closed there and reopened in the next piece. Every
// iteration strictly shrinks the remaining text.
func splitMarkdown(text string, limit int) []mdChunk {
	const markerRoom = 4 // two closing markers
	rs := []rune(text)
	prefix := 0 // synthetic reopeners at the start of rs
	var out []mdChunk
	for len(rs) > limit {
		budget := limit - markerRoom
		cut := chooseCut(rs, budget)
		closers, reopen := closeAt(rs[:cut])
		if cut <= len(reopen) { // no net progress: hard cut
			cut = budget
			for cut > 1 && !validCut(rs, cut) {
				cut--
			}
			closers, reopen = closeAt(rs[:cut])
		}
		head := rs[:cut]
		plain := head
		if prefix < len(plain) {
			plain = plain[prefix:]
		} else {
			plain = nil
		}
		out = append(out, mdChunk{string(head) + closers, unescapeURLs(string(plain))})
		rs = append([]rune(reopen), rs[cut:]...)
		prefix = len([]rune(reopen))
	}
	if len(strings.TrimSpace(string(rs))) > 0 {
		plain := rs
		if prefix < len(plain) {
			plain = plain[prefix:]
		}
		out = append(out, mdChunk{string(rs), unescapeURLs(string(plain))})
	}
	return out
}

// SendMarkdown sends text with parse_mode "markdown". URLs are escaped so
// they survive intact. If the API rejects a chunk (ok=false; Zalo's error codes
// don't distinguish parse errors, so any API-level rejection triggers it) that
// chunk is resent as plain text without the synthetic markers, so formatting
// never drops a reply. Transport and context errors are returned as they are.
func (z *ZaloBotService) SendMarkdown(ctx context.Context, chatID, text string) error {
	for _, c := range splitMarkdown(escapeURLs(text), maxTextLen) {
		_, err := z.call(ctx, "sendMessage", sendMessageRequest{ChatID: chatID, Text: c.md, ParseMode: "markdown"})
		var ae *apiError
		if errors.As(err, &ae) && strings.TrimSpace(c.plain) != "" {
			_, err = z.call(ctx, "sendMessage", sendMessageRequest{ChatID: chatID, Text: c.plain})
		}
		if err != nil {
			return err
		}
	}
	return nil
}
