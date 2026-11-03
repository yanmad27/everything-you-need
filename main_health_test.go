package main

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"everything-you-need/m/services/chat"
	"everything-you-need/m/services/config"
	"everything-you-need/m/services/convo"
	zalobot "everything-you-need/m/services/zalo-bot"
)

func TestAppHandlerRoutingAndMarkers(t *testing.T) {
	store, err := convo.Open(filepath.Join(t.TempDir(), "c.db"), convo.Options{NoBackground: true})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	hash, _ := bcrypt.GenerateFromPassword([]byte("correct horse battery"), 10)
	cfg := &config.Config{
		Admin: config.AdminConfig{Username: "o", PasswordHash: string(hash), SessionSecret: "0123456789abcdef0123456789abcdef"},
		Zalo:  config.ZaloConfig{Enabled: true, WebhookSecret: "zalo-secret-12345"},
	}
	zalo := zalobot.NewClient("t", "http://127.0.0.1:1")
	chatSvc := chat.NewServiceWithCompleter(nil, &captureSender{})
	var ready atomic.Bool
	h, mounted := buildAppHandler(cfg, nil, nil, nil, nil, zalo, chatSvc, &ready, adminMount(cfg, store, nil, nil))
	if mounted != 2 {
		t.Fatalf("mounted=%d want 2 (admin+zalo)", mounted)
	}
	do := func(method, path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, path, nil))
		return w
	}
	if w := do("GET", "/healthz"); w.Code != 503 {
		t.Errorf("healthz before ready = %d, want 503", w.Code)
	}
	ready.Store(true)

	cases := []struct {
		name, method, path string
		code               int
		retryAfter         bool
	}{
		{"healthz", "GET", "/healthz", 200, false},
		{"unknown path", "GET", "/nope", 503, true},
		{"root", "GET", "/", 503, true},
		{"disabled telegram webhook", "POST", "/telegram-webhook", 503, true},
		{"zalo bad secret", "POST", "/zalo-webhook", 401, false},
		{"admin login", "GET", "/admin/login", 200, false},
	}
	for _, c := range cases {
		w := do(c.method, c.path)
		if w.Code != c.code {
			t.Errorf("%s: %s %s = %d, want %d", c.name, c.method, c.path, w.Code, c.code)
		}
		if got := w.Header().Get("Retry-After") != ""; got != c.retryAfter {
			t.Errorf("%s: Retry-After present=%v want %v", c.name, got, c.retryAfter)
		}
		if w.Header().Get("X-EYN-Build") != buildSHA {
			t.Errorf("%s: X-EYN-Build=%q", c.name, w.Header().Get("X-EYN-Build"))
		}
		if id := w.Header().Get("X-EYN-Instance"); len(id) != 6 || id != instanceID {
			t.Errorf("%s: X-EYN-Instance=%q", c.name, id)
		}
		if c.name == "admin login" {
			if w.Header().Get("X-Frame-Options") != "DENY" {
				t.Errorf("admin x-frame-options lost: %q", w.Header().Get("X-Frame-Options"))
			}
			if w.Header().Get("Content-Security-Policy") == "" {
				t.Error("admin CSP lost")
			}
		}
	}
	if b := do("GET", "/healthz").Body.String(); strings.TrimSpace(b) != "ok" {
		t.Errorf("healthz body %q", b)
	}
}
