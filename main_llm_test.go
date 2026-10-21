package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"everything-you-need/m/services/chat"
	"everything-you-need/m/services/config"
	zalobot "everything-you-need/m/services/zalo-bot"
	"everything-you-need/m/services/reminder"
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
	zaloChat := buildChatService(cfg, zl, completer)
	teleChat := buildChatService(cfg, tg, nil)
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
		if srv := startWebhookServer(cfg, nil, nil, nil, zalo, chatSvc); srv != nil {
			t.Errorf("secret len %d: webhook must not be mounted", len(secret))
		}
	}
}
