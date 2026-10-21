package netguard

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestIsBlockedIP(t *testing.T) {
	for _, s := range []string{"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "169.254.169.254",
		"0.0.0.0", "224.0.0.1", "100.64.0.1", "::1", "fe80::1", "fc00::1", "::ffff:127.0.0.1",
		"0.1.2.3", "0.255.255.255", "198.18.0.1", "198.19.255.255", "192.0.0.8", "240.0.0.1", "255.255.255.255",
		"64:ff9b::7f00:1", "64:ff9b::a9fe:a9fe", "2002:7f00:1::1", "2001:0:4136:e378:8000:63bf:3fff:fdd2"} {
		if !IsBlockedIP(netip.MustParseAddr(s)) {
			t.Errorf("%s should be blocked", s)
		}
	}
	for _, s := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		if IsBlockedIP(netip.MustParseAddr(s)) {
			t.Errorf("%s should be allowed", s)
		}
	}
}

func TestClientRefusesLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("x")) }))
	defer srv.Close()
	_, err := NewSafeClient(5 * time.Second).Get(srv.URL)
	if err == nil || !errors.Is(err, ErrBlocked) {
		t.Fatalf("err = %v, want ErrBlocked", err)
	}
}

func TestClientRefusesRedirectToLoopback(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("x")) }))
	defer target.Close()
	redir := httptest.NewServer(http.RedirectHandler(target.URL, http.StatusFound))
	defer redir.Close()
	if _, err := NewSafeClient(5 * time.Second).Get(redir.URL); err == nil {
		t.Fatal("redirect chain to loopback must fail")
	}
}

func TestClientRejectsNonHTTPScheme(t *testing.T) {
	for _, u := range []string{"file:///etc/passwd", "ftp://example.com/x", "gopher://example.com"} {
		if _, err := NewSafeClient(time.Second).Get(u); err == nil {
			t.Errorf("%s must be rejected", u)
		}
	}
}

func TestNoProxy(t *testing.T) {
	tr := NewSafeClient(time.Second).Transport.(*http.Transport)
	if tr.Proxy != nil {
		t.Error("proxy must be nil")
	}
	if strings.Contains(ErrBlocked.Error(), ":") {
		t.Error("fixed message expected")
	}
}
