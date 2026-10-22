package admin

import (
	"bytes"
	"context"
	"html/template"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"everything-you-need/m/services/chat"
	"everything-you-need/m/services/convo"
)

const newPass = "Tr0ub4dor&3-new"

func realH(t *testing.T, cfg Config, opt Options) (*Handler, *convo.Store, *clk) {
	t.Helper()
	c := &clk{t: time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)}
	st, err := convo.Open(filepath.Join(t.TempDir(), "a.db"), convo.Options{NoBackground: true, Now: c.now,
		Prices: []convo.Price{{Model: "m-a", PromptPer1M: 1, CompletionPer1M: 10}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cfg.Username, cfg.PasswordHash = "owner", testHash
	opt.Now = c.now
	h, err := New(cfg, st, opt)
	if err != nil {
		t.Fatal(err)
	}
	return h, st, c
}

func sessCSRF(t *testing.T, h *Handler, ck *http.Cookie) string {
	t.Helper()
	m := csrfRe.FindStringSubmatch(do(h, "GET", "/admin/password", nil, ck).Body.String())
	if m == nil {
		t.Fatal("no csrf on password page")
	}
	return m[1]
}

func pwForm(csrf, cur, np, cf string) url.Values {
	return url.Values{"csrf": {csrf}, "current": {cur}, "new": {np}, "confirm": {cf}}
}

func cookieOf(t *testing.T, w interface{ Result() *http.Response }) *http.Cookie {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Name == CookieName && c.Value != "" {
			return c
		}
	}
	t.Fatal("no session cookie")
	return nil
}

func TestPasswordChangeSuccess(t *testing.T) {
	h, st, _ := realH(t, Config{}, Options{})
	a, b := loginSession(t, h), loginSession(t, h)
	var logs bytes.Buffer
	log.SetOutput(&logs)
	defer log.SetOutput(log.Writer())

	w := do(h, "POST", "/admin/password", pwForm(sessCSRF(t, h, a), testPass, newPass, newPass), a)
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/admin/password?ok=1" {
		t.Fatalf("status %d loc %q", w.Code, w.Header().Get("Location"))
	}
	na := cookieOf(t, w)
	if na.Value == a.Value {
		t.Fatal("session id not rotated")
	}
	if do(h, "GET", "/admin/chats", nil, a).Code != http.StatusFound {
		t.Fatal("old current session still valid")
	}
	if do(h, "GET", "/admin/chats", nil, b).Code != http.StatusFound {
		t.Fatal("other session still valid")
	}
	if r := do(h, "GET", "/admin/password?ok=1", nil, na); r.Code != 200 || !strings.Contains(r.Body.String(), "Đã đổi mật khẩu") {
		t.Fatalf("rotated session rejected: %d", r.Code)
	}
	stored, ok, _ := st.AdminPasswordHash(context.Background())
	if !ok || bcrypt.CompareHashAndPassword([]byte(stored), []byte(newPass)) != nil {
		t.Fatal("new hash not stored")
	}
	if cost, _ := bcrypt.Cost([]byte(stored)); cost != 12 {
		t.Fatalf("cost %d", cost)
	}
	if login(t, h, "owner", testPass).Code != http.StatusUnauthorized {
		t.Fatal("old password still works")
	}
	if login(t, h, "owner", newPass).Code != http.StatusFound {
		t.Fatal("new password rejected")
	}
	// audit: events only, never secrets.
	ev, _ := st.AdminAudit(context.Background())
	var names []string
	for _, e := range ev {
		names = append(names, e.Event)
	}
	if got := strings.Join(names, ","); got != "login_ok,login_ok,password_changed,login_fail,login_ok" {
		t.Fatalf("audit events %s", got)
	}
	out := logs.String()
	if !strings.Contains(out, "admin audit: event=password_changed ip=203.0.113.9") {
		t.Fatalf("missing audit log: %s", out)
	}
	for _, secret := range []string{newPass, testPass, stored, testHash} {
		if strings.Contains(out, secret) {
			t.Fatal("secret leaked into log")
		}
	}
}

