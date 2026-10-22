package main

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"everything-you-need/m/services/botconfig"
	"everything-you-need/m/services/chat"
	"everything-you-need/m/services/config"
	"everything-you-need/m/services/convo"
	"everything-you-need/m/services/llm"
	"everything-you-need/m/services/reminder"
	zalobot "everything-you-need/m/services/zalo-bot"
)

type toolSpy struct {
	mu   sync.Mutex
	sys  []string
	defs []string
}

func (s *toolSpy) CompleteWithTools(_ context.Context, sys string, _ []llm.ChatMessage, tools []llm.ToolDef, _ int) (llm.ToolResponse, error) {
	var names []string
	for _, d := range tools {
		names = append(names, d.Name)
	}
	s.mu.Lock()
	s.sys, s.defs = append(s.sys, sys), append(s.defs, strings.Join(names, ","))
	s.mu.Unlock()
	return llm.ToolResponse{Text: "ok", Assistant: llm.ChatMessage{Role: "assistant", Content: "ok"}}, nil
}

type nopSearch struct{}

func (nopSearch) Search(context.Context, string, int) ([]chat.WebHit, error)     { return nil, nil }
func (nopSearch) SearchNews(context.Context, string, int) ([]chat.WebHit, error) { return nil, nil }

type sink struct {
	mu    sync.Mutex
	texts []string
}

func (s *sink) SendReply(_ context.Context, _ string, _ int64, text string) error {
	s.mu.Lock()
	s.texts = append(s.texts, text)
	s.mu.Unlock()
	return nil
}

type countLLM struct{ n int }

func (p *countLLM) Complete(context.Context, string, []llm.Message, int) (string, error) {
	p.n++
	return "plain", nil
}

func liveRig(t *testing.T) (*chat.Service, *botconfig.Provider, *toolSpy, *sink, *convo.Store) {
	t.Helper()
	st, err := convo.Open(filepath.Join(t.TempDir(), "c.db"), convo.Options{NoBackground: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cfg := &config.Config{}
	cfg.Zalo.Enabled = true // no LLM base/key: lv.Client stays nil, the static client is used
	prov, err := botconfig.New(context.Background(), st, "0123456789abcdef0123456789abcdef-M", map[string]botconfig.Defaults{"zalo": botconfig.ZaloDefaults(cfg, "")})
	if err != nil {
		t.Fatal(err)
	}
	spy, snd := &toolSpy{}, &sink{}
	zl := &zaloLive{prov: prov}
	svc := chat.NewServiceWithCompleter(&countLLM{}, snd).WithTools(spy, chat.Tools{Platform: "zalo", Search: nopSearch{}, Recorder: st}).WithLive(zl.source)
	return svc, prov, spy, snd, st
}

func zmsg(text string) zalobot.Message {
	return zalobot.Message{Text: text, Kind: "text", Chat: zalobot.Chat{ID: "chat-1", ChatType: "PRIVATE"}, From: zalobot.User{ID: "u1", DisplayName: "An"}}
}

func TestZaloLiveSaveAppliesToNextTurn(t *testing.T) {
	svc, prov, spy, _, _ := liveRig(t)
	ctx := context.Background()
	dispatchZalo(ctx, nil, svc, nil, zmsg("xin chào"))
	if !strings.Contains(spy.defs[0], "web_search") || !strings.Contains(spy.defs[0], "fetch_url") {
		t.Fatalf("baseline: %s", spy.defs[0])
	}
	if err := prov.Save(ctx, "zalo", map[string]string{botconfig.KeyToolWebSearch: "false", botconfig.KeyPersona: "PERSONA-XYZ"}, "owner"); err != nil {
		t.Fatal(err)
	}
	dispatchZalo(ctx, nil, svc, nil, zmsg("lần hai"))
	if strings.Contains(spy.defs[1], "web_search") || !strings.Contains(spy.sys[1], "PERSONA-XYZ") || strings.Contains(spy.sys[0], "PERSONA-XYZ") {
		t.Fatalf("save not seen by next turn: %s | %q", spy.defs[1], spy.sys[1])
	}
	if err := prov.Reset(ctx, "zalo", botconfig.KeyPersona, "owner"); err != nil {
		t.Fatal(err)
	}
	dispatchZalo(ctx, nil, svc, nil, zmsg("lần ba"))
	if strings.Contains(spy.sys[2], "PERSONA-XYZ") {
		t.Fatal("reset not applied")
	}
}

func TestZaloPausedIsSilentAndRecorded(t *testing.T) {
	svc, prov, spy, snd, st := liveRig(t)
	ctx := context.Background()
	if err := prov.Save(ctx, "zalo", map[string]string{botconfig.KeyEnabled: "false"}, "owner"); err != nil {
		t.Fatal(err)
	}
	dispatchZalo(ctx, nil, svc, nil, zmsg("có ai không"))
	img := zmsg("")
	img.Kind, img.Photo = "image", "https://img.example/x.jpg"
	dispatchZaloWith(ctx, nil, svc, nil, img, func(context.Context, string) ([]byte, string, error) {
		t.Fatal("image fetched while paused")
		return nil, "", nil
	})
	if len(spy.sys) != 0 || len(snd.texts) != 0 {
		t.Fatalf("paused bot answered: llm=%d replies=%v", len(spy.sys), snd.texts)
	}
	_ = st.Flush(ctx)
	tl, _, err := st.Timeline(ctx, "zalo", "chat-1", convo.Page{})
	if err != nil || len(tl) != 2 {
		t.Fatalf("paused inbound must be recorded: %d %v", len(tl), err)
	}
	for _, it := range tl {
		if it.Message == nil || it.Message.ErrorTag != "paused" {
			t.Fatalf("not tagged paused: %+v", it)
		}
	}
	if err := prov.Reset(ctx, "zalo", botconfig.KeyEnabled, "owner"); err != nil {
		t.Fatal(err)
	}
	dispatchZalo(ctx, nil, svc, nil, zmsg("lại bật"))
	if len(spy.sys) != 1 {
		t.Fatal("not resumed")
	}
}

func TestZaloImagesOffReplyWithoutFetch(t *testing.T) {
	svc, prov, _, snd, _ := liveRig(t)
	ctx := context.Background()
	_ = prov.Save(ctx, "zalo", map[string]string{botconfig.KeyToolImages: "false"}, "owner")
	img := zmsg("")
	img.Kind, img.Photo = "image", "https://img.example/x.jpg"
	dispatchZaloWith(ctx, nil, svc, nil, img, func(context.Context, string) ([]byte, string, error) {
		t.Fatal("image fetched while images are off")
		return nil, "", nil
	})
	if len(snd.texts) != 1 || !strings.Contains(snd.texts[0], "tắt xử lý ảnh") {
		t.Fatalf("%v", snd.texts)
	}
}

func TestZaloConcurrentSaveWhileTurnsRun(t *testing.T) {
	svc, prov, _, _, _ := liveRig(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				m := zmsg("hi")
				m.Chat.ID = "chat-" + string(rune('a'+i))
				dispatchZalo(ctx, nil, svc, nil, m)
			}
		}(i)
	}
	for i := 1; i <= 20; i++ {
		v := "true"
		if i%2 == 0 {
			v = "false"
		}
		if err := prov.Save(ctx, "zalo", map[string]string{botconfig.KeyToolFetch: v, botconfig.KeyRatePerMin: "60", botconfig.KeyRateBurst: "20"}, "o"); err != nil {
			t.Error(err)
		}
	}
	wg.Wait()
}

