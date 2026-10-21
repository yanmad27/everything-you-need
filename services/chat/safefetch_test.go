package chat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func TestBlockedIP(t *testing.T) {
	for _, s := range []string{"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "169.254.169.254",
		"0.0.0.0", "224.0.0.1", "100.64.0.1", "::1", "fe80::1", "fc00::1", "::ffff:127.0.0.1",
		"0.1.2.3", "0.255.255.255", "198.18.0.1", "198.19.255.255", "192.0.0.8", "240.0.0.1", "255.255.255.255",
		"64:ff9b::7f00:1", "64:ff9b::a9fe:a9fe", "2002:7f00:1::1", "2001:0:4136:e378:8000:63bf:3fff:fdd2"} {
		if !blockedIP(netip.MustParseAddr(s)) {
			t.Errorf("%s should be blocked", s)
		}
	}
	for _, s := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		if blockedIP(netip.MustParseAddr(s)) {
			t.Errorf("%s should be allowed", s)
		}
	}
}

func TestSafeFetchRefusesLoopbackDefaultFetcherUnchanged(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html><body>secret internal page</body></html>"))
	}))
	defer srv.Close() // listens on 127.0.0.1
	ctx := context.Background()

	// default (Telegram) client still reaches it
	if text, err := getText(ctx, fetchHTTP, srv.URL, true); err != nil || !strings.Contains(text, "secret internal page") {
		t.Fatalf("default fetcher changed: %q %v", text, err)
	}
	// safe client refuses at dial time
	if _, err := getText(ctx, safeHTTP, srv.URL, true); err == nil {
		t.Fatal("safe client must refuse loopback")
	}
	// full safe path: error must not echo the URL
	_, err := fetchPageTextSafe(ctx, srv.URL)
	if err == nil || strings.Contains(err.Error(), srv.URL) || strings.Contains(err.Error(), "127.0.0.1") {
		t.Errorf("safe error = %v", err)
	}
}

func TestSafeFetchRefusesRedirectToLoopback(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("x")) }))
	defer target.Close()
	redir := httptest.NewServer(http.RedirectHandler(target.URL, http.StatusFound))
	defer redir.Close()
	if _, err := getText(context.Background(), safeHTTP, redir.URL, true); err == nil {
		t.Fatal("redirect chain to loopback must fail")
	}
}

func TestSafeFetchRejectsNonHTTPScheme(t *testing.T) {
	for _, u := range []string{"file:///etc/passwd", "ftp://example.com/x", "gopher://x"} {
		if _, err := fetchPageTextSafe(context.Background(), u); err == nil {
			t.Errorf("%s accepted", u)
		}
	}
}
