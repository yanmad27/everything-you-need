package chat

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// maxTrendingItems bounds the compact list handed to the model.
const maxTrendingItems = 25

var (
	trendArticleRe = regexp.MustCompile(`(?s)<article[^>]*class="[^"]*Box-row[^"]*"[^>]*>(.*?)</article>`)
	trendRepoRe    = regexp.MustCompile(`(?s)<h2[^>]*>.*?href="/([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)"`)
	trendDescRe    = regexp.MustCompile(`(?s)<p[^>]*>(.*?)</p>`)
	trendLangRe    = regexp.MustCompile(`itemprop="programmingLanguage"[^>]*>([^<]+)<`)
	trendTodayRe   = regexp.MustCompile(`([\d,.]+)\s+stars?\s+(today|this week|this month)`)
	trendTotalRe   = regexp.MustCompile(`(?s)href="/[^"/]+/[^"/]+/stargazers"[^>]*>(.*?)</a>`)
)

// isGitHubTrending reports whether rawURL is github.com/trending (optionally
// with a language path or query).
func isGitHubTrending(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return false
	}
	h := strings.ToLower(u.Hostname())
	p := strings.Trim(u.Path, "/")
	return (h == "github.com" || h == "www.github.com") && (p == "trending" || strings.HasPrefix(p, "trending/"))
}

// fetchTrending reads the GitHub trending page directly (the reader returns it
// cut off after the site menu) and returns a compact list. ok is false when the
// page cannot be fetched or parsed, so the caller falls back to the generic path.
func fetchTrending(ctx context.Context, client *http.Client, rawURL string) (string, bool) {
	if !isGitHubTrending(rawURL) {
		return "", false
	}
	body, err := getBody(ctx, client, rawURL)
	if err != nil {
		return "", false
	}
	out := parseTrending(body)
	return out, out != ""
}

func cleanHTMLText(s string) string {
	s = tagRe.ReplaceAllString(s, " ")
	return strings.Join(strings.Fields(html.UnescapeString(s)), " ")
}

// parseTrending turns the trending page HTML into a numbered list of at most
// maxTrendingItems repositories: name, description, language, stars.
func parseTrending(page string) string {
	var b strings.Builder
	n := 0
	for _, m := range trendArticleRe.FindAllStringSubmatch(page, -1) {
		art := m[1]
		r := trendRepoRe.FindStringSubmatch(art)
		if r == nil {
			continue
		}
		n++
		fmt.Fprintf(&b, "%d. %s", n, r[1])
		if d := trendDescRe.FindStringSubmatch(art); d != nil {
			if t := cleanHTMLText(d[1]); t != "" {
				if rs := []rune(t); len(rs) > 160 {
					t = string(rs[:160]) + "…"
				}
				fmt.Fprintf(&b, " — %s", t)
			}
		}
		var meta []string
		if l := trendLangRe.FindStringSubmatch(art); l != nil {
			meta = append(meta, strings.TrimSpace(html.UnescapeString(l[1])))
		}
		if t := trendTotalRe.FindStringSubmatch(art); t != nil {
			if v := cleanHTMLText(t[1]); v != "" {
				meta = append(meta, "★"+v)
			}
		}
		if t := trendTodayRe.FindStringSubmatch(art); t != nil {
			meta = append(meta, "+"+t[1]+" "+t[2])
		}
		if len(meta) > 0 {
			fmt.Fprintf(&b, " [%s]", strings.Join(meta, ", "))
		}
		b.WriteString("\n")
		if n >= maxTrendingItems {
			break
		}
	}
	if n == 0 {
		return ""
	}
	return "GitHub Trending (repo đang lên hôm nay, nguồn https://github.com/trending):\n" + b.String()
}
