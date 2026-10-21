package admin

import (
	"context"
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"everything-you-need/m/services/chat"
	"everything-you-need/m/services/convo"
)

const testPass = "correct horse battery"

var testHash = func() string {
	h, _ := bcrypt.GenerateFromPassword([]byte(testPass), 10)
	return string(h)
}()

type fixture struct{}

func (fixture) ListChats(context.Context, convo.ChatFilter, convo.Page) ([]convo.ChatSummary, int, error) {
	t := time.Date(2026, 10, 1, 9, 0, 0, 0, ict)
	return []convo.ChatSummary{{Platform: "zalo", ChatID: "c1", ChatType: "PRIVATE", DisplayName: "<b>An</b>", FirstActivity: t, LastActivity: t, Messages: 3, ToolCalls: 1}}, 1, nil
}
func (fixture) Timeline(context.Context, string, string, convo.Page) ([]convo.TimelineItem, int, error) {
	t := time.Date(2026, 10, 1, 9, 0, 0, 0, ict)
	return []convo.TimelineItem{
		{Type: "message", At: t, Message: &convo.Message{Direction: "in", Kind: "text", Text: "<script>alert(1)</script>", UserName: "An", CreatedAt: t}},
		{Type: "message", At: t, Message: &convo.Message{Direction: "in", Kind: "image", ImageMime: "image/jpeg", ImageBytes: 240 * 1024, CreatedAt: t}},
		{Type: "tool_call", At: t, Tool: &convo.ToolCall{Name: "fetch_url", Outcome: "error", LatencyMS: 12, ArgsSummary: "example.com", ResultSummary: "error", CreatedAt: t}},
		{Type: "message", At: t, Message: &convo.Message{Direction: "out", Kind: "system", Text: "⚠️", ErrorTag: "status=500", CreatedAt: t}},
	}, 4, nil
}
func (fixture) UserStats(context.Context, convo.StatsFilter) ([]convo.Stat, error) {
	return []convo.Stat{{Platform: "zalo", ID: "u1", Name: "An", Messages: 3, TotalTokens: 10, EstCost: 0.5, TopTools: []convo.ToolCount{{Name: "x", Count: 2}}}}, nil
}
func (fixture) ChatStats(context.Context, convo.StatsFilter) ([]convo.Stat, error) {
	return []convo.Stat{{Platform: "zalo", ID: "c1", Messages: 1}}, nil
}
func (fixture) ToolStats(context.Context, convo.StatsFilter) (convo.ToolReport, error) {
	return convo.ToolReport{Tools: []convo.ToolStat{{Name: "t", Calls: 2, OK: 1, SuccessRate: .5, P50MS: 3, P95MS: 9}},
		RecentFailures: []convo.ToolFailure{{Name: "t", Outcome: "error", ArgsSummary: "<i>"}}}, nil
}

type clk struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clk) now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clk) add(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

func newH(t *testing.T) (*Handler, *clk) {
	t.Helper()
	c := &clk{t: time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)}
	h, err := New(Config{Username: "owner", PasswordHash: testHash}, fixture{}, Options{Now: c.now})
	if err != nil {
		t.Fatal(err)
	}
	return h, c
}

func do(h http.Handler, method, target string, form url.Values, cookie *http.Cookie, hdr ...string) *httptest.ResponseRecorder {
	var body *strings.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	} else {
		body = strings.NewReader("")
	}
	r := httptest.NewRequest(method, target, body)
	r.RemoteAddr = "203.0.113.9:5555"
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

var csrfRe = regexp.MustCompile(`name="csrf" value="([^"]+)"`)

func loginCSRF(t *testing.T, h http.Handler) string {
	t.Helper()
	m := csrfRe.FindStringSubmatch(do(h, "GET", "/admin/login", nil, nil).Body.String())
	if m == nil {
		t.Fatal("no csrf field")
	}
	return m[1]
}

func login(t *testing.T, h http.Handler, user, pass string, hdr ...string) *httptest.ResponseRecorder {
	t.Helper()
	return do(h, "POST", "/admin/login", url.Values{"csrf": {loginCSRF(t, h)}, "username": {user}, "password": {pass}}, nil, hdr...)
}

