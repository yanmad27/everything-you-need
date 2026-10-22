package chat

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

// reasonErr tags an error with a fixed, log-safe reason class.
type reasonErr struct {
	reason string
	err    error
}

func (e *reasonErr) Error() string { return e.err.Error() }
func (e *reasonErr) Unwrap() error { return e.err }

func withReason(reason string, err error) error { return &reasonErr{reason: reason, err: err} }

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
	h := u.Hostname()
	if h == "" || !(strings.Contains(h, ".") || strings.Contains(h, ":")) {
		return nil, false
	}
	if p := u.Port(); p != "" && p != "80" && p != "443" {
		return nil, false
	}
	return u, true
}
