package main

import (
	"context"
	"testing"

	"everything-you-need/m/services/chat"
	"everything-you-need/m/services/config"
	"everything-you-need/m/services/llm"
	zalobot "everything-you-need/m/services/zalo-bot"
)

type plainLLM struct{ n int }

func (p *plainLLM) Complete(context.Context, string, []llm.Message, int) (string, error) {
	p.n++
	return "plain", nil
}

type noopSender struct{ sent []string }

func (n *noopSender) SendReply(_ context.Context, _ string, _ int64, t string) error {
	n.sent = append(n.sent, t)
	return nil
}

func TestBuildZaloToolsOmitsUnconfigured(t *testing.T) {
	tl := buildZaloTools(&config.Config{}, nil, nil, nil, nil)
	if tl.Search != nil || tl.Prices != nil || tl.News != nil || tl.Due != nil || tl.Reminders != nil || tl.Platform != "zalo" {
		t.Errorf("unconfigured services must be nil: %+v", tl)
	}
	cfg := &config.Config{}
	cfg.News.SerperAPIKey = "k" // digest disabled, search still wired
	if buildZaloTools(cfg, nil, nil, nil, nil).Search == nil {
		t.Error("search must be wired whenever the serper key is set")
	}
}

func TestEnableZaloToolsSkipsNonToolClient(t *testing.T) {
	p, s := &plainLLM{}, &noopSender{}
	svc := chat.NewServiceWithCompleter(p, s)
	enableZaloTools(svc, p, chat.Tools{})
	if err := svc.HandleAddressed(context.Background(), "c", 0, "hi"); err != nil || p.n != 1 || len(s.sent) != 1 {
		t.Fatalf("plain path must remain: n=%d sent=%v err=%v", p.n, s.sent, err)
	}
	enableZaloTools(nil, p, chat.Tools{}) // must not panic
}

type imgLLM struct{ msgs [][]llm.ChatMessage }

func (i *imgLLM) Complete(context.Context, string, []llm.Message, int) (string, error) {
	return "", nil
}
func (i *imgLLM) CompleteWithTools(_ context.Context, _ string, m []llm.ChatMessage, _ []llm.ToolDef, _ int) (llm.ToolResponse, error) {
	i.msgs = append(i.msgs, m)
	return llm.ToolResponse{Text: "ảnh ok"}, nil
}

func TestDispatchZaloImageRouting(t *testing.T) {
	names := []string{"Bot Pink EYN"}
	fetch := func(_ context.Context, url string) ([]byte, string, error) {
		if url != "https://img.test/a.png" {
			t.Errorf("fetch url = %q", url)
		}
		return []byte{1, 2, 3}, "image/png", nil
	}
	mk := func(chatType, caption string) zalobot.Message {
		m := zalobot.Message{Kind: "image", Photo: "https://img.test/a.png", Caption: caption}
		m.Chat.ID, m.Chat.ChatType = "c1", chatType
		return m
	}
	run := func(m zalobot.Message, f func(context.Context, string) ([]byte, string, error)) (*imgLLM, *noopSender) {
		p, s := &imgLLM{}, &noopSender{}
		svc := chat.NewServiceWithCompleter(p, s).WithTools(p, chat.Tools{})
		dispatchZaloWith(context.Background(), nil, svc, names, m, f)
		return p, s
	}
	// group image without caption is still answered; mention stripped from caption
	p, s := run(mk("GROUP", ""), fetch)
	if len(p.msgs) != 1 || len(s.sent) != 1 {
		t.Fatalf("group no caption: llm=%d sent=%v", len(p.msgs), s.sent)
	}
	p, _ = run(mk("GROUP", "@Bot Pink EYN đọc giúp"), fetch)
	if got := p.msgs[0][len(p.msgs[0])-1].Parts[0].Text; got != "đọc giúp" {
		t.Errorf("caption = %q", got)
	}
	// private image goes to chat even when the caption looks like a reminder
	p, _ = run(mk("PRIVATE", "nhắc tao 5 phút nữa"), fetch)
	if len(p.msgs) != 1 {
		t.Errorf("private image not routed to chat")
	}
	// no fetcher / no photo: ignored
	if p, s = run(mk("PRIVATE", "x"), nil); len(p.msgs) != 0 || len(s.sent) != 0 {
		t.Error("nil fetcher must ignore")
	}
}