func loginSession(t *testing.T, h http.Handler) *http.Cookie {
	t.Helper()
	w := login(t, h, "owner", testPass)
	if w.Code != http.StatusFound {
		t.Fatalf("login status %d", w.Code)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == CookieName {
			return c
		}
	}
	t.Fatal("no cookie")
	return nil
}

var htmlRoutes = []string{"/admin", "/admin/", "/admin/chats", "/admin/chat?platform=zalo&id=c1", "/admin/users", "/admin/chat-stats", "/admin/tools", "/admin/nope"}
var apiRoutes = []string{"/admin/api/chats", "/admin/api/timeline?platform=zalo&id=c1", "/admin/api/users", "/admin/api/chat-stats", "/admin/api/tools", "/admin/api/nope"}

func TestLoggedOutRedirectsAndRejects(t *testing.T) {
	h, _ := newH(t)
	for _, p := range htmlRoutes {
		if w := do(h, "GET", p, nil, nil); w.Code != 302 || w.Header().Get("Location") != "/admin/login" {
			t.Errorf("%s: %d %s", p, w.Code, w.Header().Get("Location"))
		}
	}
	for _, p := range apiRoutes {
		if w := do(h, "GET", p, nil, nil); w.Code != 401 {
			t.Errorf("%s: %d", p, w.Code)
		}
	}
	if w := do(h, "POST", "/admin/logout", url.Values{}, nil); w.Code != 302 {
		t.Errorf("logout logged out: %d", w.Code)
	}
	if w := do(h, "GET", "/admin/chats", nil, &http.Cookie{Name: CookieName, Value: "forged"}); w.Code != 302 {
		t.Errorf("forged cookie: %d", w.Code)
	}
}

func TestLoginWrongCredsGeneric(t *testing.T) {
	h, _ := newH(t)
	a := login(t, h, "owner", "wrong")
	b := login(t, h, "nobody", testPass)
	for _, w := range []*httptest.ResponseRecorder{a, b} {
		if w.Code != 401 || !strings.Contains(w.Body.String(), "Sai tên đăng nhập hoặc mật khẩu") || len(w.Result().Cookies()) != 0 {
			t.Errorf("wrong creds: %d %v", w.Code, w.Result().Cookies())
		}
	}
	nonceRe := regexp.MustCompile(`nonce="[^"]+"`)
	strip := func(s string) string { return nonceRe.ReplaceAllString(csrfRe.ReplaceAllString(s, ""), "") }
	if strip(a.Body.String()) != strip(b.Body.String()) {
		t.Error("error responses differ between unknown user and wrong password")
	}
}

func TestLoginRateLimit(t *testing.T) {
	h, c := newH(t)
	for i := 0; i < 5; i++ {
		if w := login(t, h, "owner", "bad"); w.Code != 401 {
			t.Fatalf("attempt %d: %d", i, w.Code)
		}
	}
	if w := login(t, h, "owner", testPass); w.Code != 429 { // even correct creds are refused while limited
		t.Errorf("6th attempt: %d", w.Code)
	}
	// another client is unaffected; CF-Connecting-IP identifies clients.
	if w := login(t, h, "owner", testPass, "CF-Connecting-IP", "198.51.100.7"); w.Code != 302 {
		t.Errorf("other ip: %d", w.Code)
	}
	c.add(16 * time.Minute)
	if w := login(t, h, "owner", testPass); w.Code != 302 {
		t.Errorf("after window: %d", w.Code)
	}
}

