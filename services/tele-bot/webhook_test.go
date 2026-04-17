package telebot

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWebhookRejectsMissingSecretToken(t *testing.T) {
	handler := NewWebhookHandler("topsecret", noopDispatcher)

	req := httptest.NewRequest("POST", "/telegram-webhook", strings.NewReader(`{"update_id":1}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestWebhookRejectsWrongSecretToken(t *testing.T) {
	handler := NewWebhookHandler("topsecret", noopDispatcher)

	req := httptest.NewRequest("POST", "/telegram-webhook", strings.NewReader(`{"update_id":1}`))
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "wrong")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestWebhookAcceptsValidRequestAndDispatches(t *testing.T) {
	var (
		mu      sync.Mutex
		got     []Update
		dispatched = make(chan struct{}, 1)
	)
	dispatcher := func(_ context.Context, u Update) {
		mu.Lock()
		got = append(got, u)
		mu.Unlock()
		dispatched <- struct{}{}
	}
	handler := NewWebhookHandler("topsecret", dispatcher)

	body := `{
		"update_id": 42,
		"message": {
			"message_id": 101,
			"from": {"id": 999, "username": "doan", "first_name": "Doan"},
			"chat": {"id": -100123},
			"text": "2h nữa nhắc tao ăn cơm"
		}
	}`
	req := httptest.NewRequest("POST", "/telegram-webhook", strings.NewReader(body))
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "topsecret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	select {
	case <-dispatched:
	case <-time.After(1 * time.Second):
		t.Fatal("dispatcher was not called within 1s")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("expected 1 dispatch, got %d", len(got))
	}
	if got[0].UpdateID != 42 {
		t.Errorf("UpdateID: got %d want 42", got[0].UpdateID)
	}
	if got[0].Message == nil || got[0].Message.Text != "2h nữa nhắc tao ăn cơm" {
		t.Errorf("Message not decoded: %+v", got[0].Message)
	}
}

func TestWebhookRejectsBadJSON(t *testing.T) {
	handler := NewWebhookHandler("topsecret", noopDispatcher)

	req := httptest.NewRequest("POST", "/telegram-webhook", strings.NewReader(`not json`))
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "topsecret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func noopDispatcher(_ context.Context, _ Update) {}
