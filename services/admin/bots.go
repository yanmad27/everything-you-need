package admin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"everything-you-need/m/services/botconfig"
	"everything-you-need/m/services/netguard"
)

const (
	platformZalo   = botconfig.PlatformZalo
	testWindow     = 15 * time.Minute
	testPerWindow  = 10
	serperSearchEP = "https://google.serper.dev/search"
)

// BotsView is the Data of bots.html: one row per known bot.
type BotsView struct {
	Bots []BotRow `json:"bots"`
}

// BotRow is one bot in the list.
type BotRow struct {
	Platform  string   `json:"platform"`
	Managed   bool     `json:"managed"`
	Enabled   bool     `json:"enabled"`   // managed bots only
	Status    string   `json:"status"`    // "enabled" | "paused" | "unmanaged"
	Overrides int      `json:"overrides"` // settings overridden from the UI
	Problems  []string `json:"problems"`  // keys whose stored value is unusable
	Note      string   `json:"note"`      // "không quản lý ở đây" for unmanaged bots
	URL       string   `json:"url"`       // settings page ("" when unmanaged)
}

// BotSettingRow is one setting on bot.html (SettingView + form helpers).
// Secrets never carry a value: only MaskedText.
type BotSettingRow struct {
	botconfig.SettingView
	Source      string `json:"source"`       // "env" | "ui"
	InputName   string `json:"input_name"`   // form field: s.<key>
	ResetName   string `json:"reset_name"`   // form checkbox: reset.<key>
	Input       string `json:"input"`        // current value as form text (non-secret)
	DefaultText string `json:"default_text"` // env/config default as text (non-secret)
	MaskedText  string `json:"masked_text"`  // secrets: "đã đặt …last4" | "chưa đặt"
	Error       string `json:"error"`        // per-field validation message
	UpdatedTime string `json:"updated_time"` // formatted updated_at ("" for env)
}

// Flash is the one-line result shown after a redirect (?flash=<code>).
type Flash struct {
	Code string `json:"code"`
	Kind string `json:"kind"` // "ok" | "err"
	Text string `json:"text"`
}

// BotView is the Data of bot.html and the body of GET /admin/api/bots/zalo.
type BotView struct {
	Platform        string          `json:"platform"`
	Enabled         bool            `json:"enabled"`
	SecretsDisabled bool            `json:"secrets_disabled"` // BOTCONFIG_SECRET missing/short
	Problems        []string        `json:"problems"`
	Settings        []BotSettingRow `json:"settings"`
	Flash           *Flash          `json:"flash"`
	FormError       string          `json:"form_error"`     // general error (e.g. re-auth failed)
	PasswordField   string          `json:"password_field"` // "admin_password"
	PostURL         string          `json:"post_url"`
	TestURL         string          `json:"test_url"`
}

var flashTexts = map[string]Flash{
	"saved":          {Kind: "ok", Text: "Đã lưu cài đặt."},
	"nochange":       {Kind: "ok", Text: "Không có thay đổi."},
	"test_llm_ok":    {Kind: "ok", Text: "Kiểm tra LLM: OK."},
	"test_serper_ok": {Kind: "ok", Text: "Kiểm tra Serper: OK."},
}

var testErrClasses = map[string]bool{"http_400": true, "http_401": true, "http_403": true, "http_404": true, "http_429": true, "http_5xx": true,
	"http_other": true, "credits": true, "timeout": true, "dns": true, "blocked": true, "network": true, "invalid": true, "not_set": true, "rate_limited": true}

func flashFor(code string) *Flash {
	if f, ok := flashTexts[code]; ok {
		f.Code = code
		return &f
	}
	for _, w := range []string{"llm", "serper"} {
		if c := strings.TrimPrefix(code, "test_"+w+"_"); c != code && testErrClasses[c] {
			return &Flash{Code: code, Kind: "err", Text: "Kiểm tra " + map[string]string{"llm": "LLM", "serper": "Serper"}[w] + " thất bại: " + c}
		}
	}
	return nil
}

func secretMask(m botconfig.Masked) string {
	if !m.Set {
		return "chưa đặt"
	}
	if m.Last4 == "" {
		return "đã đặt"
	}
	return "đã đặt …" + m.Last4
}

