package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"everything-you-need/m/services/config"
	"everything-you-need/m/services/convo"
)

func TestAdminMountGating(t *testing.T) {
	store, err := convo.Open(filepath.Join(t.TempDir(), "c.db"), convo.Options{NoBackground: true})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	good, _ := bcrypt.GenerateFromPassword([]byte("correct horse battery"), 10)
	weak, _ := bcrypt.GenerateFromPassword([]byte("x"), 4)
	cases := map[string]struct {
		a     config.AdminConfig
		store *convo.Store
		want  bool
	}{
		"ok":        {config.AdminConfig{Username: "o", PasswordHash: string(good)}, store, true},
		"no user":   {config.AdminConfig{PasswordHash: string(good)}, store, false},
		"no hash":   {config.AdminConfig{Username: "o"}, store, false},
		"bad hash":  {config.AdminConfig{Username: "o", PasswordHash: "plain"}, store, false},
		"weak cost": {config.AdminConfig{Username: "o", PasswordHash: string(weak)}, store, false},
		"no store":  {config.AdminConfig{Username: "o", PasswordHash: string(good)}, nil, false},
		"all empty": {config.AdminConfig{}, store, false},
	}
	for name, c := range cases {
		m := adminMount(&config.Config{Admin: c.a}, c.store)
		if (m != nil) != c.want {
			t.Errorf("%s: mounted=%v want %v", name, m != nil, c.want)
			continue
		}
		mux := http.NewServeMux()
		if m != nil {
			m(mux)
		}
		mux.HandleFunc("/zalo-webhook", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", "/admin/chats", nil))
		if c.want && w.Code != 302 || !c.want && w.Code != 404 {
			t.Errorf("%s: /admin/chats = %d", name, w.Code)
		}
		w = httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("POST", "/zalo-webhook", nil))
		if w.Code != 204 {
			t.Errorf("%s: webhook affected: %d", name, w.Code)
		}
	}
}

func TestZaloModelLabel(t *testing.T) {
	cfg := &config.Config{LLMOpenAI: config.LLMOpenAIConfig{Model: "gpt-x"}, Chat: config.ChatConfig{Model: "claude-y"}}
	if got := zaloModel(cfg, buildZaloCompleter(&config.Config{LLMOpenAI: config.LLMOpenAIConfig{BaseURL: "http://x", APIKey: "k", Model: "gpt-x"}})); got != "gpt-x" {
		t.Errorf("openai: %q", got)
	}
	if got := zaloModel(cfg, nil); got != "claude-y" {
		t.Errorf("fallback: %q", got)
	}
}
