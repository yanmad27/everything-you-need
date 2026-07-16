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
	fetchHTTP = &http.Client{Timeout: 12 * time.Second}
)

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
			continue
		}
		fmt.Fprintf(&b, "\n\n[Nội dung trích từ trang %s]:\n%s", u, text)
	}
	return b.String()
}

func fetchPageText(ctx context.Context, rawURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; TroLyPinkBot/1.0)")
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
	s := htmlToText(string(raw))
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
