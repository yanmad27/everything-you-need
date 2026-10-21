package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"everything-you-need/m/services/chat"
	"everything-you-need/m/services/config"
	"everything-you-need/m/services/llm"
	"everything-you-need/m/services/reminder"
	telebot "everything-you-need/m/services/tele-bot"
	zalobot "everything-you-need/m/services/zalo-bot"
)

type captureSender struct{ texts []string }

func (c *captureSender) SendReply(_ context.Context, _ string, _ int64, text string) error {
	c.texts = append(c.texts, text)
	return nil
}
func (c *captureSender) SendMessage(_ context.Context, _, text string) error {
	c.texts = append(c.texts, text)
	return nil
}

// Zalo chat + Zalo reminder parsing go to the OpenAI-compatible provider;
// Telegram chat + Telegram reminder parsing go to the Anthropic one.
func TestZaloUsesOpenAICompatTelegramUsesAnthropic(t *testing.T) {
	var openaiHits, anthropicHits atomic.Int32
	oa := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		openaiHits.Add(1)
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("openai path %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"ok\":true,\"when\":\"2026-04-17T16:00:00+07:00\",\"task\":\"họp\"}"}}]}`))
	}))
	defer oa.Close()
	an := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		anthropicHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"m","type":"message","role":"assistant","model":"x","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1},"content":[{"type":"text","text":"{\"ok\":true,\"when\":\"2026-04-17T16:00:00+07:00\",\"task\":\"họp\"}"}]}`))
	}))
	defer an.Close()
	t.Setenv("ANTHROPIC_BASE_URL", an.URL)

	cfg := &config.Config{
		Chat:      config.ChatConfig{Enabled: true, Model: "claude-x"},
		Reminder:  config.ReminderConfig{Enabled: true, OpenAIAPIKey: "anthropic-test-key", DBPath: filepath.Join(t.TempDir(), "r.db")},
		LLMOpenAI: config.LLMOpenAIConfig{BaseURL: oa.URL, APIKey: "sk-test", Model: "codex-auto-review"},
	}
	zl, tg := &captureSender{}, &captureSender{}
	completer := buildZaloCompleter(cfg)
	if completer == nil {
		t.Fatal("zalo completer not built")
	}

	// chat
	ctx := context.Background()
	zaloChat := buildChatService(cfg, zl, completer, true)
	teleChat := buildChatService(cfg, tg, nil, false)
	if err := zaloChat.Handle(ctx, "z", 0, "pink ơi chào"); err != nil {
		t.Fatal(err)
	}
	if openaiHits.Load() != 1 || anthropicHits.Load() != 0 {
		t.Fatalf("zalo chat: openai=%d anthropic=%d", openaiHits.Load(), anthropicHits.Load())
	}
	if err := teleChat.Handle(ctx, "t", 0, "pink ơi chào"); err != nil {
		t.Fatal(err)
	}
	if openaiHits.Load() != 1 || anthropicHits.Load() != 1 {
		t.Fatalf("telegram chat: openai=%d anthropic=%d", openaiHits.Load(), anthropicHits.Load())
	}

	// reminder parsing: build with stub platform services via the real constructor
	store, err := reminder.OpenStore(cfg.Reminder.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	svc := newReminderService(cfg, store, tg, zl, completer)
	for _, plat := range []string{reminder.PlatformZalo, reminder.PlatformTelegram} {
		before := [2]int32{openaiHits.Load(), anthropicHits.Load()}
		err := svc.HandleIncoming(ctx, reminder.IncomingMessage{Platform: plat, ChatID: "c", UserID: "u", Text: "4h chiều nhắc tao họp"})
		if err != nil {
			t.Fatal(err)
		}
		dOpenAI, dAnth := openaiHits.Load()-before[0], anthropicHits.Load()-before[1]
		if plat == reminder.PlatformZalo && (dOpenAI != 1 || dAnth != 0) {
			t.Errorf("zalo parse: openai+%d anthropic+%d", dOpenAI, dAnth)
		}
		if plat == reminder.PlatformTelegram && (dOpenAI != 0 || dAnth != 1) {
			t.Errorf("telegram parse: openai+%d anthropic+%d", dOpenAI, dAnth)
		}
	}
	if len(zl.texts) < 2 || !strings.Contains(zl.texts[len(zl.texts)-1], "Đã đặt nhắc") {
		t.Errorf("zalo ack missing: %v", zl.texts)
	}
}

func TestZaloCompleterUnsetFallsBack(t *testing.T) {
	if buildZaloCompleter(&config.Config{}) != nil {
		t.Error("expected nil completer when llm_openai is unset")
	}
}

func TestZaloWebhookRefusedForBadSecretLength(t *testing.T) {
	cfg := &config.Config{Zalo: config.ZaloConfig{Enabled: true, WebhookSecret: "short"}}
	zalo := zalobot.NewClient("t", "http://127.0.0.1:1")
	chatSvc := chat.NewServiceWithCompleter(nil, &captureSender{})
	for _, secret := range []string{"", "short", strings.Repeat("x", 257)} {
		cfg.Zalo.WebhookSecret = secret
		if srv := startWebhookServer(cfg, nil, nil, nil, nil, zalo, chatSvc); srv != nil {
			t.Errorf("secret len %d: webhook must not be mounted", len(secret))
		}
	}
}

type recCompleter struct {
	mu      sync.Mutex
	prompts []string
	reply   string
}

