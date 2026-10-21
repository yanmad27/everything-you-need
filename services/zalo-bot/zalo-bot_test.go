package zalobot

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

const fakeToken = "111:fake-test-token"

func TestSendMessagePostsCorrectRequest(t *testing.T) {
	var gotPath, gotCT string
	var gotBody sendMessageRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.Method + " " + r.URL.Path
		gotCT = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":"m1"}}`))
	}))
	defer srv.Close()

	z := NewClient(fakeToken, srv.URL)
	if err := z.SendReply(context.Background(), "chat-1", 99, "xin chào"); err != nil {
		t.Fatalf("SendReply: %v", err)
	}
	if gotPath != "POST /bot"+fakeToken+"/sendMessage" {
		t.Errorf("path = %q", gotPath)
	}
	if gotCT != "application/json" {
		t.Errorf("content-type = %q", gotCT)
	}
	if gotBody.ChatID != "chat-1" || gotBody.Text != "xin chào" {
		t.Errorf("body = %+v", gotBody)
	}
}

func TestSendMessageAPIErrorDoesNotLeakToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"bad chat for ` + fakeToken + `"}`))
	}))
	defer srv.Close()

	err := NewClient(fakeToken, srv.URL).SendMessage(context.Background(), "c", "hi")
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), fakeToken) {
		t.Errorf("error leaks token: %v", err)
	}
}

func TestSendMessageTransportErrorDoesNotLeakToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // connection refused

	err := NewClient(fakeToken, url).SendMessage(context.Background(), "c", "hi")
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), fakeToken) || strings.Contains(err.Error(), "/bot") {
		t.Errorf("error leaks URL/token: %v", err)
	}
}

func TestSendMessageSplitsLongText(t *testing.T) {
	var mu sync.Mutex
	var parts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body sendMessageRequest
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		mu.Lock()
		parts = append(parts, body.Text)
		mu.Unlock()
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	long := strings.Repeat("ắ", 4500)
	if err := NewClient(fakeToken, srv.URL).SendMessage(context.Background(), "c", long); err != nil {
		t.Fatal(err)
	}
	if len(parts) != 3 {
		t.Fatalf("want 3 parts, got %d", len(parts))
	}
	if strings.Join(parts, "") != long {
		t.Error("chunks do not reassemble to the original text")
	}
	for _, p := range parts {
		if n := len([]rune(p)); n > maxTextLen {
			t.Errorf("chunk of %d runes exceeds limit", n)
		}
	}
}

// --- webhook ---

const privateText = `{"ok":true,"result":{"event_name":"message.text.received","message":{
 "from":{"id":"u1","display_name":"Ted","is_bot":false},
 "chat":{"id":"u1","chat_type":"PRIVATE"},"text":"nhắc tao ăn cơm","message_id":"m1","date":1750316131602}}}`

const groupText = `{"ok":true,"result":{"event_name":"message.text.received","message":{
 "from":{"id":"u2","display_name":"Lan","is_bot":false},
 "chat":{"id":"g9","chat_type":"GROUP"},"text":"pink ơi","message_id":"m2","date":1750316131602}}}`

func serve(t *testing.T, secret, header, body string) (int, []Message) {
	t.Helper()
	var mu sync.Mutex
	var got []Message
	done := make(chan struct{}, 4)
	h := NewWebhookHandler(secret, func(_ context.Context, m Message) {
		mu.Lock()
		got = append(got, m)
		mu.Unlock()
		done <- struct{}{}
	})
	req := httptest.NewRequest("POST", "/zalo-webhook", strings.NewReader(body))
	if header != "" {
		req.Header.Set(SecretHeader, header)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	select {
	case <-done:
	case <-time.After(150 * time.Millisecond):
	}
	mu.Lock()
	defer mu.Unlock()
	return rec.Code, append([]Message(nil), got...)
}

func TestWebhookSecret(t *testing.T) {
	if code, got := serve(t, "topsecret", "", privateText); code != 401 || len(got) != 0 {
		t.Errorf("missing secret: code=%d dispatched=%d", code, len(got))
	}
	if code, got := serve(t, "topsecret", "wrong", privateText); code != 401 || len(got) != 0 {
		t.Errorf("bad secret: code=%d dispatched=%d", code, len(got))
	}
	if code, got := serve(t, "", "", privateText); code != 401 || len(got) != 0 {
		t.Errorf("empty configured secret must reject: code=%d dispatched=%d", code, len(got))
	}
	if code, got := serve(t, "topsecret", "topsecret", privateText); code != 200 || len(got) != 1 {
		t.Errorf("good secret: code=%d dispatched=%d", code, len(got))
	}
}

func TestWebhookParsesPrivateAndGroup(t *testing.T) {
	_, got := serve(t, "s", "s", privateText)
	if len(got) != 1 || got[0].Chat.ChatType != "PRIVATE" || got[0].Chat.ID != "u1" ||
		got[0].From.DisplayName != "Ted" || got[0].Text != "nhắc tao ăn cơm" {
		t.Errorf("private: %+v", got)
	}
	_, got = serve(t, "s", "s", groupText)
	if len(got) != 1 || got[0].Chat.ChatType != "GROUP" || got[0].Chat.ID != "g9" || got[0].From.ID != "u2" {
		t.Errorf("group: %+v", got)
	}
}

func TestWebhookIgnoresNonTextAndBot(t *testing.T) {
	image := `{"ok":true,"result":{"event_name":"message.image.received","message":{
	 "from":{"id":"u1","is_bot":false},"chat":{"id":"u1","chat_type":"PRIVATE"},"photo":"http://x","message_id":"m3"}}}`
	bot := strings.Replace(privateText, `"is_bot":false`, `"is_bot":true`, 1)
	for name, body := range map[string]string{"image": image, "bot": bot, "unsupported": `{"ok":true,"result":{"event_name":"message.unsupported.received"}}`} {
		code, got := serve(t, "s", "s", body)
		if code != 200 || len(got) != 0 {
			t.Errorf("%s: code=%d dispatched=%d", name, code, len(got))
		}
	}
	if code, _ := serve(t, "s", "s", "{not json"); code != 400 {
		t.Errorf("bad json: code=%d", code)
	}
}

func TestWebhookDropsDuplicateDelivery(t *testing.T) {
	var mu sync.Mutex
	n := 0
	h := NewWebhookHandler("s", func(context.Context, Message) { mu.Lock(); n++; mu.Unlock() })
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("POST", "/", strings.NewReader(privateText))
		req.Header.Set(SecretHeader, "s")
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if n != 1 {
		t.Errorf("dispatched %d times, want 1", n)
	}
}

func TestStripLeadingMention(t *testing.T) {
	names := []string{"Bot Pink EYN", "bot.RXScwGjR", "Pink"}
	cases := map[string]string{
		"@Bot Pink EYN nhắc tao 5 phút nữa uống nước": "nhắc tao 5 phút nữa uống nước",
		"@bot pink eyn, hôm nay thế nào":              "hôm nay thế nào",
		"@bot.RXScwGjR /reminders":                    "/reminders",
		"@SomeoneElse nhắc tao họp":                   "nhắc tao họp",               // unknown name: single token stripped
		"nhắc tao họp @Bot Pink EYN":                  "nhắc tao họp @Bot Pink EYN", // only leading
		"không có mention":                            "không có mention",
		"@Bot Pink EYN":                               "",
	}
	for in, want := range cases {
		if got := StripLeadingMention(in, names); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
	if got := StripLeadingMention("@Bot Pink EYN hi", nil); got != "Pink EYN hi" {
		t.Errorf("unknown names fallback: %q", got)
	}
}

func TestBotNamesFromGetMe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/getMe") {
			t.Errorf("path %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{"id":"1","account_name":"bot.X","display_name":"Bot Pink"}}`))
	}))
	defer srv.Close()
	names, err := NewClient(fakeToken, srv.URL).BotNames(context.Background())
	if err != nil || len(names) != 2 || names[0] != "Bot Pink" || names[1] != "bot.X" {
		t.Errorf("names=%v err=%v", names, err)
	}
}

