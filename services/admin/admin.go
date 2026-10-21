// Package admin serves the read-only owner UI over the conversation store.
// Everything except /admin/login requires a server-side session; login is
// CSRF-protected and rate limited. See docs/admin.md.
package admin

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"everything-you-need/m/services/convo"
)

//go:embed templates/*.html assets/app.css
var assets embed.FS

const (
	CookieName    = "__Host-eyn_admin"
	idleTimeout   = 2 * time.Hour
	absTimeout    = 12 * time.Hour
	csrfTTL       = 30 * time.Minute
	minBcryptCost = 10
	maxSessions   = 100
	maxFormBytes  = 4 << 10
)

// Data is the read-only query surface (implemented by *convo.Store).
type Data interface {
	ListChats(ctx context.Context, f convo.ChatFilter, p convo.Page) ([]convo.ChatSummary, int, error)
	Timeline(ctx context.Context, platform, chatID string, p convo.Page) ([]convo.TimelineItem, int, error)
	UserStats(ctx context.Context, f convo.StatsFilter) ([]convo.Stat, error)
	ChatStats(ctx context.Context, f convo.StatsFilter) ([]convo.Stat, error)
	ToolStats(ctx context.Context, f convo.StatsFilter) (convo.ToolReport, error)
}

// Config holds the owner credentials. Both Username and PasswordHash (bcrypt)
// are required; otherwise New fails and the UI stays unmounted.
type Config struct {
	Username      string
	PasswordHash  string
	SessionSecret string // optional HMAC key for login CSRF tokens; random per process if empty
}

// Options tunes limits and the clock (tests).
type Options struct {
	Now                func() time.Time
	LoginAttempts      int           // per client IP per window (default 5)
	GlobalLoginAttempt int           // all clients per window (default 60)
	LoginWindow        time.Duration // default 15m
}

type session struct {
	csrf    string
	created time.Time
	last    time.Time
}

// Handler is the admin http.Handler for /admin and /admin/...
type Handler struct {
	user   [32]byte // sha256(username)
	hash   []byte
	secret []byte
	data   Data
	now    func() time.Time
	tpl    *template.Template
	css    template.CSS
	ipLim  *limiter
	allLim *limiter

	mu       sync.Mutex
	sessions map[[32]byte]*session
}

// ValidateHash checks that h is a bcrypt hash with cost >= 10.
func ValidateHash(h string) error {
	cost, err := bcrypt.Cost([]byte(h))
	if err != nil {
		return errors.New("admin: password hash is not a valid bcrypt hash")
	}
	if cost < minBcryptCost {
		return fmt.Errorf("admin: bcrypt cost %d is below %d", cost, minBcryptCost)
	}
	return nil
}

// normalize trims whitespace and one matching pair of surrounding single or
// double quotes, which some env-file/deploy platforms pass through literally.
func normalize(v string) string {
	v = strings.TrimSpace(v)
	if n := len(v); n >= 2 && (v[0] == '\'' || v[0] == '"') && v[n-1] == v[0] {
		v = strings.TrimSpace(v[1 : n-1])
	}
	return v
}

// New builds the handler, or returns an error (admin must then stay disabled).
func New(cfg Config, data Data, opt Options) (*Handler, error) {
	cfg.Username = normalize(cfg.Username)
	cfg.PasswordHash = normalize(cfg.PasswordHash)
	cfg.SessionSecret = normalize(cfg.SessionSecret)
	if cfg.Username == "" || cfg.PasswordHash == "" {
		return nil, errors.New("admin: username and password hash are required")
	}
	if data == nil {
		return nil, errors.New("admin: no data source")
	}
	if err := ValidateHash(cfg.PasswordHash); err != nil {
		return nil, err
	}
	if opt.Now == nil {
		opt.Now = time.Now
	}
	if opt.LoginAttempts <= 0 {
		opt.LoginAttempts = 5
	}
	if opt.GlobalLoginAttempt <= 0 {
		opt.GlobalLoginAttempt = 300
	}
	if opt.LoginWindow <= 0 {
		opt.LoginWindow = 15 * time.Minute
	}
	secret := []byte(cfg.SessionSecret)
	if len(secret) == 0 {
		secret = make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return nil, err
		}
	}
	css, err := assets.ReadFile("assets/app.css")
	if err != nil {
		return nil, err
	}
	tpl, err := template.New("").Funcs(funcs).ParseFS(assets, "templates/*.html")
	if err != nil {
		return nil, err
	}
	return &Handler{
		user: sha256.Sum256([]byte(cfg.Username)), hash: []byte(cfg.PasswordHash), secret: secret,
		data: data, now: opt.Now, tpl: tpl, css: template.CSS(css),
		ipLim:    newLimiter(opt.LoginAttempts, opt.LoginWindow, opt.Now),
		allLim:   newLimiter(opt.GlobalLoginAttempt, opt.LoginWindow, opt.Now),
		sessions: map[[32]byte]*session{},
	}, nil
}

