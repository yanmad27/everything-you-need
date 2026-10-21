package chat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
