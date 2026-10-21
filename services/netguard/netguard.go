// Package netguard provides an SSRF-safe HTTP client: connections to
// loopback, private, link-local and other special-purpose addresses are
// refused at dial time, on the already-resolved IP.
package netguard

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"time"
)

// ErrBlocked is returned for refused destinations. Its message is fixed and
// never contains the URL or host:port.
var ErrBlocked = errors.New("blocked destination")

var (
	errTooManyRedirects = errors.New("too many redirects")
	errBadScheme        = errors.New("unsupported scheme")
)

// extraBlocked lists special-purpose ranges not covered by the netip predicates.
var extraBlocked = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),      // "this network"
	netip.MustParsePrefix("100.64.0.0/10"),  // CGNAT
	netip.MustParsePrefix("198.18.0.0/15"),  // benchmarking
	netip.MustParsePrefix("192.0.0.0/24"),   // IETF protocol assignments
	netip.MustParsePrefix("240.0.0.0/4"),    // reserved
	netip.MustParsePrefix("64:ff9b::/96"),   // NAT64
	netip.MustParsePrefix("2002::/16"),      // 6to4
	netip.MustParsePrefix("2001::/32"),      // Teredo
	netip.MustParsePrefix("::/96"),          // IPv4-compatible (deprecated), also :: and ::1
	netip.MustParsePrefix("64:ff9b:1::/48"), // local-use NAT64
	netip.MustParsePrefix("fec0::/10"),      // site-local (deprecated)
}

// IsBlockedIP reports whether ip is a loopback, private, link-local,
// unspecified, multicast, carrier-grade-NAT or otherwise special-purpose address.
func IsBlockedIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	for _, p := range extraBlocked {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// NewSafeClient returns a client that refuses blocked destinations (the check
// runs in the dialer's Control hook, so it covers redirects and DNS
// rebinding), speaks only http/https, uses no proxy and follows at most 5
// redirects.
func NewSafeClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{
		Timeout: 10 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return ErrBlocked
			}
			ip, err := netip.ParseAddr(host)
			if err != nil || IsBlockedIP(ip) {
				return ErrBlocked
			}
			return nil
		},
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: &http.Transport{DialContext: dialer.DialContext, Proxy: nil},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errTooManyRedirects
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return errBadScheme
			}
			return nil
		},
	}
}
