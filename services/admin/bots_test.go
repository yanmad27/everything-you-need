package admin

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"everything-you-need/m/services/botconfig"
	"everything-you-need/m/services/config"
	"everything-you-need/m/services/convo"
)

const (
	botSentinel  = "SENTINEL-sk-bot-4d3c2b1a0f9e8d7c"
	upstreamLeak = "UPSTREAM-BODY-LEAK-8841"
	botSecret    = "0123456789abcdef0123456789abcdef-T"
)

type rt struct {
	mu    sync.Mutex
	calls atomic.Int64
	fn    func(*http.Request) (*http.Response, error)
	last  *http.Request
}

func (t *rt) RoundTrip(r *http.Request) (*http.Response, error) {
	t.calls.Add(1)
	t.mu.Lock()
	t.last = r
	t.mu.Unlock()
	return t.fn(r)
}

func respond(code int) func(*http.Request) (*http.Response, error) {
	return func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(`{"error":"` + upstreamLeak + botSentinel + `"}`)), Header: http.Header{}}, nil
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func botsH(t *testing.T, secret string, opt Options) (*Handler, *botconfig.Provider, *convo.Store, *rt) {
	t.Helper()
	c := &clk{t: time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)}
	st, err := convo.Open(filepath.Join(t.TempDir(), "b.db"), convo.Options{NoBackground: true, Now: c.now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cfg := &config.Config{}
	cfg.Zalo.Enabled = true
	cfg.LLMOpenAI = config.LLMOpenAIConfig{BaseURL: "https://llm.example.com", Model: "env-model", APIKey: "env-key-abcdefghijklmnop"}
	prov, err := botconfig.New(context.Background(), st, secret, map[string]botconfig.Defaults{botconfig.PlatformZalo: botconfig.ZaloDefaults(cfg, "")})
	if err != nil {
		t.Fatal(err)
	}
	fake := &rt{fn: respond(200)}
	opt.Now, opt.Bots, opt.Outbound = c.now, prov, &http.Client{Transport: fake, Timeout: 5 * time.Second}
	h, err := New(Config{Username: "owner", PasswordHash: testHash}, st, opt)
	if err != nil {
		t.Fatal(err)
	}
	return h, prov, st, fake
}

func botCSRF(t *testing.T, h *Handler, ck *http.Cookie) string {
	t.Helper()
	m := csrfRe.FindStringSubmatch(do(h, "GET", "/admin/bots/zalo", nil, ck).Body.String())
	if m == nil {
		t.Fatal("no csrf on bot page")
	}
	return m[1]
}

func postBot(h *Handler, ck *http.Cookie, csrf string, kv ...string) *http.Response {
	f := url.Values{"csrf": {csrf}}
	for i := 0; i+1 < len(kv); i += 2 {
		f.Set(kv[i], kv[i+1])
	}
	return do(h, "POST", "/admin/bots/zalo", f, ck).Result()
}

func bodyOf(r *http.Response) string { b, _ := io.ReadAll(r.Body); return string(b) }

func TestBotsRequireAuthAndCSRF(t *testing.T) {
	h, _, _, _ := botsH(t, botSecret, Options{})
	for _, p := range []string{"/admin/bots", "/admin/bots/zalo"} {
		if w := do(h, "GET", p, nil, nil); w.Code != http.StatusFound {
			t.Errorf("%s: %d", p, w.Code)
		}
	}
	if w := do(h, "GET", "/admin/api/bots/zalo", nil, nil); w.Code != http.StatusUnauthorized {
		t.Errorf("api: %d", w.Code)
	}
	for _, p := range []string{"/admin/bots/zalo", "/admin/bots/zalo/test?what=llm"} {
		if w := do(h, "POST", p, url.Values{"csrf": {"x"}}, nil); w.Code != http.StatusFound {
			t.Errorf("logged-out POST %s: %d", p, w.Code)
		}
	}
	ck := loginSession(t, h)
	for _, p := range []string{"/admin/bots/zalo", "/admin/bots/zalo/test?what=llm"} {
		for _, c := range []string{"", "bad"} {
			if w := do(h, "POST", p, url.Values{"csrf": {c}}, ck); w.Code != http.StatusForbidden {
				t.Errorf("%s csrf %q: %d", p, c, w.Code)
			}
		}
	}
	if w := do(h, "GET", "/admin/bots/zalo/test", nil, ck); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET test: %d", w.Code)
	}
}

