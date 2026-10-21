package chat

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"syscall"
	"time"
)

var errBlockedAddr = errors.New("blocked address")

// extraBlocked lists special-purpose ranges not covered by the netip predicates.
var extraBlocked = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),     // "this network"
	netip.MustParsePrefix("100.64.0.0/10"), // CGNAT
	netip.MustParsePrefix("198.18.0.0/15"), // benchmarking
	netip.MustParsePrefix("192.0.0.0/24"),  // IETF protocol assignments
	netip.MustParsePrefix("240.0.0.0/4"),   // reserved
	netip.MustParsePrefix("64:ff9b::/96"),  // NAT64
	netip.MustParsePrefix("2002::/16"),     // 6to4
	netip.MustParsePrefix("2001::/32"),     // Teredo
}

// blockedIP reports whether ip is a loopback, private, link-local, unspecified,
// multicast or carrier-grade-NAT address.
func blockedIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() || inExtraBlocked(ip)
}

func inExtraBlocked(ip netip.Addr) bool {
	for _, p := range extraBlocked {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// safeClient refuses to connect to blocked addresses. The check runs in the
// dialer's Control hook on the already-resolved IP, so it also covers redirects
// and DNS rebinding. Proxies are not used.
func newSafeClient() *http.Client {
	dialer := &net.Dialer{
		Timeout: 10 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return errBlockedAddr
			}
			ip, err := netip.ParseAddr(host)
			if err != nil || blockedIP(ip) {
				return errBlockedAddr
			}
			return nil
		},
	}
	return &http.Client{
		Timeout:   30 * time.Second,
		Transport: &http.Transport{DialContext: dialer.DialContext, Proxy: nil},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return errors.New("redirect to non-http scheme")
			}
			return nil
		},
	}
}

var safeHTTP = newSafeClient()

// fetchPageTextSafe is fetchPageText with the SSRF-guarded client for the
// direct fetch, only http/https URLs, and errors that never contain the URL.
func fetchPageTextSafe(ctx context.Context, rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return "", errors.New("unsupported url")
	}
	if text, err := getText(ctx, safeHTTP, "https://r.jina.ai/"+rawURL, false); err == nil {
		return text, nil
	}
	text, err := getText(ctx, safeHTTP, rawURL, true)
	if err != nil {
		// Transport errors embed host:port; report a fixed message only.
		if errors.Is(err, errBlockedAddr) {
			return "", errors.New("fetch refused: address not allowed")
		}
		return "", errors.New("fetch failed")
	}
	return text, nil
}