func TestGlobalRateLimitCountsFailuresOnly(t *testing.T) {
	c := &clk{t: time.Now()}
	h, _ := New(Config{Username: "owner", PasswordHash: testHash}, fixture{}, Options{Now: c.now, GlobalLoginAttempt: 3})
	for i := 0; i < 5; i++ { // successful logins never consume the global bucket
		if w := login(t, h, "owner", testPass, "CF-Connecting-IP", "198.51.100."+string(rune('1'+i))); w.Code != 302 {
			t.Fatalf("success %d: %d", i, w.Code)
		}
	}
	for i := 0; i < 3; i++ {
		login(t, h, "owner", "bad", "CF-Connecting-IP", "198.51.101."+string(rune('1'+i)))
	}
	if w := login(t, h, "owner", testPass, "CF-Connecting-IP", "198.51.102.9"); w.Code != 429 {
		t.Errorf("after global failures: %d", w.Code)
	}
	c.add(16 * time.Minute)
	if w := login(t, h, "owner", testPass, "CF-Connecting-IP", "198.51.102.9"); w.Code != 302 {
		t.Errorf("after window: %d", w.Code)
	}
}

func TestDefaultGlobalLimitIsGenerous(t *testing.T) {
	h, _ := newH(t)
	if h.allLim.max != 300 || h.ipLim.max != 5 {
		t.Errorf("limits ip=%d global=%d", h.ipLim.max, h.allLim.max)
	}
}

func TestClientIPOrder(t *testing.T) {
	mk := func(remote string, hdr ...string) string {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = remote
		for i := 0; i+1 < len(hdr); i += 2 {
			r.Header.Set(hdr[i], hdr[i+1])
		}
		return clientIP(r)
	}
	if got := mk("10.0.0.1:1", "CF-Connecting-IP", "198.51.100.1", "X-Forwarded-For", "1.1.1.1, 2.2.2.2"); got != "198.51.100.1" {
		t.Errorf("cf: %s", got)
	}
	if got := mk("10.0.0.1:1", "X-Forwarded-For", "6.6.6.6, 2.2.2.2"); got != "2.2.2.2" { // last hop, not client-supplied first
		t.Errorf("xff: %s", got)
	}
	if got := mk("10.0.0.1:1", "CF-Connecting-IP", "junk", "X-Forwarded-For", "nonsense"); got != "10.0.0.1" {
		t.Errorf("fallback: %s", got)
	}
}

func TestLoginCSRF(t *testing.T) {
	h, c := newH(t)
	for name, tok := range map[string]string{"missing": "", "garbage": "abc.def", "tampered": loginCSRF(t, h) + "x"} {
		w := do(h, "POST", "/admin/login", url.Values{"csrf": {tok}, "username": {"owner"}, "password": {testPass}}, nil)
		if w.Code != 403 || len(w.Result().Cookies()) != 0 {
			t.Errorf("%s: %d", name, w.Code)
		}
	}
	old := loginCSRF(t, h)
	c.add(time.Hour)
	if w := do(h, "POST", "/admin/login", url.Values{"csrf": {old}, "username": {"owner"}, "password": {testPass}}, nil); w.Code != 403 {
		t.Errorf("expired csrf: %d", w.Code)
	}
}

func TestSuccessfulLoginCookieAndPages(t *testing.T) {
	h, _ := newH(t)
	ck := loginSession(t, h)
	if ck.Name != "__Host-eyn_admin" || !ck.HttpOnly || !ck.Secure || ck.SameSite != http.SameSiteStrictMode || ck.Path != "/" || ck.Domain != "" || len(ck.Value) < 40 {
		t.Errorf("cookie = %+v", ck)
	}
	for _, p := range htmlRoutes[:7] {
		w := do(h, "GET", p, nil, ck)
		if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
			t.Errorf("%s: %d", p, w.Code)
		}
	}
	for _, p := range apiRoutes[:5] {
		w := do(h, "GET", p, nil, ck)
		if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
			t.Errorf("%s: %d", p, w.Code)
		}
	}
	if w := do(h, "GET", "/admin/nope", nil, ck); w.Code != 404 {
		t.Errorf("unknown: %d", w.Code)
	}
	if w := do(h, "POST", "/admin/chats", url.Values{}, ck); w.Code != 405 {
		t.Errorf("POST data route: %d", w.Code)
	}
	if w := do(h, "GET", "/admin/login", nil, ck); w.Code != 302 || w.Header().Get("Location") != "/admin" {
		t.Errorf("login while logged in: %d", w.Code)
	}
}

