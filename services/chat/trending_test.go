package chat

import (
	"bytes"
	"context"
	"errors"
	"log"
	"time"

	"everything-you-need/m/services/llm"
	"os"
	"strings"
	"testing"
)

func TestParseTrendingFixture(t *testing.T) {
	b, err := os.ReadFile("testdata/github_trending.html")
	if err != nil {
		t.Fatal(err)
	}
	got := parseTrending(string(b))
	if n := strings.Count(got, "\n"); n < 4 || n > maxTrendingItems+1 {
		t.Fatalf("lines=%d:\n%s", n, got)
	}
	for _, w := range []string{"1. https://github.com/cloudflare/security-audit-skill — A coding-agent skill", "JavaScript", "★36,977", "+2,012 today"} {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in:\n%s", w, got)
		}
	}
	if strings.Contains(got, "<") || strings.Contains(got, "Features") || len(got) > 4000 {
		t.Errorf("not compact (len %d):\n%s", len(got), got)
	}
	// the first repo sits within the result cap
	if i := strings.Index(got, "github.com/cloudflare/"); i < 0 || i > defMaxResult/10 {
		t.Errorf("first repo at %d", i)
	}
	if parseTrending("<html>nothing</html>") != "" {
		t.Error("empty page should not produce a list")
	}
}

func TestParseTrendingCapsItems(t *testing.T) {
	one := `<article class="Box-row"><h2 class="h3"><a href="/o/r%d">o / r</a></h2><p class="col-9">d</p></article>`
	var b strings.Builder
	for i := 0; i < 40; i++ {
		b.WriteString(strings.Replace(one, "%d", string(rune('a'+i%26))+string(rune('a'+i/26)), 1))
	}
	if got := parseTrending(b.String()); strings.Count(got, "\n") != maxTrendingItems+1 {
		t.Errorf("lines=%d", strings.Count(got, "\n"))
	}
}

func TestIsGitHubTrending(t *testing.T) {
	for in, want := range map[string]bool{
		"https://github.com/trending": true, "https://github.com/trending/go?since=weekly": true, "https://www.github.com/trending/": true,
		"https://github.com/trendingx": false, "https://evil.test/trending": false, "https://github.com/a/trending": false,
	} {
		if isGitHubTrending(in) != want {
			t.Errorf("%s", in)
		}
	}
}

func TestSkipLeadingNav(t *testing.T) {
	var b strings.Builder
	b.WriteString("Title: GitHub Trending\n## Navigation Menu\n")
	for i := 0; i < 40; i++ {
		b.WriteString("Product menu item\n")
	}
	b.WriteString("# Trending\n1. repo-one is the first repository listed on the page with description " + strings.Repeat("and more text ", 20) + "\n")
	got := condenseReader(b.String())
	if !strings.HasPrefix(got, "Title: GitHub Trending\n# Trending") || strings.Contains(got, "Product menu") {
		t.Errorf("nav kept: %q", got)
	}
	short := "Title: x\n# Heading\n" + strings.Repeat("body text here. ", 20)
	if got := condenseReader(short); !strings.Contains(got, "# Heading") || !strings.Contains(got, "Title: x") {
		t.Errorf("short page changed: %q", got)
	}
}

func offersSearch(defs []llm.ToolDef) bool {
	for _, d := range defs {
		if d.Name == "web_search" {
			return true
		}
	}
	return false
}

func TestSearchProviderCooldown(t *testing.T) {
	for _, tc := range []struct {
		err   string
		state string
		down  bool
	}{
		{"serper status 400", ProviderError, false}, {"serper status 402", ProviderNoCredit, true},
		{"serper status 401", ProviderAuth, true}, {"serper status 403", ProviderAuth, true},
		{"serper status 500", ProviderError, false},
	} {
		now := time.Date(2026, 10, 22, 9, 0, 0, 0, ictZone)
		var offered []bool
		var buf bytes.Buffer
		log.SetOutput(&buf)
		probe := func(_ []llm.ChatMessage, defs []llm.ToolDef) (llm.ToolResponse, error) {
			offered = append(offered, offersSearch(defs))
			return textStep("ok")(nil, nil)
		}
		r := newRig(t, func(tl *Tools) {
			tl.Search = errSearch{errors.New(tc.err + " body: Not enough credits KEYSECRET")}
			tl.Now = func() time.Time { return now }
		}, toolStep(call("1", "web_search", `{"query":"q"}`)), textStep("a"), probe, probe, probe)
		r.ask("tìm")
		st := ProviderStatuses()[0]
		if st.Name != "serper" || st.State != tc.state || st.Since.IsZero() || st.Detail == "" || strings.Contains(st.Detail, "KEYSECRET") {
			t.Errorf("%s: status %+v", tc.err, st)
		}
		r.ask("hỏi lại")
		now = now.Add(31 * time.Minute)
		r.ask("hỏi nữa")
		log.SetOutput(os.Stderr)
		if len(offered) != 2 || offered[0] == tc.down || !offered[1] {
			t.Errorf("%s: offered=%v", tc.err, offered)
		}
		if got := strings.Count(buf.String(), "reason=provider_unavailable"); (got == 1) != tc.down || got > 1 {
			t.Errorf("%s: provider_unavailable logged %d times", tc.err, got)
		}
		if strings.Contains(buf.String(), "KEYSECRET") {
			t.Error("log leaks upstream text")
		}
	}
}

