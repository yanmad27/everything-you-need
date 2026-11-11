package telebot

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSetReactionRequestShape(t *testing.T) {
	var path string
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		body, _ = io.ReadAll(r.Body)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	svc := &TeleBotService{botToken: "SECRET-TOKEN", client: srv.Client(), apiBase: srv.URL}
	if err := svc.SetReaction(context.Background(), "-100", 42, "🙏"); err != nil {
		t.Fatal(err)
	}
	if path != "/botSECRET-TOKEN/setMessageReaction" {
		t.Errorf("path=%q", path)
	}
	want := `{"chat_id":"-100","message_id":42,"reaction":[{"type":"emoji","emoji":"🙏"}]}`
	if string(body) != want {
		t.Errorf("body=%s", body)
	}
	var v map[string]any
	if json.Unmarshal(body, &v) != nil {
		t.Error("body not JSON")
	}
}

func TestSetReactionErrorsAreGeneric(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		w.Write([]byte(`{"ok":false,"description":"bad SECRET-TOKEN"}`))
	}))
	svc := &TeleBotService{botToken: "SECRET-TOKEN", client: srv.Client(), apiBase: srv.URL}
	err := svc.SetReaction(context.Background(), "1", 1, "👍")
	if err == nil || strings.Contains(err.Error(), "SECRET-TOKEN") {
		t.Errorf("rejected: err=%v", err)
	}
	srv.Close() // connection refused: the client error embeds the URL, ours must not
	err = svc.SetReaction(context.Background(), "1", 1, "👍")
	if err == nil || strings.Contains(err.Error(), "SECRET-TOKEN") || strings.Contains(err.Error(), "127.0.0.1") {
		t.Errorf("transport: err=%v", err)
	}
}

func TestSetReactionTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)
	svc := &TeleBotService{botToken: "T", client: srv.Client(), apiBase: srv.URL}
	if err := svc.SetReaction(ctx, "1", 1, "👍"); err == nil {
		t.Error("want error on timeout")
	}
}

func TestSetReactionRefusesUnlistedEmoji(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer srv.Close()
	svc := &TeleBotService{botToken: "SECRET-TOKEN", client: srv.Client(), apiBase: srv.URL}
	for _, e := range []string{"🩷", "💗", "❤\ufe0f", "", "abc", "👍👍"} {
		err := svc.React(context.Background(), "1", 1, e)
		if err == nil || strings.Contains(err.Error(), "SECRET-TOKEN") {
			t.Errorf("emoji %q: err=%v", e, err)
		}
	}
	if called {
		t.Error("request sent for a refused emoji")
	}
}
