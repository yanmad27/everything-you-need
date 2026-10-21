package zalobot

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// SecretHeader carries the secret_token registered via setWebhook.
const SecretHeader = "X-Bot-Api-Secret-Token"

const eventTextReceived = "message.text.received"

// knownNonTextEvent reports documented events that are expected to be dropped.
func knownNonTextEvent(e string) bool {
	switch strings.ToLower(e) {
	case "message.image.received", "message.sticker.received", "message.voice.received", "message.unsupported.received":
		return true
	}
	return false
}

// maxBodyBytes bounds the webhook request body.
const maxBodyBytes = 1 << 20

// Update is the documented webhook payload: {"ok":true,"result":{...}}. The
// handler also accepts the bare form {"event_name":...,"message":...}.
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

// flexString decodes a JSON string or number into a string.
type flexString string

func (f *flexString) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		return nil
	}
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*f = flexString(s)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*f = flexString(n.String())
	return nil
}

func (m *Message) UnmarshalJSON(b []byte) error {
	var a struct {
		MessageID flexString `json:"message_id"`
		From      User       `json:"from"`
		Chat      Chat       `json:"chat"`
		Text      string     `json:"text"`
		Date      int64      `json:"date"`
	}
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	*m = Message{MessageID: string(a.MessageID), From: a.From, Chat: a.Chat, Text: a.Text, Date: a.Date}
	return nil
}

func (u *User) UnmarshalJSON(b []byte) error {
	var a struct {
		ID          flexString `json:"id"`
		DisplayName string     `json:"display_name"`
		IsBot       bool       `json:"is_bot"`
	}
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	*u = User{ID: string(a.ID), DisplayName: a.DisplayName, IsBot: a.IsBot}
	return nil
}

func (c *Chat) UnmarshalJSON(b []byte) error {
	var a struct {
		ID       flexString `json:"id"`
		ChatType string     `json:"chat_type"`
	}
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	*c = Chat{ID: string(a.ID), ChatType: a.ChatType}
	return nil
}

// envelope covers both the wrapped and the bare payload shapes.
type envelope struct {
	Result    *Result  `json:"result"`
	EventName string   `json:"event_name"`
	Message   *Message `json:"message"`
}

// dispatchTimeout bounds one dispatched message (LLM call + reply).
const dispatchTimeout = 90 * time.Second

func maskID(id string) string {
	if id == "" {
		return "-"
	}
	if len(id) <= 4 {
		return "…" + id
	}
	return "…" + id[len(id)-4:]
}

func jsonType(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "bool"
	case float64:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	default:
		return "object"
	}
}

// structureDump renders keys with value types only (no values), capped.
func structureDump(v any) string {
	var sb strings.Builder
	var walk func(v any)
	walk = func(v any) {
		if sb.Len() > 600 {
			return
		}
		switch t := v.(type) {
		case map[string]any:
			keys := make([]string, 0, len(t))
			for k := range t {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			sb.WriteString("{")
			for i, k := range keys {
				if i > 0 {
					sb.WriteString(",")
				}
				sb.WriteString(k + ":")
				walk(t[k])
			}
			sb.WriteString("}")
		case []any:
			sb.WriteString("[")
			if len(t) > 0 {
				walk(t[0])
			}
			sb.WriteString("]")
		default:
			sb.WriteString(jsonType(v))
		}
	}
	walk(v)
	out := sb.String()
	if len(out) > 500 {
		out = out[:500] + "…"
	}
	return out
}

func topKeys(raw map[string]json.RawMessage) string {
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
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

		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
		if err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(body, &raw); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"message":"Success"}`))

		shape, event, msg := "unknown", "", (*Message)(nil)
		var env envelope
		decodeErr := json.Unmarshal(body, &env)
		switch {
		case decodeErr != nil:
		case env.Result != nil && (env.Result.EventName != "" || env.Result.Message != nil):
			shape, event, msg = "wrapped", env.Result.EventName, env.Result.Message
		case env.EventName != "" || env.Message != nil:
			shape, event, msg = "bare", env.EventName, env.Message
		}

		textLen, isBot, chatType, chatID := 0, false, "-", ""
		if msg != nil {
			textLen, isBot, chatID = utf8.RuneCountInString(msg.Text), msg.From.IsBot, msg.Chat.ID
			if msg.Chat.ChatType != "" {
				chatType = strings.ToUpper(msg.Chat.ChatType)
			}
		}
		outcome := "dispatched"
		switch {
		case decodeErr != nil:
			outcome = "dropped:decode_error"
		case shape == "unknown":
			outcome = "dropped:unknown_shape"
		case !strings.EqualFold(event, eventTextReceived):
			outcome = "dropped:event_not_text"
		case msg == nil:
			outcome = "dropped:no_message"
		case msg.Text == "":
			outcome = "dropped:empty_text"
		case msg.Chat.ID == "":
			outcome = "dropped:no_chat_id"
		case msg.From.IsBot:
			outcome = "dropped:from_bot"
		case msg.MessageID != "" && !seen.add(msg.MessageID):
			outcome = "dropped:duplicate"
		}
		log.Printf("zalo webhook: shape=%s keys=[%s] event=%q chat_type=%s text_len=%d is_bot=%t chat=%s outcome=%s",
			shape, topKeys(raw), event, chatType, textLen, isBot, maskID(chatID), outcome)
		if shape == "unknown" || (outcome == "dropped:event_not_text" && !knownNonTextEvent(event)) {
			var generic any
			_ = json.Unmarshal(body, &generic)
			log.Printf("zalo webhook: structure=%s", structureDump(generic))
		}
		if outcome != "dispatched" {
			return
		}

		go func(m Message) {
			defer func() {
				if rec := recover(); rec != nil {
					log.Printf("zalo webhook dispatcher panic: %v", rec)
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), dispatchTimeout)
			defer cancel()
			dispatch(ctx, m)
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
