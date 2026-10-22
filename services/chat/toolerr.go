package chat

import (
	"errors"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
)

// reasonErr tags an error with a fixed, log-safe reason class.
type reasonErr struct {
	reason  string
	err     error
	blocked bool // reported as outcome=blocked rather than error
}

func (e *reasonErr) Error() string { return e.err.Error() }
func (e *reasonErr) Unwrap() error { return e.err }

func withReason(reason string, err error) error { return &reasonErr{reason: reason, err: err} }

// blockedReason is a policy refusal (outcome=blocked).
func blockedReason(reason string, err error) error {
	return &reasonErr{reason: reason, err: err, blocked: true}
}

var (
	errUnsupportedURL = errors.New("unsupported url")
	errFetchFailed    = errors.New("fetch failed")

	serperStatusRe = regexp.MustCompile(`serper status (\d{3})`)
)

// reasonOf maps a tool error to a fixed tag for logs. Only tags and HTTP status
// digits are produced, never URLs, queries, keys or provider text.
func reasonOf(err error) string {
	var re *reasonErr
	switch {
	case errors.As(err, &re):
		return re.reason
	case errors.Is(err, errFetchRefused):
		return "fetch_refused"
	case errors.Is(err, errUnsupportedURL):
		return "invalid_url"
	case errors.Is(err, errFetchFailed):
		return "fetch_failed"
	}
	msg := err.Error()
	if m := serperStatusRe.FindStringSubmatch(msg); m != nil {
		return "serper_status_" + m[1]
	}
	switch {
	case strings.Contains(msg, "api key not configured"):
		return "serper_no_key"
	case strings.HasPrefix(msg, "serper request"):
		return "serper_transport"
	case strings.HasPrefix(msg, "serper"):
		return "serper_response"
	}
	return "tool_error"
}

// badHost rejects IP literals (including odd numeric forms such as 127.1 or
// 0x7f.1: a dotted host must end in a non-numeric label) and local-only names.
func badHost(h string) bool {
	if _, err := netip.ParseAddr(h); err == nil {
		return true
	}
	if h == "localhost" || strings.HasPrefix(h, "localhost.") {
		return true
	}
	for _, suf := range []string{".localhost", ".local", ".internal", ".lan", ".home", ".corp"} {
		if strings.HasSuffix(h, suf) {
			return true
		}
	}
	last := h[strings.LastIndex(h, ".")+1:]
	if last == "" || strings.HasPrefix(last, "0x") {
		return true
	}
	allDigits := true
	for _, r := range last {
		if r < '0' || r > '9' {
			allDigits = false
		}
	}
	return allDigits
}

// defaultFetchAllow are the public sites the model may fetch on its own.
var defaultFetchAllow = []string{"github.com", "wikipedia.org", "news.ycombinator.com", "vnexpress.net", "tuoitre.vn",
	"thanhnien.vn", "dantri.com.vn", "cafef.vn", "coingecko.com", "reuters.com", "bbc.com", "apnews.com"}

var pathSegRe = regexp.MustCompile(`^[A-Za-z0-9._~-]+$`)

// modelURLOK is the extra shape check for a URL the model proposed itself:
// ASCII host on the allowlist (exact or subdomain) and a short plain path, so
// the URL cannot carry data. Query and fragment are stripped by the caller.
func modelURLOK(u *url.URL, allow []string) bool {
	h := strings.ToLower(u.Hostname())
	for _, r := range h {
		if r > 126 {
			return false
		}
	}
	if strings.HasPrefix(h, "xn--") || strings.Contains(h, ".xn--") {
		return false
	}
	hostOK := false
	for _, d := range allow {
		if h == d || strings.HasSuffix(h, "."+d) {
			hostOK = true
			break
		}
	}
	if !hostOK {
		return false
	}
	p := u.EscapedPath()
	if strings.Contains(p, "%") || len(p) > 80 {
		return false
	}
	segs := strings.Split(strings.Trim(p, "/"), "/")
	if p == "" || p == "/" {
		return true
	}
	if len(segs) > 5 {
		return false
	}
	for _, sg := range segs {
		if !pathSegRe.MatchString(sg) {
			return false
		}
	}
	return true
}

// normalizeURL accepts a bare host/path ("github.com/trending") by assuming
// https://, then validates: http(s) only, a dotted host, no userinfo, port
// 80/443 only. The SSRF guard still applies at dial time.
func normalizeURL(raw string) (*url.URL, bool) {
	s := strings.TrimSpace(raw)
	if s == "" || strings.ContainsAny(s, " \t\r\n\x00") {
		return nil, false
	}
	switch {
	case strings.Contains(s, "://"):
	case strings.HasPrefix(s, "//"):
		s = "https:" + s
	default:
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return nil, false
	}
	h := strings.ToLower(u.Hostname())
	for _, r := range h {
		if r > 126 {
			return nil, false // no IDN hosts for any fetch_url URL
		}
	}
	if h == "" || !strings.Contains(h, ".") || strings.HasSuffix(h, ".") || badHost(h) {
		return nil, false
	}
	if p := u.Port(); p != "" && p != "80" && p != "443" {
		return nil, false
	}
	return u, true
}