// captureLog redirects the std logger for the duration of fn.
func captureLog(fn func()) string {
	var buf strings.Builder
	var mu sync.Mutex
	log.SetOutput(writerFunc(func(p []byte) (int, error) { mu.Lock(); defer mu.Unlock(); return buf.Write(p) }))
	defer log.SetOutput(os.Stderr)
	fn()
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	return buf.String()
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

func TestWebhookAcceptsBareAndWrapped(t *testing.T) {
	bare := `{"event_name":"message.text.received","message":{"from":{"id":123456,"display_name":"Ted","is_bot":false},
	 "chat":{"id":987654321,"chat_type":"private"},"text":"hello","message_id":77}}`
	var code int
	var got []Message
	logs := captureLog(func() { code, got = serve(t, "s", "s", bare) })
	if code != 200 || len(got) != 1 || got[0].Chat.ID != "987654321" || got[0].From.ID != "123456" || got[0].MessageID != "77" {
		t.Fatalf("bare: code=%d got=%+v", code, got)
	}
	if !strings.Contains(logs, "shape=bare") || !strings.Contains(logs, "outcome=dispatched") || !strings.Contains(logs, "chat=…4321") ||
		!strings.Contains(logs, "chat_type=\"PRIVATE\"") || !strings.Contains(logs, "text_len=5") {
		t.Errorf("bare log: %q", logs)
	}
	logs = captureLog(func() { _, got = serve(t, "s", "s", privateText) })
	if len(got) != 1 || !strings.Contains(logs, "shape=wrapped") || !strings.Contains(logs, "outcome=dispatched") {
		t.Errorf("wrapped: got=%+v log=%q", got, logs)
	}
}

func TestWebhookGroupMentionDispatched(t *testing.T) {
	body := strings.Replace(groupText, "pink ơi", "@Bot Pink EYN nhắc tao", 1)
	var got []Message
	logs := captureLog(func() { _, got = serve(t, "s", "s", body) })
	if len(got) != 1 || !strings.HasPrefix(got[0].Text, "@Bot Pink") || !strings.Contains(logs, "chat_type=\"GROUP\"") {
		t.Errorf("group: got=%+v log=%q", got, logs)
	}
}

func TestWebhookLogsDropReasonWithoutContent(t *testing.T) {
	image := `{"ok":true,"result":{"event_name":"message.image.received","message":{
	 "from":{"id":"u1","is_bot":false},"chat":{"id":"u1","chat_type":"PRIVATE"},"photo":"http://x","message_id":"m3"}}}`
	var got []Message
	logs := captureLog(func() { _, got = serve(t, "topsecret", "topsecret", image) })
	if len(got) != 0 || !strings.Contains(logs, "outcome=dropped:event_not_text") {
		t.Errorf("got=%+v log=%q", got, logs)
	}
	if strings.Contains(logs, "topsecret") || strings.Contains(logs, "structure=") {
		t.Errorf("known non-text event must not dump structure / leak secret: %q", logs)
	}
}

func TestWebhookUnknownShapeLogsTypesOnly(t *testing.T) {
	body := `{"weird":{"who":"SECRETVALUE-abc","n":42,"flag":true},"list":["PLANTEDTEXT"]}`
	var got []Message
	logs := captureLog(func() { _, got = serve(t, "hdr-secret", "hdr-secret", body) })
	if len(got) != 0 || !strings.Contains(logs, "shape=unknown") || !strings.Contains(logs, "outcome=dropped:unknown_shape") ||
		!strings.Contains(logs, "structure={\"list\":[string],\"weird\":{\"flag\":bool,\"n\":number,\"who\":string}}") {
		t.Errorf("got=%+v log=%q", got, logs)
	}
	for _, leak := range []string{"SECRETVALUE", "PLANTEDTEXT", "hdr-secret", "42"} {
		if strings.Contains(logs, leak) {
			t.Errorf("log leaks %q: %q", leak, logs)
		}
	}
	// unrecognised event name also dumps structure, still without values
	odd := `{"ok":true,"result":{"event_name":"message.weird.received","message":{"text":"PLANTEDTEXT","chat":{"id":"c1"}}}}`
	logs = captureLog(func() { serve(t, "s", "s", odd) })
	if !strings.Contains(logs, "structure=") || strings.Contains(logs, "PLANTEDTEXT") {
		t.Errorf("odd event log: %q", logs)
	}
}

func TestWebhookTolerantOptionalFields(t *testing.T) {
	for name, extra := range map[string]string{
		"date string": `"date":"1750316131602"`,
		"date float":  `"date":1.75031613e12`,
		"date object": `"date":{"x":1}`,
	} {
		body := `{"event_name":"message.text.received","message":{"from":{"id":"u1","is_bot":"false"},` +
			`"chat":{"id":"c12345","chat_type":"PRIVATE"},"text":"hi","message_id":"` + name + `",` + extra + `}}`
		if _, got := serve(t, "s", "s", body); len(got) != 1 || got[0].Text != "hi" {
			t.Errorf("%s: got=%+v", name, got)
		}
	}
	bot := `{"event_name":"message.text.received","message":{"from":{"id":"b","is_bot":"true"},"chat":{"id":"c1"},"text":"hi"}}`
	if _, got := serve(t, "s", "s", bot); len(got) != 0 {
		t.Errorf("string is_bot=true must drop: %+v", got)
	}
}

func TestLogHygieneCaps(t *testing.T) {
	long := strings.Repeat("ệ", 100)
	if got := SafeField(long, 40); len([]rune(got)) != 41 {
		t.Errorf("SafeField runes=%d", len([]rune(got)))
	}
	if got := MaskID("abcệưở"); got != "…cệưở" && got != "…bcệưở"[:0]+"…cệưở" {
		t.Errorf("MaskID=%q", got)
	}
	big := map[string]any{}
	for i := 0; i < 200; i++ {
		big[strings.Repeat("ệ", 30)+strconv.Itoa(i)] = "v"
	}
	if d := structureDump(big); len(d) > 504 || !utf8.ValidString(d) {
		t.Errorf("dump len=%d valid=%v", len(d), utf8.ValidString(d))
	}
}