// botView builds the page model from one snapshot. inputs/errs overlay a
// rejected submission (non-secret values only).
func (h *Handler) botView(inputs map[string]string, errs map[string]string) BotView {
	sn := h.botsP.Snapshot(platformZalo)
	defs := h.botsP.Defaults(platformZalo)
	v := BotView{Platform: platformZalo, Enabled: sn.Bool(botconfig.KeyEnabled), SecretsDisabled: h.botsP.SecretsDisabled(),
		Problems: append([]string{}, sn.Problems...), PasswordField: "admin_password", PostURL: "/admin/bots/zalo", TestURL: "/admin/bots/zalo/test",
		Settings: []BotSettingRow{}}
	for _, sv := range sn.View(defs) {
		r := BotSettingRow{SettingView: sv, Source: "env", InputName: "s." + sv.Key, ResetName: "reset." + sv.Key, Error: errs[sv.Key]}
		if sv.Overridden {
			r.Source = "ui"
			r.UpdatedTime = ftimeMS(sv.UpdatedAt)
		}
		if sv.Secret {
			r.MaskedText = secretMask(sv.Masked)
		} else {
			r.Input, r.DefaultText = botconfig.FormatValue(sv.Value), botconfig.FormatValue(sv.Default)
			if in, ok := inputs[sv.Key]; ok {
				r.Input = in
			}
		}
		v.Settings = append(v.Settings, r)
	}
	return v
}

func ftimeMS(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).In(ict).Format("02/01/2006 15:04")
}