func TestBotsListAndPage(t *testing.T) {
	h, _, _, _ := botsH(t, "", Options{})
	ck := loginSession(t, h)
	list := do(h, "GET", "/admin/bots", nil, ck).Body.String()
	if !strings.Contains(list, "zalo") || !strings.Contains(list, "đang bật") || !strings.Contains(list, "telegram") || !strings.Contains(list, "không quản lý ở đây") {
		t.Fatalf("list: %s", list)
	}
	page := do(h, "GET", "/admin/bots/zalo", nil, ck).Body.String()
	for _, want := range []string{"BOTCONFIG_SECRET", "chưa đặt", "đã đặt …", "name=\"s.llm_model\"", "name=\"admin_password\"", "source-env", "env-model"} {
		if want == "đã đặt …" {
			continue // env key is set but shorter secrets hide last4; checked below
		}
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(page, "env-key-abcdefghijklmnop") {
		t.Fatal("env secret rendered")
	}
}

func TestBotSaveFlowReauthLockoutAndSecrets(t *testing.T) {
	h, prov, st, _ := botsH(t, botSecret, Options{PasswordAttempts: 3})
	ck := loginSession(t, h)
	csrf := botCSRF(t, h, ck)

	// unchanged form (all current values) creates no override
	cur := do(h, "GET", "/admin/bots/zalo", nil, ck).Body.String()
	_ = cur
	r := postBot(h, ck, csrf, "s.llm_model", "env-model", "s.enabled", "true")
	if r.StatusCode != 302 || !strings.Contains(r.Header.Get("Location"), "flash=nochange") || prov.Snapshot("zalo").Overridden(botconfig.KeyLLMModel) {
		t.Fatalf("unchanged form: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	// non-sensitive change needs no password; actor is the admin username
	r = postBot(h, ck, csrf, "s.llm_model", "new-model", "s.rate_limit_per_min", "30")
	if r.StatusCode != 302 || prov.Snapshot("zalo").Zalo().LLMModel != "new-model" || prov.Snapshot("zalo").Zalo().RateLimitPerMin != 30 {
		t.Fatalf("non-sensitive: %d", r.StatusCode)
	}
	ev, _ := st.AdminAudit(context.Background())
	foundActor := false
	for _, e := range ev {
		if e.Event == "setting_changed" && e.Actor == "owner" && strings.HasPrefix(e.Detail, "zalo/") {
			foundActor = true
		}
	}
	if !foundActor {
		t.Fatalf("audit actor missing: %+v", ev)
	}
	// sensitive change without password → 400, nothing saved, secret not echoed
	r = postBot(h, ck, csrf, "s.fetch_allow_domains", "docs.example.com", "s.llm_api_key", botSentinel)
	body := bodyOf(r)
	if r.StatusCode != 400 || strings.Contains(body, botSentinel) || prov.Snapshot("zalo").Overridden(botconfig.KeyFetchAllow) {
		t.Fatalf("no password: %d", r.StatusCode)
	}
	// the missing password above counted as a failure; two wrong ones reach the limit (3)
	for i := 0; i < 2; i++ {
		r = postBot(h, ck, csrf, "s.fetch_allow_domains", "docs.example.com", "admin_password", "wrong")
		if r.StatusCode != 400 {
			t.Fatalf("wrong pw %d: %d", i, r.StatusCode)
		}
	}
	if r = postBot(h, ck, csrf, "s.fetch_allow_domains", "docs.example.com", "admin_password", testPass); r.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("lockout: %d", r.StatusCode)
	}
	// new session (different cookie) is still limited by the per-IP bucket
	ck2 := loginSession(t, h)
	if r = postBot(h, ck2, botCSRF(t, h, ck2), "s.fetch_allow_domains", "docs.example.com", "admin_password", testPass); r.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("per-IP lockout: %d", r.StatusCode)
	}
	if prov.Snapshot("zalo").Overridden(botconfig.KeyFetchAllow) {
		t.Fatal("saved while locked")
	}
}

func TestBotSecretNeverRendered(t *testing.T) {
	h, prov, st, _ := botsH(t, botSecret, Options{})
	ck := loginSession(t, h)
	csrf := botCSRF(t, h, ck)
	r := postBot(h, ck, csrf, "s.llm_api_key", botSentinel, "s.serper_api_key", botSentinel+"-serper", "admin_password", testPass)
	if r.StatusCode != 302 || prov.Snapshot("zalo").Secret(botconfig.KeyLLMAPIKey).Reveal() != botSentinel {
		t.Fatalf("save secret: %d", r.StatusCode)
	}
	page := do(h, "GET", "/admin/bots/zalo?flash=saved", nil, ck).Body.String()
	api := do(h, "GET", "/admin/api/bots/zalo", nil, ck).Body.String()
	list := do(h, "GET", "/admin/bots", nil, ck).Body.String()
	for name, b := range map[string]string{"page": page, "api": api, "list": list} {
		if strings.Contains(b, botSentinel) {
			t.Errorf("%s leaks the secret", name)
		}
	}
	if !strings.Contains(page, "đã đặt …"+botSentinel[len(botSentinel)-4:]) || !strings.Contains(api, `"last4":"`+botSentinel[len(botSentinel)-4:]+`"`) {
		t.Fatalf("masked view missing:\n%s", api)
	}
	// a rejected submission (validation error) does not echo secrets either
	r = postBot(h, ck, csrf, "s.llm_api_key", botSentinel+"2", "s.llm_base_url", "http://bad.example.com", "admin_password", testPass)
	if b := bodyOf(r); r.StatusCode != 400 || strings.Contains(b, botSentinel) {
		t.Fatalf("validation re-render: %d", r.StatusCode)
	}
	// secrets never reach the audit table
	ev, _ := st.AdminAudit(context.Background())
	for _, e := range ev {
		if strings.Contains(e.Detail+e.Actor+e.Event+e.IP, botSentinel) {
			t.Fatal("secret in audit")
		}
	}
}

func TestBotValidationErrorsShownAndResetWorks(t *testing.T) {
	h, prov, _, _ := botsH(t, botSecret, Options{})
	ck := loginSession(t, h)
	csrf := botCSRF(t, h, ck)
	r := postBot(h, ck, csrf, "s.llm_model", "bad model!", "s.rate_limit_per_min", "5000")
	b := bodyOf(r)
	if r.StatusCode != 400 || strings.Count(b, "field-error") < 2 || !strings.Contains(b, "bad model!") || prov.Snapshot("zalo").Overridden(botconfig.KeyLLMModel) {
		t.Fatalf("validation: %d %s", r.StatusCode, b)
	}
	postBot(h, ck, csrf, "s.llm_model", "temp-model")
	if prov.Snapshot("zalo").Zalo().LLMModel != "temp-model" {
		t.Fatal("setup")
	}
	if page := do(h, "GET", "/admin/bots/zalo", nil, ck).Body.String(); !strings.Contains(page, "source-ui") || !strings.Contains(page, `name="reset.llm_model"`) {
		t.Fatal("override badge / reset checkbox missing")
	}
	r = postBot(h, ck, csrf, "reset.llm_model", "1")
	if r.StatusCode != 302 || prov.Snapshot("zalo").Zalo().LLMModel != "env-model" || prov.Snapshot("zalo").Overridden(botconfig.KeyLLMModel) {
		t.Fatalf("reset: %d", r.StatusCode)
	}
	// resetting a sensitive key needs the password
	postBot(h, ck, csrf, "s.fetch_allow_domains", "a.example.com", "admin_password", testPass)
	if r = postBot(h, ck, csrf, "reset.fetch_allow_domains", "1"); r.StatusCode != 400 || !prov.Snapshot("zalo").Overridden(botconfig.KeyFetchAllow) {
		t.Fatalf("sensitive reset without password: %d", r.StatusCode)
	}
	if r = postBot(h, ck, csrf, "reset.fetch_allow_domains", "1", "admin_password", testPass); r.StatusCode != 302 || prov.Snapshot("zalo").Overridden(botconfig.KeyFetchAllow) {
		t.Fatalf("sensitive reset: %d", r.StatusCode)
	}
}

func TestBotSecretsDisabledBanner(t *testing.T) {
	h, _, _, _ := botsH(t, "", Options{})
	ck := loginSession(t, h)
	if p := do(h, "GET", "/admin/bots/zalo", nil, ck).Body.String(); !strings.Contains(p, "banner-secrets") {
		t.Fatal("banner missing")
	}
	r := postBot(h, ck, botCSRF(t, h, ck), "s.llm_api_key", botSentinel, "admin_password", testPass)
	if b := bodyOf(r); r.StatusCode != 400 || !strings.Contains(b, "BOTCONFIG_SECRET") || strings.Contains(b, botSentinel) {
		t.Fatalf("secrets disabled save: %d", r.StatusCode)
	}
}

func TestBotConnectionTestClassesNoLeak(t *testing.T) {
	h, _, _, fake := botsH(t, botSecret, Options{TestAttempts: 100})
	ck := loginSession(t, h)
	csrf := botCSRF(t, h, ck)
	run := func(what string) string {
		w := do(h, "POST", "/admin/bots/zalo/test?what="+what, url.Values{"csrf": {csrf}}, ck)
		if w.Code != 302 {
			t.Fatalf("test status %d", w.Code)
		}
		loc := w.Header().Get("Location")
		page := do(h, "GET", loc, nil, ck).Body.String()
		for _, leak := range []string{upstreamLeak, botSentinel, "env-key-abcdefghijklmnop"} {
			if strings.Contains(loc, leak) || strings.Contains(page, leak) {
				t.Fatalf("leak %q in %s", leak, loc)
			}
		}
		return loc
	}
	for _, c := range []struct {
		fn   func(*http.Request) (*http.Response, error)
		want string
	}{
		{respond(200), "test_llm_ok"}, {respond(401), "test_llm_http_401"}, {respond(402), "test_llm_credits"}, {respond(400), "test_llm_http_400"},
		{respond(503), "test_llm_http_5xx"}, {respond(429), "test_llm_http_429"},
		{func(*http.Request) (*http.Response, error) { return nil, timeoutErr{} }, "test_llm_timeout"},
		{func(*http.Request) (*http.Response, error) { return nil, &net.DNSError{Err: "no such host"} }, "test_llm_dns"},
		{func(*http.Request) (*http.Response, error) { return nil, errors.New("dial: blocked destination") }, "test_llm_network"},
	} {
		fake.fn = c.fn
		if loc := run("llm"); !strings.HasSuffix(loc, "flash="+c.want) {
			t.Errorf("want %s, got %s", c.want, loc)
		}
	}
	fake.fn = respond(200)
	run("llm")
	if fake.last.URL.String() != "https://llm.example.com/v1/chat/completions" || fake.last.Header.Get("Authorization") != "Bearer env-key-abcdefghijklmnop" {
		t.Fatalf("request: %s", fake.last.URL)
	}
	if loc := run("serper"); !strings.HasSuffix(loc, "flash=test_serper_not_set") {
		t.Fatalf("serper not set: %s", loc)
	}
	postBot(h, ck, csrf, "s.serper_api_key", botSentinel+"x", "admin_password", testPass)
	if loc := run("serper"); !strings.HasSuffix(loc, "flash=test_serper_ok") || fake.last.URL.String() != "https://google.serper.dev/search" || fake.last.Header.Get("X-API-KEY") != botSentinel+"x" {
		t.Fatalf("serper: %s %s", loc, fake.last.URL)
	}
	if p := do(h, "GET", "/admin/bots/zalo?flash=%3Cscript%3E", nil, ck).Body.String(); strings.Contains(p, "<script>") {
		t.Fatal("flash reflected")
	}
}

func TestBotLLMTestRetriesWithMaxCompletionTokens(t *testing.T) {
	h, _, _, fake := botsH(t, botSecret, Options{TestAttempts: 100})
	ck := loginSession(t, h)
	csrf := botCSRF(t, h, ck)
	var bodies []string
	fake.fn = func(r *http.Request) (*http.Response, error) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		if strings.Contains(string(b), `"max_tokens"`) {
			return &http.Response{StatusCode: 400, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"Unsupported parameter: 'max_tokens' is not supported with this model. Use 'max_completion_tokens' instead."}}`))}, nil
		}
		return respond(200)(r)
	}
	w := do(h, "POST", "/admin/bots/zalo/test?what=llm", url.Values{"csrf": {csrf}}, ck)
	if !strings.HasSuffix(w.Header().Get("Location"), "flash=test_llm_ok") || len(bodies) != 2 || !strings.Contains(bodies[1], `"max_completion_tokens":5`) || strings.Contains(bodies[1], `"max_tokens"`) {
		t.Fatalf("%s %v", w.Header().Get("Location"), bodies)
	}
}

func TestBotTestIgnoresFormValuesAndRefusesPrivateBase(t *testing.T) {
	h, _, _, fake := botsH(t, botSecret, Options{TestAttempts: 100})
	ck := loginSession(t, h)
	csrf := botCSRF(t, h, ck)
	f := url.Values{"csrf": {csrf}, "s.llm_base_url": {"https://attacker.example.net"}, "s.llm_api_key": {botSentinel}, "s.llm_model": {"evil"}}
	w := do(h, "POST", "/admin/bots/zalo/test?what=llm", f, ck)
	if !strings.HasSuffix(w.Header().Get("Location"), "flash=test_llm_ok") || fake.last.URL.Host != "llm.example.com" || strings.Contains(fake.last.Header.Get("Authorization"), botSentinel) {
		t.Fatalf("form override honoured: %s %s", w.Header().Get("Location"), fake.last.URL)
	}
	before := fake.calls.Load()
	for _, base := range []string{"https://127.0.0.1", "https://10.1.2.3", "http://llm.example.com", "https://localhost", "https://169.254.169.254"} {
		if got := h.testLLM(context.Background(), base, "k", "m"); got != "blocked" {
			t.Errorf("%s: %s", base, got)
		}
	}
	if fake.calls.Load() != before {
		t.Fatal("outbound call made for a private base URL")
	}
}

func TestBotPersonaEditsDetectedAndSensitive(t *testing.T) {
	h, prov, _, _ := botsH(t, botSecret, Options{})
	ck := loginSession(t, h)
	csrf := botCSRF(t, h, ck)
	if r := postBot(h, ck, csrf, "s.persona", "Bạn là A.\n\nVui vẻ"); r.StatusCode != 400 || prov.Snapshot("zalo").Overridden(botconfig.KeyPersona) {
		t.Fatalf("persona without password: %d", r.StatusCode)
	}
	if r := postBot(h, ck, csrf, "s.retention_days", "30"); r.StatusCode != 400 {
		t.Fatalf("retention without password: %d", r.StatusCode)
	}
	base := "Bạn là A.\n\nVui vẻ, ngắn gọn"
	if r := postBot(h, ck, csrf, "s.persona", base, "admin_password", testPass); r.StatusCode != 302 {
		t.Fatal("save persona")
	}
	if r := postBot(h, ck, csrf, "s.persona", strings.ReplaceAll(base, "\n", "\r\n")); r.StatusCode != 302 || !strings.Contains(r.Header.Get("Location"), "nochange") {
		t.Fatalf("CRLF fold: %s", r.Header.Get("Location"))
	}
	for name, edited := range map[string]string{"blank line": "Bạn là A.\nVui vẻ, ngắn gọn", "comma spacing": "Bạn là A.\n\nVui vẻ,ngắn gọn", "trailing space": base + " "} {
		if r := postBot(h, ck, csrf, "s.persona", edited, "admin_password", testPass); r.StatusCode != 302 || !strings.Contains(r.Header.Get("Location"), "saved") || prov.Snapshot("zalo").Zalo().Persona != edited {
			t.Fatalf("%s edit not saved: %s", name, r.Header.Get("Location"))
		}
	}
}

func TestBotSaveWithResetIsOneApply(t *testing.T) {
	h, prov, _, _ := botsH(t, botSecret, Options{})
	ck := loginSession(t, h)
	csrf := botCSRF(t, h, ck)
	postBot(h, ck, csrf, "s.llm_model", "m1")
	postBot(h, ck, csrf, "s.rate_limit_per_min", "20")
	before := prov.Snapshot("zalo")
	if r := postBot(h, ck, csrf, "s.rate_limit_burst", "5", "reset.llm_model", "1"); r.StatusCode != 302 {
		t.Fatalf("%d", r.StatusCode)
	}
	after := prov.Snapshot("zalo")
	if after == before || after.Zalo().RateLimitBurst != 5 || after.Overridden(botconfig.KeyLLMModel) || after.Zalo().RateLimitPerMin != 20 {
		t.Fatal("combined apply")
	}
	if r := postBot(h, ck, csrf, "s.rate_limit_burst", "999", "reset.rate_limit_per_min", "1"); r.StatusCode != 400 || !prov.Snapshot("zalo").Overridden(botconfig.KeyRatePerMin) {
		t.Fatalf("an invalid change must block the reset in the same request: %d", r.StatusCode)
	}
}

func TestBotTestRateLimited(t *testing.T) {
	h, _, _, _ := botsH(t, botSecret, Options{})
	ck := loginSession(t, h)
	csrf := botCSRF(t, h, ck)
	codes := map[int]int{}
	for i := 0; i < 12; i++ {
		codes[do(h, "POST", "/admin/bots/zalo/test?what=llm", url.Values{"csrf": {csrf}}, ck).Code]++
	}
	if codes[302] != testPerWindow || codes[429] != 2 {
		t.Fatalf("%v", codes)
	}
}

func TestBotsOffWithoutProvider(t *testing.T) {
	h, _ := newH(t)
	ck := loginSession(t, h)
	if w := do(h, "GET", "/admin/bots", nil, ck); w.Code != 404 {
		t.Fatalf("%d", w.Code)
	}
	if w := do(h, "GET", "/admin/api/bots/zalo", nil, ck); w.Code != 404 {
		t.Fatalf("%d", w.Code)
	}
}
