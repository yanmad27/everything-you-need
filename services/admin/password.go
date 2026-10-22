package admin

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"log"
	"net/http"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

const (
	newPassCost    = 12
	minNewPassLen  = 12
	maxNewPassByte = 72 // bcrypt ignores everything past 72 bytes
	longPassLen    = 20
)

// PasswordView is the Data of password.html.
type PasswordView struct {
	Saved  bool   // redirected back after a successful change
	Error  string // user-facing message for a failed attempt ("" when none)
	MinLen int    // minimum length in characters
	MaxLen int    // maximum length in bytes
	Hint   string // short statement of the strength rule
}

// activeHash is the stored (UI-changed) hash when present, else the env hash.
func (h *Handler) activeHash(ctx context.Context) ([]byte, error) {
	if h.creds != nil {
		stored, ok, err := h.creds.AdminPasswordHash(ctx)
		if err != nil {
			log.Printf("admin: reading stored credentials failed")
			return nil, err
		}
		if ok {
			return []byte(stored), nil
		}
	}
	return h.hash, nil
}

// audit records an event (name and client IP only) in the table and the log.
func (h *Handler) audit(r *http.Request, event string) {
	ip := clientIP(r)
	log.Printf("admin audit: event=%s ip=%s", event, ip)
	if h.creds != nil {
		if err := h.creds.RecordAdminAudit(r.Context(), event, ip); err != nil {
			log.Printf("admin audit: write failed event=%s", event)
		}
	}
}

// checkNewPassword returns a user-facing problem with np, or "".
func checkNewPassword(np, current string) string {
	n := utf8.RuneCountInString(np)
	switch {
	case n < minNewPassLen:
		return "Mật khẩu mới phải có ít nhất 12 ký tự."
	case len(np) > maxNewPassByte:
		return "Mật khẩu mới tối đa 72 byte."
	case np == current:
		return "Mật khẩu mới phải khác mật khẩu hiện tại."
	}
	if n >= longPassLen {
		return ""
	}
	var lower, upper, digit, sym bool
	for _, c := range np {
		switch {
		case unicode.IsLower(c):
			lower = true
		case unicode.IsUpper(c):
			upper = true
		case unicode.IsDigit(c):
			digit = true
		default:
			sym = true
		}
	}
	k := 0
	for _, b := range []bool{lower, upper, digit, sym} {
		if b {
			k++
		}
	}
	if k < 3 {
		return "Mật khẩu mới cần ít nhất 3 trong 4 loại (chữ thường, chữ hoa, số, ký hiệu) hoặc dài từ 20 ký tự."
	}
	return ""
}

func (h *Handler) passwordPage(w http.ResponseWriter, r *http.Request, s *session, status int, saved bool, msg string) {
	h.render(w, r, status, "password.html", page{Title: "Đổi mật khẩu", Nav: "password", Active: "password", CSRF: s.csrf, Q: r.URL.Query(),
		Data: PasswordView{Saved: saved, Error: msg, MinLen: minNewPassLen, MaxLen: maxNewPassByte,
			Hint: "≥12 ký tự và ≥3 loại ký tự (thường/hoa/số/ký hiệu), hoặc ≥20 ký tự"}})
}

// password serves GET/POST /admin/password (the caller has authenticated s).
func (h *Handler) password(w http.ResponseWriter, r *http.Request, s *session) {
	if h.creds == nil {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		h.passwordPage(w, r, s, http.StatusOK, r.URL.Query().Get("ok") == "1", "")
	case http.MethodPost:
		r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
		if err := r.ParseForm(); err != nil || subtle.ConstantTimeCompare([]byte(r.PostForm.Get("csrf")), []byte(s.csrf)) != 1 {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		c, _ := r.Cookie(CookieName)
		sk := ""
		if c != nil {
			k := sha256.Sum256([]byte(c.Value))
			sk = string(k[:])
		}
		ip := clientIP(r)
		if h.pwSess.full(sk) || h.pwIP.full(ip) {
			w.Header().Set("Retry-After", "900")
			http.Error(w, "too many attempts, try again later", http.StatusTooManyRequests)
			return
		}
		cur, np, confirm := r.PostForm.Get("current"), r.PostForm.Get("new"), r.PostForm.Get("confirm")
		hash, err := h.activeHash(r.Context())
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		fail := func(msg string) {
			h.pwSess.add(sk)
			h.pwIP.add(ip)
			h.audit(r, "password_change_fail")
			h.passwordPage(w, r, s, http.StatusBadRequest, false, msg)
		}
		if bcrypt.CompareHashAndPassword(hash, []byte(cur)) != nil {
			fail("Mật khẩu hiện tại không đúng.")
			return
		}
		if np != confirm {
			fail("Mật khẩu xác nhận không khớp.")
			return
		}
		if msg := checkNewPassword(np, cur); msg != "" {
			fail(msg)
			return
		}
		nh, err := bcrypt.GenerateFromPassword([]byte(np), newPassCost)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if err := h.creds.SetAdminPasswordHash(r.Context(), h.username, string(nh)); err != nil {
			log.Printf("admin: storing new password failed")
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		h.pwSess.reset(sk)
		h.pwIP.reset(ip)
		h.audit(r, "password_changed")
		h.dropAllSessions()
		id, _ := h.startSession() // rotated: the old cookie is no longer valid
		http.SetCookie(w, h.cookie(id, int(absTimeout/time.Second)))
		http.Redirect(w, r, "/admin/password?ok=1", http.StatusFound)
	default:
		w.Header().Set("Allow", "GET, HEAD, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) dropAllSessions() {
	h.mu.Lock()
	h.sessions = map[[32]byte]*session{}
	h.mu.Unlock()
}