// bots serves /admin/bots*, /admin/api/bots/* (the caller has authenticated s).
func (h *Handler) bots(w http.ResponseWriter, r *http.Request, path string, s *session) {
	if h.botsP == nil {
		if strings.HasPrefix(path, "/admin/api/") {
			writeJSONError(w, http.StatusNotFound, "not found")
			return
		}
		http.NotFound(w, r)
		return
	}
	switch path {
	case "/admin/bots", "/admin/bots/zalo", "/admin/api/bots/zalo":
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !(path == "/admin/bots/zalo" && r.Method == http.MethodPost) {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.Method == http.MethodPost {
			h.botSave(w, r, s)
			return
		}
		switch path {
		case "/admin/bots":
			h.render(w, r, http.StatusOK, "bots.html", page{Title: "Bots", Nav: "bots", Active: "bots", CSRF: s.csrf, Q: r.URL.Query(), Data: h.botsList()})
		case "/admin/bots/zalo":
			v := h.botView(nil, nil)
			v.Flash = flashFor(r.URL.Query().Get("flash"))
			h.render(w, r, http.StatusOK, "bot.html", page{Title: "Bot Zalo", Nav: "bots", Active: "bots", CSRF: s.csrf, Q: r.URL.Query(), Data: v})
		default:
			writeJSON(w, h.botView(nil, nil))
		}
	case "/admin/bots/zalo/test":
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		h.botTest(w, r, s)
	default:
		if strings.HasPrefix(path, "/admin/api/") {
			writeJSONError(w, http.StatusNotFound, "not found")
			return
		}
		http.NotFound(w, r)
	}
}

func (h *Handler) botsList() BotsView {
	var v BotsView
	for _, b := range botconfig.Bots() {
		row := BotRow{Platform: b.Platform, Managed: b.Managed, Status: "unmanaged", Problems: []string{}}
		if !b.Managed {
			row.Note = "không quản lý ở đây"
		} else if sn := h.botsP.Snapshot(b.Platform); sn != nil {
			row.Enabled = sn.Bool(botconfig.KeyEnabled)
			row.Status = "paused"
			if row.Enabled {
				row.Status = "enabled"
			}
			for _, sp := range botconfig.Specs(b.Platform) {
				if sn.Overridden(sp.Key) {
					row.Overrides++
				}
			}
			row.Problems = append(row.Problems, sn.Problems...)
			row.URL = "/admin/bots/" + b.Platform
		}
		v.Bots = append(v.Bots, row)
	}
	return v
}

// reauth verifies the admin password under the credential lock with the same
// lockout as a password change. It returns ok, or an HTTP status and message.
func (h *Handler) reauth(r *http.Request, s *session, pw string) (ok bool, status int, msg string) {
	c, _ := r.Cookie(CookieName)
	sk := ""
	if c != nil {
		k := sha256.Sum256([]byte(c.Value))
		sk = string(k[:])
	}
	ip := clientIP(r)
	h.credMu.Lock()
	defer h.credMu.Unlock()
	if h.pwSess.full(sk) || h.pwIP.full(ip) {
		return false, http.StatusTooManyRequests, "too many attempts, try again later"
	}
	hash, err := h.activeHash(r.Context())
	if err != nil {
		return false, http.StatusInternalServerError, "internal error"
	}
	if bcrypt.CompareHashAndPassword(hash, []byte(pw)) != nil {
		h.pwSess.add(sk)
		h.pwIP.add(ip)
		h.audit(r, "settings_reauth_fail")
		return false, http.StatusBadRequest, "Mật khẩu quản trị không đúng."
	}
	return true, 0, ""
}

func (h *Handler) botSave(w http.ResponseWriter, r *http.Request, s *session) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil || subtle.ConstantTimeCompare([]byte(r.PostForm.Get("csrf")), []byte(s.csrf)) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	sn := h.botsP.Snapshot(platformZalo)
	changes := map[string]string{}
	inputs := map[string]string{}
	var resets []string
	needAuth := false
	cur := map[string]string{} // effective text of each non-secret setting
	for _, sv := range sn.View(h.botsP.Defaults(platformZalo)) {
		cur[sv.Key] = botconfig.FormatValue(sv.Value)
	}
	for _, sp := range botconfig.Specs(platformZalo) {
		if r.PostForm.Get("reset."+sp.Key) != "" {
			if sn.Overridden(sp.Key) {
				resets = append(resets, sp.Key)
				needAuth = needAuth || sp.Sensitive || sp.Secret
			}
			continue
		}
		vals, present := r.PostForm["s."+sp.Key]
		if !present || len(vals) == 0 {
			continue
		}
		in := vals[0]
		if sp.Secret {
			if in == "" {
				continue // empty = unchanged
			}
			changes[sp.Key] = in
			needAuth = true
			continue
		}
		inputs[sp.Key] = in
		if normalizeForm(in) == normalizeForm(cur[sp.Key]) {
			continue
		}
		changes[sp.Key] = in
		needAuth = needAuth || sp.Sensitive
	}
	if len(changes) == 0 && len(resets) == 0 {
		http.Redirect(w, r, "/admin/bots/zalo?flash=nochange", http.StatusFound)
		return
	}
	rerender := func(status int, formErr string, errs map[string]string) {
		v := h.botView(inputs, errs)
		v.FormError = formErr
		h.render(w, r, status, "bot.html", page{Title: "Bot Zalo", Nav: "bots", Active: "bots", CSRF: s.csrf, Q: r.URL.Query(), Data: v})
	}
	if needAuth {
		if ok, status, msg := h.reauth(r, s, r.PostForm.Get("admin_password")); !ok {
			if status == http.StatusTooManyRequests || status == http.StatusInternalServerError {
				if status == http.StatusTooManyRequests {
					w.Header().Set("Retry-After", "900")
				}
				http.Error(w, msg, status)
				return
			}
			rerender(status, msg, nil)
			return
		}
	}
	actor := h.username
	if len(changes) > 0 {
		if err := h.botsP.Save(r.Context(), platformZalo, changes, actor); err != nil {
			var ve *botconfig.ValidationError
			switch {
			case errors.As(err, &ve):
				rerender(http.StatusBadRequest, "Có giá trị không hợp lệ.", ve.Problems)
			case errors.Is(err, botconfig.ErrSecretsDisabled):
				rerender(http.StatusBadRequest, "Chưa đặt BOTCONFIG_SECRET (≥32 ký tự) nên không thể lưu khoá bí mật.", nil)
			default:
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
			return
		}
	}
	for _, k := range resets {
		if err := h.botsP.Reset(r.Context(), platformZalo, k, actor); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}
	http.Redirect(w, r, "/admin/bots/zalo?flash=saved", http.StatusFound)
}

// normalizeForm makes "a, b" / "a,b" / CRLF variants of the same list compare equal.
func normalizeForm(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
	if strings.ContainsAny(s, ",\n") {
		parts := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '\n' })
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		return strings.Join(parts, ",")
	}
	return strings.TrimSpace(s)
}