func (r *recCompleter) Complete(_ context.Context, _ string, msgs []llm.Message, _ int) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.prompts = append(r.prompts, msgs[len(msgs)-1].Content)
	return r.reply, nil
}

func (r *recCompleter) last() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.prompts) == 0 {
		return ""
	}
	return r.prompts[len(r.prompts)-1]
}

func zaloMsg(chatType, text string) zalobot.Message {
	return zalobot.Message{
		MessageID: "m", Text: text,
		From: zalobot.User{ID: "u1", DisplayName: "Lan"},
		Chat: zalobot.Chat{ID: "c1", ChatType: chatType},
	}
}

func TestDispatchZaloGroupVersusPrivate(t *testing.T) {
	ctx := context.Background()
	names := []string{"Bot Pink EYN"}
	parse := &recCompleter{reply: `{"ok":true,"when":"2026-10-21T20:00:00+07:00","task":"uống nước"}`}
	chatLLM := &recCompleter{reply: "chào!"}
	zl := &captureSender{}

	store, err := reminder.OpenStore(filepath.Join(t.TempDir(), "r.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	svc := reminder.NewService(store, nil, nil, nil).
		WithSender(reminder.PlatformZalo, zl).
		WithParser(reminder.PlatformZalo, reminder.NewLLMParser(parse))
	chatSvc := chat.NewServiceWithCompleter(chatLLM, zl)

	// group: @mention + reminder keyword -> reminder created, mention not seen by the parser
	dispatchZalo(ctx, svc, chatSvc, names, zaloMsg("GROUP", "@Bot Pink EYN nhắc tao 5 phút nữa uống nước"))
	if p := parse.last(); !strings.Contains(p, "MESSAGE=nhắc tao 5 phút nữa uống nước") || strings.Contains(p, "@") {
		t.Errorf("parser saw %q", p)
	}
	if list, _ := store.ListForPlatformChat(ctx, reminder.PlatformZalo, "c1"); len(list) != 1 {
		t.Errorf("reminders = %d, want 1", len(list))
	}

	// group question without wake word -> answered, mention stripped
	dispatchZalo(ctx, svc, chatSvc, names, zaloMsg("GROUP", "@Bot Pink EYN hôm nay trời sao"))
	if p := chatLLM.last(); p != "hôm nay trời sao" {
		t.Errorf("chat prompt = %q", p)
	}

	// private: no wake word -> silent, mention-like text is NOT stripped
	before := len(chatLLM.prompts)
	dispatchZalo(ctx, svc, chatSvc, names, zaloMsg("PRIVATE", "hôm nay trời sao"))
	if len(chatLLM.prompts) != before {
		t.Error("private message without wake word must not be answered")
	}
	// private chats never strip a mention; only the wake word is removed
	dispatchZalo(ctx, svc, chatSvc, names, zaloMsg("PRIVATE", "@Bot Pink EYN chào"))
	if p := chatLLM.last(); p != "@Bot EYN chào" {
		t.Errorf("private prompt = %q, want %q", p, "@Bot EYN chào")
	}
	// group message that is only a mention is ignored
	n := len(chatLLM.prompts)
	dispatchZalo(ctx, svc, chatSvc, names, zaloMsg("GROUP", "@Bot Pink EYN"))
	if len(chatLLM.prompts) != n {
		t.Error("mention-only group message must be ignored")
	}
}

func TestReminderForZaloWhenTelegramKeyEmpty(t *testing.T) {
	ctx := context.Background()
	cfg := &config.Config{Reminder: config.ReminderConfig{Enabled: true, DBPath: filepath.Join(t.TempDir(), "r.db")}} // no anthropic key

	if tOn, zOn := reminderPlatforms(cfg, true, true, true); tOn || !zOn {
		t.Fatalf("key empty + provider: tele=%v zalo=%v", tOn, zOn)
	}
	if tOn, zOn := reminderPlatforms(cfg, true, true, false); tOn || zOn {
		t.Fatalf("key empty + no provider: tele=%v zalo=%v", tOn, zOn)
	}
	cfg.Reminder.OpenAIAPIKey = "k"
	if tOn, zOn := reminderPlatforms(cfg, true, true, false); !tOn || !zOn {
		t.Fatalf("key set: tele=%v zalo=%v", tOn, zOn)
	}
	cfg.Reminder.OpenAIAPIKey = ""

	// Real constructor: Telegram bot present, key empty, Zalo provider set.
	svc, store := buildReminderService(cfg, &telebot.TeleBotService{}, zalobot.NewClient("t", "http://127.0.0.1:1"), &recCompleter{reply: "{}"})
	if svc == nil {
		t.Fatal("reminder service must be available for Zalo")
	}
	defer store.Close()
	// A telegram row stays untouched (no telegram sender), exactly as when the service was off.
	id, _ := store.Create(ctx, reminder.Reminder{ChatID: "tg", UserName: "A", Task: "t", FireAt: time.Now().Add(-time.Hour)})
	if err := svc.Sweep(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if r, _ := store.GetByID(ctx, id); r.FiredAt != nil || r.Attempts != 0 {
		t.Errorf("telegram reminder touched: %+v", r)
	}
	// And with no provider and no key the service stays disabled, as on main.
	if s2, _ := buildReminderService(cfg, &telebot.TeleBotService{}, nil, nil); s2 != nil {
		t.Error("telegram-only with empty key must stay disabled")
	}
}
