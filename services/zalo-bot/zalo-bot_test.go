package zalobot

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
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
	names := []string{"Bot Pink EYN", "bot.RXScwGjR"}
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