func TestEscapingAndImagePlaceholder(t *testing.T) {
	h, _ := newH(t)
	ck := loginSession(t, h)
	body := do(h, "GET", "/admin/chat?platform=zalo&id=c1", nil, ck).Body.String()
	if strings.Contains(body, "<script>alert") || !strings.Contains(body, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Error("message text not escaped")
	}
	if !strings.Contains(body, "image/jpeg 240 KB") || !strings.Contains(body, "fetch_url") || !strings.Contains(body, "status=500") {
		t.Errorf("timeline body missing parts")
	}
	list := do(h, "GET", "/admin/chats", nil, ck).Body.String()
	if strings.Contains(list, "<b>An</b>") || !strings.Contains(list, "&lt;b&gt;An&lt;/b&gt;") {
		t.Error("display name not escaped")
	}
	if strings.Contains(do(h, "GET", "/admin/tools", nil, ck).Body.String(), "<i>") {
		t.Error("args not escaped")
	}
	if strings.Contains(strings.ToLower(body), "<script") {
		t.Error("script tag present in page")
	}
}

func TestHeaders(t *testing.T) {
	h, _ := newH(t)
	ck := loginSession(t, h)
	for _, w := range []*httptest.ResponseRecorder{do(h, "GET", "/admin/login", nil, nil), do(h, "GET", "/admin/chats", nil, ck), do(h, "GET", "/admin/api/tools", nil, ck), do(h, "GET", "/admin/api/tools", nil, nil), do(h, "GET", "/admin/chats", nil, nil)} {
		hd := w.Header()
		csp := hd.Get("Content-Security-Policy")
		if hd.Get("Cache-Control") != "no-store" || hd.Get("X-Content-Type-Options") != "nosniff" || hd.Get("Referrer-Policy") != "no-referrer" ||
			hd.Get("X-Frame-Options") != "DENY" || !strings.HasPrefix(csp, "default-src 'none'") || strings.Contains(csp, "unsafe") || !strings.Contains(csp, "frame-ancestors 'none'") {
			t.Errorf("headers: %v", hd)
		}
	}
	w := do(h, "GET", "/admin/login", nil, nil)
	m := regexp.MustCompile(`nonce-([A-Za-z0-9_-]+)`).FindStringSubmatch(w.Header().Get("Content-Security-Policy"))
	if m == nil || !strings.Contains(w.Body.String(), `<style nonce="`+m[1]+`">`) {
		t.Error("style nonce mismatch")
	}
}

func TestSessionExpiry(t *testing.T) {
	h, c := newH(t)
	ck := loginSession(t, h)
	c.add(time.Hour + 59*time.Minute)
	if w := do(h, "GET", "/admin/chats", nil, ck); w.Code != 200 { // activity refreshes idle
		t.Fatalf("within idle: %d", w.Code)
	}
	c.add(time.Hour + 59*time.Minute)
	if w := do(h, "GET", "/admin/chats", nil, ck); w.Code != 200 {
		t.Fatalf("still active: %d", w.Code)
	}
	c.add(2*time.Hour + time.Minute) // idle
	if w := do(h, "GET", "/admin/chats", nil, ck); w.Code != 302 {
		t.Errorf("idle expiry: %d", w.Code)
	}
	ck = loginSession(t, h)
	for i := 0; i < 8; i++ { // keep alive past the 12h absolute cap
		c.add(100 * time.Minute)
		if w := do(h, "GET", "/admin/chats", nil, ck); i < 6 && w.Code != 200 {
			t.Fatalf("keepalive %d: %d", i, w.Code)
		}
	}
	if w := do(h, "GET", "/admin/chats", nil, ck); w.Code != 302 {
		t.Errorf("absolute expiry: %d", w.Code)
	}
}

