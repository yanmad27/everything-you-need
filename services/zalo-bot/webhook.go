package zalobot

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"
	"sync"
)

// SecretHeader carries the secret_token registered via setWebhook.
const SecretHeader = "X-Bot-Api-Secret-Token"

const eventTextReceived = "message.text.received"

// maxBodyBytes bounds the webhook request body.
const maxBodyBytes = 1 << 20

// Update is the webhook payload: {"ok":true,"result":{...}}.
type Update struct {
	Ok     bool   `json:"ok"`
	Result Result `json:"result"`
}

type Result struct {
	EventName string   `json:"event_name"`
	Message   *Message `json:"message,omitempty"`
}

type Message struct {
	MessageID string `json:"message_id"`
	From      User   `json:"from"`
	Chat      Chat   `json:"chat"`
	Text      string `json:"text,omitempty"`
	Date      int64  `json:"date,omitempty"`
}

type User struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name,omitempty"`
	IsBot       bool   `json:"is_bot"`
}

type Chat struct {
	ID       string `json:"id"`
	ChatType string `json:"chat_type"` // PRIVATE or GROUP
}

// Dispatcher handles a decoded text message. Always invoked in a fresh goroutine.
type Dispatcher func(ctx context.Context, msg Message)

// NewWebhookHandler returns an http.Handler that validates Zalo's secret header
// (constant time), keeps only human text messages, drops duplicate deliveries,
// and dispatches asynchronously. An empty secret rejects everything.
func NewWebhookHandler(secretToken string, dispatch Dispatcher) http.Handler {
	seen := newSeenSet(1024)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get(SecretHeader)
		if secretToken == "" || subtle.ConstantTimeCompare([]byte(got), []byte(secretToken)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		var update Update
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&update); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"message":"Success"}`))

		msg := update.Result.Message
		if update.Result.EventName != eventTextReceived || msg == nil ||
			msg.Text == "" || msg.Chat.ID == "" || msg.From.IsBot {
			return
		}
		if msg.MessageID != "" && !seen.add(msg.MessageID) {
			return
		}

		go func(m Message) {
			defer func() {
				if rec := recover(); rec != nil {
					log.Printf("zalo webhook dispatcher panic: %v", rec)
				}
			}()
			dispatch(context.Background(), m)
		}(*msg)
	})
}

// seenSet is a bounded set of recently seen ids (oldest evicted first).
type seenSet struct {
	mu    sync.Mutex
	limit int
	ids   map[string]struct{}
	order []string
}

func newSeenSet(limit int) *seenSet {
	return &seenSet{limit: limit, ids: make(map[string]struct{}, limit)}
}

// add records id and reports whether it was new.
func (s *seenSet) add(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.ids[id]; ok {
		return false
	}
	s.ids[id] = struct{}{}
	s.order = append(s.order, id)
	if len(s.order) > s.limit {
		delete(s.ids, s.order[0])
		s.order = s.order[1:]
	}
	return true
}