func TestPasswordChangeRejections(t *testing.T) {
	h, st, _ := realH(t, Config{}, Options{PasswordAttempts: 100})
	ck := loginSession(t, h)
	csrf := sessCSRF(t, h, ck)
	for name, c := range map[string]struct{ cur, np, cf string }{
		"wrong current": {"nope", newPass, newPass},
		"mismatch":      {testPass, newPass, newPass + "x"},
		"short":         {testPass, "Ab1!xyz", "Ab1!xyz"},
		"two classes":   {testPass, "alllowercase1", "alllowercase1"},
		"too long":      {testPass, strings.Repeat("aB3", 25), strings.Repeat("aB3", 25)},
	} {
		w := do(h, "POST", "/admin/password", pwForm(csrf, c.cur, c.np, c.cf), ck)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d", name, w.Code)
		}
	}
	if _, ok, _ := st.AdminPasswordHash(context.Background()); ok {
		t.Fatal("hash stored despite rejections")
	}
	if do(h, "GET", "/admin/chats", nil, ck).Code != 200 {
		t.Fatal("session lost after failures")
	}
	// new == current is rejected even if otherwise strong
	strong := "Str0ng&Passphrase!"
	if checkNewPassword(strong, strong) == "" || checkNewPassword(strong, "other") != "" {
		t.Fatal("same-as-current rule")
	}
}

func TestPasswordRules(t *testing.T) {
	ok := []string{"Abcdefgh1234", "abcdefgh!234", "ABCDEFGH!234", "abcdefghijklmnopqrst", "pässwörd-Ünï1"}
	bad := []string{"Abcdefg1234", "abcdefghijkl", "abcdefgh1234", "ABCDEFGHIJ12", strings.Repeat("aB1", 25)}
	for _, p := range ok {
		if m := checkNewPassword(p, "other"); m != "" {
			t.Errorf("%q rejected: %s", p, m)
		}
	}
	for _, p := range bad {
		if checkNewPassword(p, "other") == "" {
			t.Errorf("%q accepted", p)
		}
	}
	if checkNewPassword(strings.Repeat("a", 73), "x") == "" {
		t.Error("73 bytes accepted")
	}
}

func TestPasswordCSRFAndAuth(t *testing.T) {
	h, _, _ := realH(t, Config{}, Options{})
	for _, p := range []string{"/admin/password", "/admin/usage", "/admin/api/usage"} {
		w := do(h, "GET", p, nil, nil)
		want := http.StatusFound
		if strings.Contains(p, "/api/") {
			want = http.StatusUnauthorized
		}
		if w.Code != want {
			t.Errorf("%s logged out: %d want %d", p, w.Code, want)
		}
	}
	if do(h, "POST", "/admin/password", pwForm("x", testPass, newPass, newPass), nil).Code != http.StatusFound {
		t.Fatal("logged-out POST must redirect to login")
	}
	ck := loginSession(t, h)
	for _, csrf := range []string{"", "wrong"} {
		if w := do(h, "POST", "/admin/password", pwForm(csrf, testPass, newPass, newPass), ck); w.Code != http.StatusForbidden {
			t.Errorf("csrf %q: %d", csrf, w.Code)
		}
	}
	if w := do(h, "PUT", "/admin/password", nil, ck); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("PUT: %d", w.Code)
	}
}

func TestPasswordRateLimit(t *testing.T) {
	h, st, _ := realH(t, Config{}, Options{PasswordAttempts: 3})
	ck := loginSession(t, h)
	csrf := sessCSRF(t, h, ck)
	for i := 0; i < 3; i++ {
		if w := do(h, "POST", "/admin/password", pwForm(csrf, "wrong", newPass, newPass), ck); w.Code != http.StatusBadRequest {
			t.Fatalf("attempt %d: %d", i, w.Code)
		}
	}
	// Even the right password is refused once the limit is reached.
	if w := do(h, "POST", "/admin/password", pwForm(csrf, testPass, newPass, newPass), ck); w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
		t.Fatalf("expected 429, got %d", w.Code)
	}
	if _, ok, _ := st.AdminPasswordHash(context.Background()); ok {
		t.Fatal("changed while limited")
	}
	// A different session from the same IP is limited by the per-IP bucket.
	other := loginSession(t, h)
	if w := do(h, "POST", "/admin/password", pwForm(sessCSRF(t, h, other), testPass, newPass, newPass), other); w.Code != http.StatusTooManyRequests {
		t.Fatalf("per-IP: %d", w.Code)
	}
}

