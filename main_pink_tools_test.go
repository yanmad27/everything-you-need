package main

import (
	"context"
	"testing"

	"everything-you-need/m/services/chat"
	"everything-you-need/m/services/config"
	"everything-you-need/m/services/llm"
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
