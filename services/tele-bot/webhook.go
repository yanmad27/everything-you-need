package telebot

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
)

// Update is the subset of Telegram's Update object we consume.
type Update struct {
	UpdateID int64    `json:"update_id"`
	Message  *Message `json:"message,omitempty"`
}

type Message struct {
	MessageID int64  `json:"message_id"`
	From      *User  `json:"from,omitempty"`
	Chat      Chat   `json:"chat"`
	Text      string `json:"text,omitempty"`
}

type User struct {
	ID        int64  `json:"id"`
	Username  string `json:"username,omitempty"`
	FirstName string `json:"first_name,omitempty"`
}

type Chat struct {
	ID int64 `json:"id"`
}

// DisplayName returns a short display name for @-mentioning the user.
func (u *User) DisplayName() string {
	if u == nil {
		return ""
	}
	if u.Username != "" {
		return u.Username
	}
	return u.FirstName
}

// Dispatcher handles a decoded update. Always invoked in a fresh goroutine.
type Dispatcher func(ctx context.Context, update Update)

// NewWebhookHandler returns an http.Handler that validates Telegram's
// secret_token header, decodes the update, and dispatches it asynchronously.
func NewWebhookHandler(secretToken string, dispatch Dispatcher) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Telegram-Bot-Api-Secret-Token") != secretToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		var update Update
		if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}

		w.WriteHeader(http.StatusOK)

		go func() {
			defer func() {
				if rec := recover(); rec != nil {
					log.Printf("webhook dispatcher panic: %v", rec)
				}
			}()
			dispatch(context.Background(), update)
		}()
	})
}