func TestPasswordResetFlagClearsStoredHash(t *testing.T) {
	h, st, _ := realH(t, Config{}, Options{})
	ck := loginSession(t, h)
	do(h, "POST", "/admin/password", pwForm(sessCSRF(t, h, ck), testPass, newPass, newPass), ck)
	if _, ok, _ := st.AdminPasswordHash(context.Background()); !ok {
		t.Fatal("setup: no stored hash")
	}
	var logs bytes.Buffer
	log.SetOutput(&logs)
	defer log.SetOutput(log.Writer())
	h2, err := New(Config{Username: "owner", PasswordHash: testHash, ResetPassword: true}, st, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := st.AdminPasswordHash(context.Background()); ok {
		t.Fatal("stored hash not cleared")
	}
	if !strings.Contains(logs.String(), "ADMIN_PASSWORD_RESET") {
		t.Fatal("reset not logged")
	}
	if login(t, h2, "owner", testPass).Code != http.StatusFound || login(t, h2, "owner", newPass).Code != http.StatusUnauthorized {
		t.Fatal("env hash must apply again")
	}
}

func TestPasswordPageUnavailableWithoutCredStore(t *testing.T) {
	h, _ := newH(t) // fixture has no CredStore/UsageData
	ck := loginSession(t, h)
	for _, p := range []string{"/admin/password", "/admin/usage", "/admin/api/usage"} {
		if w := do(h, "GET", p, nil, ck); w.Code != http.StatusNotFound {
			t.Errorf("%s: %d", p, w.Code)
		}
	}
}

func TestUsagePageAndAPI(t *testing.T) {
	h, st, c := realH(t, Config{}, Options{})
	st.RecordMessage(context.Background(), chat.MessageEvent{Platform: "zalo", ChatID: "c1", UserID: "u1", TurnID: "t1", Model: "m-a", Rounds: 1,
		PromptTokens: 1_000_000, CompletionTokens: 100_000, Total: 1_100_000})
	st.RecordMessage(context.Background(), chat.MessageEvent{Platform: "zalo", ChatID: "c2", UserID: "u2", TurnID: "t2", Model: "m-x", Rounds: 1,
		PromptTokens: 10, CompletionTokens: 5, Total: 15})
	_ = st.Flush(context.Background())
	_ = c
	ck := loginSession(t, h)

	w := do(h, "GET", "/admin/usage", nil, ck)
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, "2.0000") || !strings.Contains(body, "price not configured") || !strings.Contains(body, "≥ 2.0000 (partial)") {
		t.Fatalf("usage page %d:\n%s", w.Code, body)
	}
	w = do(h, "GET", "/admin/api/usage?platform=zalo&from=2026-10-01&to=2026-10-31", nil, ck)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"currency":"USD"`) || !strings.Contains(w.Body.String(), `"total_tokens":1100015`) {
		t.Fatalf("usage api %d: %s", w.Code, w.Body.String())
	}
	// unpriced-only model row serialises cost as null
	if !strings.Contains(w.Body.String(), `"model":"m-x","priced":false`) || !strings.Contains(w.Body.String(), `"cost":null`) {
		t.Fatalf("null cost missing: %s", w.Body.String())
	}
}

func TestActiveAndUsernameInLayoutData(t *testing.T) {
	h, _, _ := realH(t, Config{}, Options{})
	template.Must(h.tpl.New("probe.html").Parse("{{.Active}}|{{.Username}}"))
	r := httptest.NewRequest("GET", "/admin", nil)
	for nav, want := range map[string]string{"chats": "chats|owner", "users": "users|owner", "chatstats": "chat-stats|owner", "tools": "tools|owner",
		"usage": "usage|owner", "password": "password|owner"} {
		w := httptest.NewRecorder()
		h.render(w, r, 200, "probe.html", page{Nav: nav, CSRF: "x"})
		if w.Body.String() != want {
			t.Errorf("%s: %q want %q", nav, w.Body.String(), want)
		}
	}
	w := httptest.NewRecorder()
	h.render(w, r, 200, "probe.html", page{}) // login page: not signed in
	if w.Body.String() != "|" {
		t.Errorf("login: %q", w.Body.String())
	}
}