// Mount registers /admin and /admin/ on mux.
func (h *Handler) Mount(mux *http.ServeMux) {
	mux.Handle("/admin", h)
	mux.Handle("/admin/", h)
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	nonce := randToken(16)
	hd := w.Header()
	hd.Set("Cache-Control", "no-store")
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("Referrer-Policy", "no-referrer")
	hd.Set("X-Frame-Options", "DENY")
	hd.Set("Content-Security-Policy", "default-src 'none'; style-src 'nonce-"+nonce+"'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	r = r.WithContext(context.WithValue(r.Context(), nonceKey{}, nonce))

	path := strings.TrimSuffix(r.URL.Path, "/")
	if path == "/admin/login" {
		h.login(w, r)
		return
	}
	sess := h.authenticate(r)
	if sess == nil {
		if strings.HasPrefix(path, "/admin/api/") {
			writeJSONError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		http.Redirect(w, r, "/admin/login", http.StatusFound)
		return
	}
	if path == "/admin/logout" {
		h.logout(w, r, sess)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	h.route(w, r, path, sess)
}

type nonceKey struct{}

// authenticate returns the live session for the request cookie, enforcing the
// idle and absolute timeouts, or nil.
func (h *Handler) authenticate(r *http.Request) *session {
	c, err := r.Cookie(CookieName)
	if err != nil || c.Value == "" {
		return nil
	}
	key := sha256.Sum256([]byte(c.Value))
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.sessions[key]
	if s == nil {
		return nil
	}
	now := h.now()
	if now.Sub(s.last) > idleTimeout || now.Sub(s.created) > absTimeout {
		delete(h.sessions, key)
		return nil
	}
	s.last = now
	cp := *s
	return &cp
}

func (h *Handler) startSession() (id string, csrf string) {
	id, csrf = randToken(32), randToken(32)
	now := h.now()
	h.mu.Lock()
	defer h.mu.Unlock()
	for k, s := range h.sessions { // sweep expired
		if now.Sub(s.last) > idleTimeout || now.Sub(s.created) > absTimeout {
			delete(h.sessions, k)
		}
	}
	for len(h.sessions) >= maxSessions { // evict the oldest
		var oldK [32]byte
		var oldT time.Time
		first := true
		for k, s := range h.sessions {
			if first || s.created.Before(oldT) {
				oldK, oldT, first = k, s.created, false
			}
		}
		delete(h.sessions, oldK)
	}
	h.sessions[sha256.Sum256([]byte(id))] = &session{csrf: csrf, created: now, last: now}
	return id, csrf
}

func (h *Handler) cookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{Name: CookieName, Value: value, Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode, MaxAge: maxAge}
}

// ---- login CSRF: stateless HMAC token "nonce.expiry.mac" ----

func (h *Handler) mac(msg string) string {
	m := hmac.New(sha256.New, h.secret)
	m.Write([]byte("admin-login-csrf|" + msg))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (h *Handler) loginCSRF() string {
	body := randToken(16) + "." + fmt.Sprint(h.now().Add(csrfTTL).Unix())
	return body + "." + h.mac(body)
}

func (h *Handler) validLoginCSRF(tok string) bool {
	i := strings.LastIndexByte(tok, '.')
	if i < 0 {
		return false
	}
	body, mac := tok[:i], tok[i+1:]
	if subtle.ConstantTimeCompare([]byte(mac), []byte(h.mac(body))) != 1 {
		return false
	}
	j := strings.LastIndexByte(body, '.')
	if j < 0 {
		return false
	}
	var exp int64
	if _, err := fmt.Sscan(body[j+1:], &exp); err != nil {
		return false
	}
	return h.now().Unix() <= exp
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		if h.authenticate(r) != nil {
			http.Redirect(w, r, "/admin", http.StatusFound)
			return
		}
		h.renderLogin(w, r, http.StatusOK, false)
	case http.MethodPost:
		r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
		if err := r.ParseForm(); err != nil || !h.validLoginCSRF(r.PostForm.Get("csrf")) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		ip := clientIP(r)
		if !h.ipLim.allow(ip) || h.allLim.full("*") {
			w.Header().Set("Retry-After", "900")
			http.Error(w, "too many attempts, try again later", http.StatusTooManyRequests)
			return
		}
		// Always run both comparisons so timing does not reveal which part was wrong.
		u := sha256.Sum256([]byte(r.PostForm.Get("username")))
		userOK := subtle.ConstantTimeCompare(u[:], h.user[:]) == 1
		pwOK := bcrypt.CompareHashAndPassword(h.hash, []byte(r.PostForm.Get("password"))) == nil
		if !userOK || !pwOK {
			h.allLim.add("*") // the global bucket counts failures only
			h.renderLogin(w, r, http.StatusUnauthorized, true)
			return
		}
		h.ipLim.reset(ip)
		id, _ := h.startSession()
		http.SetCookie(w, h.cookie(id, int(absTimeout/time.Second)))
		http.Redirect(w, r, "/admin", http.StatusFound)
	default:
		w.Header().Set("Allow", "GET, HEAD, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request, s *session) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil || subtle.ConstantTimeCompare([]byte(r.PostForm.Get("csrf")), []byte(s.csrf)) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if c, err := r.Cookie(CookieName); err == nil {
		h.mu.Lock()
		delete(h.sessions, sha256.Sum256([]byte(c.Value)))
		h.mu.Unlock()
	}
	http.SetCookie(w, h.cookie("", -1))
	http.Redirect(w, r, "/admin/login", http.StatusFound)
}

// ---- helpers ----

func randToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("admin: no randomness") // never continue with predictable tokens
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// clientIP prefers Cloudflare's header (the site is served through it), then
// the last X-Forwarded-For hop (the one our own proxy appended), else the peer address.
func clientIP(r *http.Request) string {
	if v := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); v != "" {
		if ip := net.ParseIP(v); ip != nil {
			return ip.String()
		}
	}
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		parts := strings.Split(v, ",")
		if ip := net.ParseIP(strings.TrimSpace(parts[len(parts)-1])); ip != nil {
			return ip.String()
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// limiter is a sliding-window attempt counter keyed by client.
type limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	now    func() time.Time
	hits   map[string][]time.Time
}

func newLimiter(max int, window time.Duration, now func() time.Time) *limiter {
	return &limiter{max: max, window: window, now: now, hits: map[string][]time.Time{}}
}

// allow records an attempt and reports whether it is within the limit.
func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := l.now()
	if len(l.hits) > 10000 {
		for k, v := range l.hits {
			if len(v) == 0 || n.Sub(v[len(v)-1]) > l.window {
				delete(l.hits, k)
			}
		}
	}
	keep := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if n.Sub(t) <= l.window {
			keep = append(keep, t)
		}
	}
	if len(keep) >= l.max {
		l.hits[key] = keep
		return false
	}
	l.hits[key] = append(keep, n)
	return true
}

// live drops entries outside the window (l.mu held) and returns what is left.
func (l *limiter) live(key string) []time.Time {
	n := l.now()
	keep := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if n.Sub(t) <= l.window {
			keep = append(keep, t)
		}
	}
	if len(keep) == 0 {
		delete(l.hits, key)
		return nil
	}
	l.hits[key] = keep
	return keep
}

// full reports whether key is at its limit without recording an attempt.
func (l *limiter) full(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.live(key)) >= l.max
}

// add records an attempt unconditionally; stored attempts stay bounded by
// max (the oldest are dropped) so a flood cannot grow the slice.
func (l *limiter) add(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	keep := append(l.live(key), l.now())
	if len(keep) > l.max {
		keep = keep[len(keep)-l.max:]
	}
	l.hits[key] = keep
}

func (l *limiter) reset(key string) {
	l.mu.Lock()
	delete(l.hits, key)
	l.mu.Unlock()
}
