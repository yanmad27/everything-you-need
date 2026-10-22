package chat

import (
	"context"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var (
	urlRe     = regexp.MustCompile(`https?://[^\s<>"]+`)
	scriptRe  = regexp.MustCompile(`(?is)<(script|style|noscript)[^>]*>.*?</(script|style|noscript)>`)
	tagRe     = regexp.MustCompile(`(?s)<[^>]+>`)
	fetchHTTP = &http.Client{Timeout: 30 * time.Second}
)

const browserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"

// maxPageChars caps how much page text is fed to the model (keeps token cost sane).
const maxPageChars = 12000

// augmentWithURLs appends readable text of any URLs found in the prompt so the
// model can answer about a link the user pasted. Best-effort: fetch failures are
// logged and skipped (up to 2 URLs).
func augmentWithURLs(ctx context.Context, prompt string, safe bool) string {
	urls := urlRe.FindAllString(prompt, 2)
	if len(urls) == 0 {
		return prompt
	}
	var b strings.Builder
	b.WriteString(prompt)
	for _, u := range urls {
		if safe {
			text, err := fetchPageTextSafe(ctx, u)
			if err != nil {
				// No URL or transport detail in logs / model text.
				log.Printf("chat: link fetch failed (safe mode)")
				b.WriteString("\n\n[Không đọc được nội dung một liên kết. Hãy nói với người dùng là không truy cập được trang này và gợi ý họ dán nội dung vào.]")
				continue
			}
			fmt.Fprintf(&b, "\n\n[Nội dung trích từ trang %s]:\n%s", u, text)
			continue
		}
		text, err := fetchPageText(ctx, u)
		if err != nil {
			log.Printf("chat: fetch %s: %v", u, err)
			fmt.Fprintf(&b, "\n\n[Không đọc được nội dung trang %s (%v). Hãy nói với người dùng là không truy cập được trang này và gợi ý họ dán nội dung vào.]", u, err)
			continue
		}
		fmt.Fprintf(&b, "\n\n[Nội dung trích từ trang %s]:\n%s", u, text)
	}
	return b.String()
}

// fetchPageText prefers the r.jina.ai reader (fetches from its own infra, so it
// bypasses WAF/IP blocks and renders JS, returning clean LLM-ready text). Falls
// back to a direct fetch if the reader is unavailable.
func fetchPageText(ctx context.Context, rawURL string) (string, error) {
	if text, ok := fetchTrending(ctx, fetchHTTP, rawURL); ok {
		return text, nil
	}
	if text, err := getText(ctx, fetchHTTP, "https://r.jina.ai/"+rawURL, false); err == nil {
		return text, nil
	} else {
		log.Printf("chat: reader %s: %v (trying direct)", rawURL, err)
	}
	return getText(ctx, fetchHTTP, rawURL, true)
}

// getText GETs a URL and returns readable text. stripHTML runs the tag stripper
// (needed for a direct fetch; the reader already returns markdown).
func getText(ctx context.Context, client *http.Client, url string, stripHTML bool) (string, error) {
	raw, err := getBody(ctx, client, url)
	if err != nil {
		return "", err
	}
	s := raw
	if stripHTML {
		s = htmlToText(s)
	} else {
		s = condenseReader(s) // reader returns markdown; keep structure, drop nav boilerplate
		if len(s) > maxPageChars {
			s = strings.ToValidUTF8(s[:maxPageChars], "")
		}
	}
	if s == "" {
		return "", fmt.Errorf("no readable text")
	}
	return s, nil
}

// getBody GETs a URL with a browser UA and returns at most 3 MB of the body.
func getBody(ctx context.Context, client *http.Client, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "vi,en;q=0.9")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 3<<20)) // 3 MB cap
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// htmlToText strips script/style/tags, unescapes entities, collapses whitespace,
// and caps the length.
func htmlToText(raw string) string {
	s := scriptRe.ReplaceAllString(raw, " ") // drop script/style
	s = tagRe.ReplaceAllString(s, " ")       // strip remaining tags
	s = html.UnescapeString(s)
	s = strings.Join(strings.Fields(s), " ") // collapse whitespace
	if len(s) > maxPageChars {
		s = strings.ToValidUTF8(s[:maxPageChars], "")
	}
	return s
}

var (
	mdListLinkRe = regexp.MustCompile(`^([*+-])\s+\[([^\]]+)\]\([^)]*\)\s*$`)
	mdLinkOnlyRe = regexp.MustCompile(`^!?\[[^\]]*\]\([^)]*\)$`)
	mdImageRe    = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
)

const (
	// maxReaderLine truncates giant lines (in runes; mega-menus, inlined data) from reader
	// output; kept small so one such line cannot push the content past the caps.
	maxReaderLine = 600
	// minCondensed: below this the condensed text is not trusted (a link-only
	// page) and the uncondensed text is used instead.
	minCondensed = 200
	// maxListLinks: above this many list-item links they are treated as menus and dropped.
	maxListLinks = 40
)

// condenseReader removes blank lines, bare links, images and the tail of huge
// lines so the cap keeps page content instead of a site's navigation menu.
// List-item links keep their text ("- [Title](url)" -> "- Title").
func condenseReader(s string) string {
	s = strings.TrimSpace(s)
	lines := skipLeadingNav(strings.Split(s, "\n"))
	links := 0
	for _, l := range lines {
		if mdListLinkRe.MatchString(strings.TrimSpace(l)) {
			links++
		}
	}
	keepLinks := links <= maxListLinks // a page that is mostly link lists is menus, not content
	var out []string
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if m := mdListLinkRe.FindStringSubmatch(l); m != nil {
			if keepLinks {
				out = append(out, m[1]+" "+m[2])
			}
			continue
		}
		if mdLinkOnlyRe.MatchString(l) {
			continue
		}
		if l = strings.TrimSpace(mdImageRe.ReplaceAllString(l, "")); l == "" || l == "*" || l == "-" || l == "+" {
			continue
		}
		if r := []rune(l); len(r) > maxReaderLine {
			l = string(r[:maxReaderLine]) + "…"
		}
		out = append(out, l)
	}
	if c := strings.Join(out, "\n"); len(c) >= minCondensed {
		return c
	}
	return s
}

// navLeadLines: a heading preceded by at least this many lines is assumed to
// start the main content, the lines before it being site navigation.
const navLeadLines = 25

var mdHeadingRe = regexp.MustCompile(`^#{1,3}\s+\S`)

// skipLeadingNav drops the site menu that reader output puts before the page
// content: when a long run of lines precedes the first heading that is not a
// navigation heading, the run is cut. The first line (the reader's "Title:")
// is kept. Short pages and pages without such a heading are untouched.
func skipLeadingNav(lines []string) []string {
	for i, l := range lines {
		l = strings.TrimSpace(l)
		if !mdHeadingRe.MatchString(l) || navHeadingRe.MatchString(l) {
			continue
		}
		if i < navLeadLines {
			return lines
		}
		return append([]string{lines[0]}, lines[i:]...)
	}
	return lines
}

var navHeadingRe = regexp.MustCompile(`(?i)^#+\s*(navigation|menu|skip to|main menu|site navigation)`)