// ---- connection tests ----

func (h *Handler) botTest(w http.ResponseWriter, r *http.Request, s *session) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes*4)
	if err := r.ParseForm(); err != nil || subtle.ConstantTimeCompare([]byte(r.PostForm.Get("csrf")), []byte(s.csrf)) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	what := r.URL.Query().Get("what")
	if what != "llm" && what != "serper" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	c, _ := r.Cookie(CookieName)
	sk := ""
	if c != nil {
		k := sha256.Sum256([]byte(c.Value))
		sk = string(k[:])
	}
	if !h.testSess.allow(sk) || !h.testIP.allow(clientIP(r)) {
		w.Header().Set("Retry-After", "900")
		http.Error(w, "too many attempts, try again later", http.StatusTooManyRequests)
		return
	}
	sn := h.botsP.Snapshot(platformZalo)
	pick := func(key string) string { // just-entered value wins, else the snapshot's
		if v := strings.TrimSpace(r.PostForm.Get("s." + key)); v != "" {
			return v
		}
		return ""
	}
	var class string
	switch what {
	case "llm":
		base, key, model := pick(botconfig.KeyLLMBaseURL), pick(botconfig.KeyLLMAPIKey), pick(botconfig.KeyLLMModel)
		z := sn.Zalo()
		if base == "" {
			base = z.LLMBaseURL
		}
		if key == "" {
			key = z.LLMAPIKey.Reveal()
		}
		if model == "" {
			model = z.LLMModel
		}
		class = h.testLLM(r.Context(), base, key, model)
	default:
		key := pick(botconfig.KeySerperAPIKey)
		if key == "" {
			key = sn.Zalo().SerperAPIKey.Reveal()
		}
		class = h.testSerper(r.Context(), key)
	}
	h.audit(r, "settings_test_"+what)
	code := "test_" + what + "_" + class
	if class == "ok" {
		code = "test_" + what + "_ok"
	}
	http.Redirect(w, r, "/admin/bots/zalo?flash="+url.QueryEscape(code), http.StatusFound)
}

func (h *Handler) testLLM(ctx context.Context, base, key, model string) string {
	if base == "" || key == "" {
		return "not_set"
	}
	nb, err := botconfig.ValidateBaseURL(base)
	if err != nil {
		return "blocked" // refused before any request (not https / private / IP host)
	}
	if model == "" {
		return "not_set"
	}
	ep := strings.TrimSuffix(strings.TrimRight(nb, "/"), "/v1") + "/v1/chat/completions"
	body, _ := json.Marshal(map[string]any{"model": model, "messages": []map[string]string{{"role": "user", "content": "ping"}}, "max_tokens": 5})
	return h.doTest(ctx, ep, body, map[string]string{"Authorization": "Bearer " + key})
}

func (h *Handler) testSerper(ctx context.Context, key string) string {
	if key == "" {
		return "not_set"
	}
	body, _ := json.Marshal(map[string]any{"q": "test", "num": 1})
	return h.doTest(ctx, serperSearchEP, body, map[string]string{"X-API-KEY": key})
}

// doTest sends one request through the SSRF-guarded client and returns "ok" or
// an error class. The upstream body is drained and discarded; nothing from it
// (or the key) reaches the result.
func (h *Handler) doTest(ctx context.Context, endpoint string, body []byte, hdr map[string]string) string {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "invalid"
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := h.outbound.Do(req)
	if err != nil {
		return classifyNetErr(err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	switch c := resp.StatusCode; {
	case c >= 200 && c < 300:
		return "ok"
	case c == 402:
		return "credits"
	case c == 400, c == 401, c == 403, c == 404, c == 429:
		return fmt.Sprintf("http_%d", c)
	case c >= 500:
		return "http_5xx"
	}
	return "http_other"
}

func classifyNetErr(err error) string {
	var dns *net.DNSError
	switch {
	case errors.Is(err, netguard.ErrBlocked):
		return "blocked"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.As(err, &dns):
		return "dns"
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return "timeout"
	}
	return "network"
}
