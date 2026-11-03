package admin

import (
	"context"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"everything-you-need/m/services/chat"
	"everything-you-need/m/services/convo"
	"everything-you-need/m/services/memory"
)

const secretText = "<script>alert(1)</script> thích <b>cà phê</b>"

var (
	alice = memory.Scope{Platform: "zalo", UserID: "alice<1>"}
	bob   = memory.Scope{Platform: "zalo", UserID: "bob"}
)

// rawMem serves unsanitised text to prove the page escapes it.
type rawMem struct{ MemoryData }

func (rawMem) Users(context.Context) ([]memory.UserSummary, error) { return nil, nil }
func (rawMem) UserMemories(context.Context, string, string) ([]memory.Memory, error) {
	return []memory.Memory{{ID: 1, Kind: "other", Text: secretText, Source: "dm"}}, nil
}

func memFixture(t *testing.T) (*Handler, *convo.Store, *memory.Store, []int64) {
	t.Helper()
	ms, err := memory.Open(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ms.Close() })
	cand := func(text string) memory.Candidate {
		return memory.Candidate{Kind: memory.KindPreference, Text: text, Importance: 0.5, Confidence: 0.8, Shareable: true, Source: memory.SourceDM}
	}
	var ids []int64
	for _, x := range []struct {
		sc memory.Scope
		tx string
	}{{alice, secretText}, {alice, "ở Hà Nội"}, {bob, "chơi cờ vua"}} {
		id, _, err := ms.Upsert(context.Background(), x.sc, cand(x.tx))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	h, st, _ := realH(t, Config{}, Options{Memory: ms, PasswordAttempts: 50})
	return h, st, ms, ids
}

func memPost(h *Handler, ck *http.Cookie, csrf, pw string, sc memory.Scope, extra url.Values) int {
	f := url.Values{"csrf": {csrf}, "admin_password": {pw}, "platform": {sc.Platform}, "id": {sc.UserID}}
	for k, v := range extra {
		f[k] = v
	}
	if csrf == "-" {
		f.Del("csrf")
	}
	if pw == "-" {
		f.Del("admin_password")
	}
	return do(h, "POST", "/admin/memory/delete", f, ck).Code
}

func countOf(t *testing.T, ms *memory.Store, sc memory.Scope) int {
	t.Helper()
	m, err := ms.UserMemories(context.Background(), sc.Platform, sc.UserID)
	if err != nil {
		t.Fatal(err)
	}
	return len(m)
}