func TestLogout(t *testing.T) {
	h, _ := newH(t)
	ck := loginSession(t, h)
	if w := do(h, "POST", "/admin/logout", url.Values{}, ck); w.Code != 403 {
		t.Errorf("no csrf: %d", w.Code)
	}
	if w := do(h, "POST", "/admin/logout", url.Values{"csrf": {"nope"}}, ck); w.Code != 403 {
		t.Errorf("bad csrf: %d", w.Code)
	}
	if w := do(h, "GET", "/admin/logout", nil, ck); w.Code != 405 {
		t.Errorf("GET logout: %d", w.Code)
	}
	page := do(h, "GET", "/admin/chats", nil, ck).Body.String()
	tok := csrfRe.FindStringSubmatch(page)[1]
	w := do(h, "POST", "/admin/logout", url.Values{"csrf": {tok}}, ck)
	if w.Code != 302 || w.Header().Get("Location") != "/admin/login" {
		t.Fatalf("logout: %d", w.Code)
	}
	cleared := false
	for _, c := range w.Result().Cookies() {
		cleared = cleared || (c.Name == CookieName && c.MaxAge < 0 && c.Value == "")
	}
	if !cleared {
		t.Error("cookie not cleared")
	}
	if w := do(h, "GET", "/admin/chats", nil, ck); w.Code != 302 {
		t.Errorf("session survives logout: %d", w.Code)
	}
}

func TestDisabledWithoutValidConfig(t *testing.T) {
	weak, _ := bcrypt.GenerateFromPassword([]byte("x"), 4)
	for name, c := range map[string]Config{
		"no user": {PasswordHash: testHash}, "no hash": {Username: "o"}, "not bcrypt": {Username: "o", PasswordHash: "plaintext"},
		"low cost": {Username: "o", PasswordHash: string(weak)},
	} {
		if h, err := New(c, fixture{}, Options{}); err == nil || h != nil {
			t.Errorf("%s: expected error", name)
		}
	}
	if _, err := New(Config{Username: "o", PasswordHash: testHash}, nil, Options{}); err == nil {
		t.Error("nil data accepted")
	}
}

func TestRealStoreIntegrationEscapes(t *testing.T) {
	s, err := convo.Open(t.TempDir()+"/c.db", convo.Options{NoBackground: true})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.RecordConvo(context.Background(), chat.ConvoEvent{Platform: "zalo", ChatID: "c1", ChatType: "PRIVATE", UserID: "u1", UserName: "An", Direction: "in", Kind: "text", Text: `<img src=x onerror=alert(1)>`})
	_ = s.Flush(context.Background())
	h, _ := New(Config{Username: "owner", PasswordHash: testHash}, s, Options{})
	ck := loginSession(t, h)
	body := do(h, "GET", "/admin/chat?platform=zalo&id=c1", nil, ck).Body.String()
	if strings.Contains(body, "<img src=x") || !strings.Contains(body, "&lt;img src=x") {
		t.Errorf("real store text not escaped")
	}
	if w := do(h, "GET", "/admin/chats?q=%25&from=bad", nil, ck); w.Code != 200 {
		t.Errorf("chats: %d", w.Code)
	}
}

func TestConfigValuesNormalised(t *testing.T) {
	ok := map[string]Config{
		"unquoted":    {Username: "owner", PasswordHash: testHash},
		"single":      {Username: "owner", PasswordHash: "'" + testHash + "'"},
		"double":      {Username: "owner", PasswordHash: `"` + testHash + `"`},
		"spaces":      {Username: "owner", PasswordHash: "  '" + testHash + "' \n"},
		"user quoted": {Username: " 'owner' ", PasswordHash: testHash, SessionSecret: ` "s3cret" `},
	}
	for name, c := range ok {
		h, err := New(c, fixture{}, Options{})
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if string(h.hash) != testHash {
			t.Errorf("%s: hash not normalised", name)
		}
		if h.user != sha256.Sum256([]byte("owner")) {
			t.Errorf("%s: username not normalised", name)
		}
	}
	h, _ := New(ok["user quoted"], fixture{}, Options{})
	if string(h.secret) != "s3cret" {
		t.Errorf("secret not normalised")
	}
	for name, hash := range map[string]string{"leading only": "'" + testHash, "mismatched": "'" + testHash + `"`} {
		if h, err := New(Config{Username: "owner", PasswordHash: hash}, fixture{}, Options{}); err == nil || h != nil {
			t.Errorf("%s: expected error", name)
		}
	}
}