func TestTelegramChatPathIgnoresBotConfig(t *testing.T) {
	// Telegram's chat service has no live source: dispatchIncoming never consults
	// botconfig, even when zalo is paused.
	st, _ := convo.Open(filepath.Join(t.TempDir(), "t.db"), convo.Options{NoBackground: true})
	defer st.Close()
	cfg := &config.Config{}
	prov, _ := botconfig.New(context.Background(), st, "", map[string]botconfig.Defaults{"zalo": botconfig.ZaloDefaults(cfg, "")}) // zalo disabled by default (Enabled false)
	if prov.Snapshot("zalo").Bool(botconfig.KeyEnabled) {
		t.Fatal("setup: zalo should be paused")
	}
	f, snd := &countLLM{}, &sink{}
	svc := chat.NewServiceWithCompleter(f, snd)
	got := dispatchIncoming(context.Background(), nil, svc, reminder.IncomingMessage{Platform: reminder.PlatformTelegram, ChatID: "1", Text: "pink ơi chào"}, false)
	if got != "chat" || f.n != 1 || len(snd.texts) != 1 {
		t.Fatalf("telegram path changed: %s llm=%d replies=%v", got, f.n, snd.texts)
	}
	if ctx, lv := svc.CaptureLive(context.Background()); lv.Set || ctx != context.Background() {
		t.Fatal("telegram service captured live settings")
	}
}

func TestPricesFormatRoundTrip(t *testing.T) {
	in := []convo.Price{{Model: "a", PromptPer1M: 1.25, CompletionPer1M: 10}, {Model: "b:c", PromptPer1M: 0.5, CompletionPer1M: 2}}
	got, bad := convo.ParsePrices(formatPrices(in))
	if len(bad) != 0 || len(got) != 2 || got[0] != in[0] || got[1] != in[1] {
		t.Fatalf("%q %v %v", formatPrices(in), got, bad)
	}
}