func TestMemoryPages(t *testing.T) {
	h, _, _, _ := memFixture(t)
	for _, p := range []string{"/admin/memory", "/admin/memory?platform=zalo&id=bob", "/admin/memory/delete"} {
		if w := do(h, "GET", p, nil, nil); w.Code != http.StatusFound || !strings.Contains(w.Header().Get("Location"), "/admin/login") {
			t.Errorf("unauthenticated %s: %d", p, w.Code)
		}
	}
	ck := loginSession(t, h)
	w := do(h, "GET", "/admin/memory", nil, ck)
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, "alice&lt;1&gt;") || strings.Contains(body, "alice<1>") || !strings.Contains(body, `href="/admin/memory"`) || !strings.Contains(body, `aria-current="page"`) {
		t.Fatalf("list: %d %s", w.Code, body)
	}
	w = do(h, "GET", "/admin/memory?platform=zalo&id="+url.QueryEscape(alice.UserID), nil, ck)
	body = w.Body.String()
	if w.Code != 200 || strings.Contains(body, "<script>alert") || !strings.Contains(body, "ở Hà Nội") || strings.Contains(body, "chơi cờ vua") {
		t.Fatalf("detail: %d %s", w.Code, body)
	}
	// the store strips angle brackets on write; a raw source must still be escaped by the page
	h3, err := New(Config{Username: "owner", PasswordHash: testHash}, fixture{}, Options{Memory: rawMem{}})
	if err != nil {
		t.Fatal(err)
	}
	b3 := do(h3, "GET", "/admin/memory?platform=zalo&id=u", nil, loginSession(t, h3)).Body.String()
	if strings.Contains(b3, "<script>alert") || strings.Contains(b3, "<b>cà phê</b>") || !strings.Contains(b3, "&lt;script&gt;alert(1)&lt;/script&gt; thích &lt;b&gt;cà phê&lt;/b&gt;") {
		t.Errorf("not escaped: %s", b3)
	}
	if !strings.Contains(body, "Xoá toàn bộ bộ nhớ của người này") || !strings.Contains(body, `name="admin_password"`) || !strings.Contains(body, `name="csrf"`) {
		t.Error("delete forms missing")
	}
	if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("csp %q", csp)
	}
	if strings.Contains(body, " style=") || strings.Contains(body, "<script") {
		t.Error("inline style or script")
	}
	// disabled without a store
	h2, _ := newH(t)
	ck2 := loginSession(t, h2)
	for _, p := range []string{"/admin/memory", "/admin/memory?platform=zalo&id=x"} {
		if w := do(h2, "GET", p, nil, ck2); w.Code != http.StatusNotFound {
			t.Errorf("disabled %s: %d", p, w.Code)
		}
	}
	if w := do(h2, "POST", "/admin/memory/delete", url.Values{"csrf": {"x"}}, ck2); w.Code != http.StatusNotFound {
		t.Errorf("disabled post: %d", w.Code)
	}
}

func TestMemoryDeleteRequiresCSRFAndPassword(t *testing.T) {
	h, _, ms, ids := memFixture(t)
	ck := loginSession(t, h)
	csrf := sessCSRF(t, h, ck)
	one := url.Values{"memory_id": {itoa(ids[0])}}
	all := url.Values{"all": {"1"}}

	// GET never deletes
	if w := do(h, "GET", "/admin/memory/delete?platform=zalo&id="+url.QueryEscape(alice.UserID)+"&all=1&memory_id="+itoa(ids[0]), nil, ck); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("get: %d", w.Code)
	}
	for _, c := range []struct {
		name, csrf, pw string
		extra          url.Values
	}{
		{"one/no csrf", "-", testPass, one}, {"one/bad csrf", "bad", testPass, one},
		{"one/no pw", csrf, "-", one}, {"one/empty pw", csrf, "", one}, {"one/wrong pw", csrf, "nope", one},
		{"all/no csrf", "-", testPass, all}, {"all/bad csrf", "bad", testPass, all},
		{"all/no pw", csrf, "-", all}, {"all/wrong pw", csrf, "nope", all},
	} {
		if code := memPost(h, ck, c.csrf, c.pw, alice, c.extra); code != http.StatusForbidden {
			t.Errorf("%s: want 403, got %d", c.name, code)
		}
		if n := countOf(t, ms, alice); n != 2 {
			t.Fatalf("%s deleted something: %d left", c.name, n)
		}
	}
	// success: one
	w := do(h, "POST", "/admin/memory/delete", url.Values{"csrf": {csrf}, "admin_password": {testPass}, "platform": {alice.Platform}, "id": {alice.UserID}, "memory_id": {itoa(ids[0])}}, ck)
	if w.Code != http.StatusSeeOther || !strings.Contains(w.Header().Get("Location"), "flash=deleted") || !strings.HasPrefix(w.Header().Get("Location"), "/admin/memory?") {
		t.Fatalf("delete one: %d %s", w.Code, w.Header().Get("Location"))
	}
	if n := countOf(t, ms, alice); n != 1 {
		t.Fatalf("after delete-one: %d", n)
	}
	// another user's memory id under alice's scope deletes nothing
	if code := memPost(h, ck, csrf, testPass, alice, url.Values{"memory_id": {itoa(ids[2])}}); code != http.StatusSeeOther {
		t.Errorf("foreign id: %d", code)
	}
	if countOf(t, ms, bob) != 1 {
		t.Fatal("bob's memory deleted via alice scope")
	}
	// success: all, bob untouched
	if code := memPost(h, ck, csrf, testPass, alice, all); code != http.StatusSeeOther {
		t.Fatalf("delete all: %d", code)
	}
	if countOf(t, ms, alice) != 0 || countOf(t, ms, bob) != 1 {
		t.Fatalf("delete-all scope wrong: alice=%d bob=%d", countOf(t, ms, alice), countOf(t, ms, bob))
	}
	// the notice renders after the redirect
	w = do(h, "GET", "/admin/memory?platform=zalo&id=bob&flash=deleted_all_2", nil, ck)
	if !strings.Contains(w.Body.String(), "Đã xoá toàn bộ bộ nhớ của người này (2 ký ức)") {
		t.Error("notice missing")
	}
}

