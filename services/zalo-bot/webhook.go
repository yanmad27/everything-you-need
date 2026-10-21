package zalobot

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// SecretHeader carries the secret_token registered via setWebhook.
const SecretHeader = "X-Bot-Api-Secret-Token"

const (
	eventTextReceived  = "message.text.received"
	eventImageReceived = "message.image.received"
)

// photoKeys are the message fields that may carry an image URL, in priority order.
var photoKeys = []string{"photo", "url", "image", "image_url"}

func httpURL(r json.RawMessage) string {
	var s string
	if json.Unmarshal(r, &s) != nil {
		return ""
	}
	s = strings.TrimSpace(s)
	if len(s) > 8 && (strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")) {
		return s
	}
	return ""
}

// findPhotoURL recovers an image URL from a message's fields: photo (the
// documented one), else the first http(s) string among url|image|image_url,
// else attachments[*].payload.url (or .url).
func findPhotoURL(f map[string]json.RawMessage) string {
	for _, k := range photoKeys {
		if u := httpURL(f[k]); u != "" {
			return u
		}
	}
	var atts []json.RawMessage
	if json.Unmarshal(f["attachments"], &atts) != nil {
		return ""
	}
	for _, a := range atts {
		af := rawFields(a)
		if u := httpURL(af["url"]); u != "" {
			return u
		}
		if u := httpURL(rawFields(af["payload"])["url"]); u != "" {
			return u
		}
	}
	return ""
}

// hostOnly returns just the host of a URL (path and query may carry tokens).
func hostOnly(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return "-"
	}
	return SafeField(u.Hostname(), 100)
}

// knownNonTextEvent reports documented events that are expected to be dropped.
func knownNonTextEvent(e string) bool {
	switch strings.ToLower(e) {
	case "message.sticker.received", "message.voice.received", "message.unsupported.received":
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

	Kind    string `json:"-"` // "text" | "image", set by the webhook handler
	Photo   string `json:"-"` // image URL (never logged)
	Caption string `json:"-"`
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

// Decoding is field-by-field and tolerant: a field of an unexpected type is
// left at its zero value instead of failing the whole message.
func rawFields(b []byte) map[string]json.RawMessage {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return nil
	}
	return m
}

func rawString(r json.RawMessage) string {
	var f flexString
	if len(r) == 0 || f.UnmarshalJSON(r) != nil {
		return ""
	}
	return string(f)
}

func rawBool(r json.RawMessage) bool {
	var v bool
	if json.Unmarshal(r, &v) == nil {
		return v
	}
	var s string
	if json.Unmarshal(r, &s) == nil {
		v, _ = strconv.ParseBool(strings.TrimSpace(s))
	}
	return v
}

func (m *Message) UnmarshalJSON(b []byte) error {
	f := rawFields(b)
	*m = Message{MessageID: rawString(f["message_id"]), Text: rawString(f["text"]),
		Caption: rawString(f["caption"]), Photo: findPhotoURL(f)}
	if r, ok := f["from"]; ok {
		_ = m.From.UnmarshalJSON(r)
	}
	if r, ok := f["chat"]; ok {
		_ = m.Chat.UnmarshalJSON(r)
	}
	var d float64
	if json.Unmarshal(f["date"], &d) == nil {
		m.Date = int64(d)
	}
	return nil
}

func (u *User) UnmarshalJSON(b []byte) error {
	f := rawFields(b)
	*u = User{ID: rawString(f["id"]), DisplayName: rawString(f["display_name"]), IsBot: rawBool(f["is_bot"])}
	return nil
}

func (c *Chat) UnmarshalJSON(b []byte) error {
	f := rawFields(b)
	*c = Chat{ID: rawString(f["id"]), ChatType: rawString(f["chat_type"])}
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

// MaskID keeps only the last 4 runes of an id.
func MaskID(id string) string {
	if id == "" {
		return "-"
	}
	r := []rune(id)
	if len(r) > 4 {
		r = r[len(r)-4:]
	}
	return "…" + string(r)
}

// SafeField truncates s to n runes (on a rune boundary) for logging with %q.
func SafeField(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
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

// structureDump renders keys with value types only (no values), capped at
// maxDump bytes while building and truncated on a rune boundary.
func structureDump(v any) string {
	const maxDump = 500
	var sb strings.Builder
	put := func(x string) bool {
		if sb.Len()+len(x) > maxDump {
			return false
		}
		sb.WriteString(x)
		return true
	}
	var walk func(v any) bool
	walk = func(v any) bool {
		switch t := v.(type) {
		case map[string]any:
			keys := make([]string, 0, len(t))
			for k := range t {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			if !put("{") {
				return false
			}
			for i, k := range keys {
				if i > 0 && !put(",") {
					return false
				}
				if !put(strconv.Quote(SafeField(k, 40))+":") || !walk(t[k]) {
					return false
				}
			}
			return put("}")
		case []any:
			if !put("[") {
				return false
			}
			if len(t) > 0 && !walk(t[0]) {
				return false
			}
			return put("]")
		default:
			return put(jsonType(v))
		}
	}
	if !walk(v) {
		out := sb.String()
		for !utf8.ValidString(out) {
			out = out[:len(out)-1]
		}
		return out + "…"
	}
	return sb.String()
}

func topKeys(raw map[string]json.RawMessage) string {
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) > 20 {
		keys = keys[:20]
	}
	for i, k := range keys {
		keys[i] = strconv.Quote(SafeField(k, 40))
	}
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
		isImage := strings.EqualFold(event, eventImageReceived)
		outcome := "dispatched"
		switch {
		case decodeErr != nil:
			outcome = "dropped:decode_error"
		case shape == "unknown":
			outcome = "dropped:unknown_shape"
		case !isImage && !strings.EqualFold(event, eventTextReceived):
			outcome = "dropped:event_not_text"
		case msg == nil:
			outcome = "dropped:no_message"
		case isImage && msg.Photo == "":
			outcome = "dropped:no_photo"
		case !isImage && msg.Text == "":
			outcome = "dropped:empty_text"
		case msg.Chat.ID == "":
			outcome = "dropped:no_chat_id"
		case msg.From.IsBot:
			outcome = "dropped:from_bot"
		case msg.MessageID != "" && !seen.add(msg.MessageID):
			outcome = "dropped:duplicate"
		}
		imageInfo := ""
		if isImage && msg != nil {
			imageInfo = " kind=image photo_host=" + strconv.Quote(hostOnly(msg.Photo))
		}
		log.Printf("zalo webhook: shape=%s keys=[%s] event=%q chat_type=%q text_len=%d is_bot=%t chat=%s%s outcome=%s",
			shape, topKeys(raw), SafeField(event, 40), SafeField(chatType, 40), textLen, isBot, MaskID(chatID), imageInfo, outcome)
		// TODO: drop the image structure dump once real image payloads are known.
		if shape == "unknown" || isImage || (outcome == "dropped:event_not_text" && !knownNonTextEvent(event)) {
			var generic any
			_ = json.Unmarshal(body, &generic)
			log.Printf("zalo webhook: structure=%s", structureDump(generic))
		}
		if outcome != "dispatched" {
			return
		}

		if isImage {
			msg.Kind = "image"
			if msg.Caption == "" {
				msg.Caption = msg.Text
			}
		} else {
			msg.Kind = "text"
			msg.Photo, msg.Caption = "", ""
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