func TestSearchProviderRecoversOnKeyChange(t *testing.T) {
	now := time.Date(2026, 10, 22, 9, 0, 0, 0, ictZone)
	var offered []bool
	probe := func(_ []llm.ChatMessage, defs []llm.ToolDef) (llm.ToolResponse, error) {
		offered = append(offered, offersSearch(defs))
		return textStep("ok")(nil, nil)
	}
	bad := errSearch{errors.New("serper status 401")}
	r := newRig(t, func(tl *Tools) { tl.Search = bad; tl.Now = func() time.Time { return now } },
		toolStep(call("1", "web_search", `{"query":"q"}`)), textStep("a"), probe, probe)
	r.ask("tìm")
	r.ask("lại")
	r.svc.WithLive(func() Live { return Live{Search: &fakeSearch{}} }) // new backend = new key
	r.ask("lại nữa")
	if len(offered) != 2 || offered[0] || !offered[1] {
		t.Errorf("offered=%v", offered)
	}
	if st := ProviderStatuses()[0]; st.State != ProviderUnknown {
		t.Errorf("state after key change: %+v", st)
	}
}

type creditErr struct{ credits bool }

func (creditErr) Error() string            { return "serper status 400" }
func (e creditErr) CreditsExhausted() bool { return e.credits }

func TestSearch400OnlyWithCreditsCools(t *testing.T) {
	for credits, want := range map[bool]string{true: ProviderNoCredit, false: ProviderError} {
		r := newRig(t, func(tl *Tools) { tl.Search = errSearch{creditErr{credits}} }, toolStep(call("1", "web_search", `{"query":"q"}`)), textStep("ok"))
		r.ask("tìm")
		if st := ProviderStatuses()[0]; st.State != want {
			t.Errorf("credits=%v state=%s", credits, st.State)
		}
	}
}

func TestTaintedFetchOnlySeenOrKnown(t *testing.T) {
	trending := func(_ context.Context, u string) (string, error) {
		if u == "https://github.com/trending" {
			return "1. https://github.com/o/seen-repo — d\n2. https://evil.test/x — d\n", nil
		}
		return "page of " + u, nil
	}
	f := func(u string) llm.ToolCall { return call("f", "fetch_url", `{"url":"`+u+`"}`) }
	ok := map[string]bool{"https://github.com/o/seen-repo": true, "https://github.com/o/seen-repo?q=SECRET": true,
		"https://github.com/trending/python": true, "https://github.com/trending/c++": true, "https://github.com": true,
		"https://github.com/attacker/abc123secret": false, "https://github.com/trending/Python%20x": false,
		"https://github.com/trending/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa": false, "https://evil.test/x": false,
		"https://en.wikipedia.org/wiki/Secret": false, "https://github.com/o/seen-repo/issues/1": false}
	for u, want := range ok {
		r := newRig(t, func(tl *Tools) { tl.Fetch = trending }, toolStep(f("https://github.com/trending")), toolStep(f(u)), textStep("ok"))
		r.ask("trending có gì")
		got := lastTool(r.sc.calls[2])
		if allowed := strings.Contains(got, "page of "); allowed != want || (want && strings.Contains(got, "SECRET")) {
			t.Errorf("%s: allowed=%v want %v: %q", u, allowed, want, got)
		}
		if !want && !strings.Contains(got, "chỉ đọc link") {
			t.Errorf("%s: wrong refusal %q", u, got)
		}
	}
}