func TestMemoryDeleteAudit(t *testing.T) {
	h, st, ms, ids := memFixture(t)
	ck := loginSession(t, h)
	csrf := sessCSRF(t, h, ck)
	if memPost(h, ck, csrf, "nope", alice, url.Values{"all": {"1"}}) != http.StatusForbidden {
		t.Fatal("wrong pw accepted")
	}
	memPost(h, ck, csrf, testPass, alice, url.Values{"memory_id": {itoa(ids[0])}})
	memPost(h, ck, csrf, testPass, alice, url.Values{"all": {"1"}})
	_ = ms
	rows, err := st.AdminAudit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var del, delAll bool
	for _, r := range rows {
		for _, s := range []string{r.Event, r.Detail, r.Actor, r.IP} {
			if strings.Contains(s, "cà phê") || strings.Contains(s, "script") || strings.Contains(s, "Hà Nội") {
				t.Fatalf("memory text in audit: %+v", r)
			}
		}
		switch r.Event {
		case "memory_delete":
			del = r.Actor == "owner" && r.Detail == "zalo/alice<1> n=1"
		case "memory_delete_all":
			delAll = r.Actor == "owner" && r.Detail == "zalo/alice<1> n=1" // one was already deleted
		}
	}
	if !del || !delAll {
		t.Fatalf("audit entries missing/incorrect: %+v", rows)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestUsageMemorySplit(t *testing.T) {
	h, st, _ := realH(t, Config{}, Options{})
	ctx := context.Background()
	st.RecordMessage(ctx, chat.MessageEvent{Platform: "zalo", ChatID: "c1", UserID: "u1", TurnID: "t1", Model: "m-a", Rounds: 1, PromptTokens: 1_000_000, Total: 1_000_000})
	st.RecordMessage(ctx, chat.MessageEvent{Platform: "zalo", ChatID: "c1", UserID: "u1", TurnID: "t2", Model: "m-a", Kind: chat.UsageKindMemory, Rounds: 1, PromptTokens: 500_000, Total: 500_000})
	_ = st.Flush(ctx)
	ck := loginSession(t, h)
	w := do(h, "GET", "/admin/api/usage", nil, ck)
	b := w.Body.String()
	if w.Code != 200 || !strings.Contains(b, `"chat_totals":{`) || !strings.Contains(b, `"memory_totals":{`) {
		t.Fatalf("api: %d %s", w.Code, b)
	}
	for _, want := range []string{`"total_tokens":1500000`, `"total_tokens":1000000`, `"total_tokens":500000`} {
		if !strings.Contains(b, want) {
			t.Errorf("missing %s in %s", want, b)
		}
	}
	page := do(h, "GET", "/admin/usage", nil, ck).Body.String()
	if !strings.Contains(page, "Bộ nhớ (trích xuất)") || !strings.Contains(page, "0.50") || !strings.Contains(page, "1.50") {
		t.Errorf("page split missing:\n%s", page)
	}
}
