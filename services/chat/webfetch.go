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
func augmentWithURLs(ctx context.Context, prompt string) string {
	urls := urlRe.FindAllString(prompt, 2)
	if len(urls) == 0 {
		return prompt
	}
	var b strings.Builder
	b.WriteString(prompt)
	for _, u := range urls {
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
	if text, err := getText(ctx, "https://r.jina.ai/"+rawURL, false); err == nil {
		return text, nil
	} else {
		log.Printf("chat: reader %s: %v (trying direct)", rawURL, err)
	}
	return getText(ctx, rawURL, true)
}

// getText GETs a URL and returns readable text. stripHTML runs the tag stripper
// (needed for a direct fetch; the reader already returns markdown).
func getText(ctx context.Context, url string, stripHTML bool) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "vi,en;q=0.9")
	resp, err := fetchHTTP.Do(req)
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
	s := string(raw)
	if stripHTML {
		s = htmlToText(s)
	} else {
		s = strings.TrimSpace(s) // reader returns clean markdown; keep its structure
		if len(s) > maxPageChars {
			s = strings.ToValidUTF8(s[:maxPageChars], "")
		}
	}
	if s == "" {
		return "", fmt.Errorf("no readable text")
	}
	return s, nil
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
