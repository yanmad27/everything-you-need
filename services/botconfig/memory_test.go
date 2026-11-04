package botconfig

import (
	"context"
	"testing"

	"everything-you-need/m/services/config"
)

func memProv(t *testing.T) *Provider {
	t.Helper()
	cfg := &config.Config{}
	cfg.LLMOpenAI.BaseURL = "https://api.example.com"
	cfg.LLMOpenAI.Model = "m"
	p, err := New(context.Background(), newStore(t), secretA, map[string]Defaults{PlatformZalo: ZaloDefaults(cfg, "")})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestMemoryDefaults(t *testing.T) {
	m := memProv(t).Snapshot(PlatformZalo).Zalo().Memory
	if !m.Enabled || m.MaxPerUser != 200 || m.RetentionDays != 365 {
		t.Fatalf("defaults: %+v", m)
	}
	if got := Specs(PlatformZalo); len(got) == 0 {
		t.Fatal("no specs")
	}
	for _, k := range []string{KeyToolMemory, KeyMemoryMaxPerUser, KeyMemoryRetentionDays} {
		if _, ok := specFor(PlatformZalo, k); !ok {
			t.Errorf("spec for %s missing", k)
		}
	}
}

func TestMemoryValidation(t *testing.T) {
	ctx := context.Background()
	p := memProv(t)
	bad := []map[string]string{
		{KeyMemoryMaxPerUser: "19"}, {KeyMemoryMaxPerUser: "501"}, {KeyMemoryMaxPerUser: "0"}, {KeyMemoryMaxPerUser: "abc"}, {KeyMemoryMaxPerUser: "-5"},
		{KeyMemoryRetentionDays: "29"}, {KeyMemoryRetentionDays: "1096"}, {KeyMemoryRetentionDays: "x"},
		{KeyToolMemory: "maybe"},
	}
	for _, c := range bad {
		if err := p.Save(ctx, PlatformZalo, c, "o"); err == nil {
			t.Errorf("accepted %v", c)
		}
	}
	if m := p.Snapshot(PlatformZalo).Zalo().Memory; m.MaxPerUser != 200 || m.RetentionDays != 365 || !m.Enabled {
		t.Fatalf("rejected save changed state: %+v", m)
	}
	good := map[string]string{KeyMemoryMaxPerUser: "20", KeyMemoryRetentionDays: "1095", KeyToolMemory: "false"}
	if err := p.Save(ctx, PlatformZalo, good, "o"); err != nil {
		t.Fatal(err)
	}
	m := p.Snapshot(PlatformZalo).Zalo().Memory
	if m.Enabled || m.MaxPerUser != 20 || m.RetentionDays != 1095 {
		t.Fatalf("%+v", m)
	}
	if err := p.Save(ctx, PlatformZalo, map[string]string{KeyMemoryMaxPerUser: "500", KeyMemoryRetentionDays: "30"}, "o"); err != nil {
		t.Fatal(err)
	}
	if err := p.Reset(ctx, PlatformZalo, KeyMemoryMaxPerUser, "o"); err != nil {
		t.Fatal(err)
	}
	if m := p.Snapshot(PlatformZalo).Zalo().Memory; m.MaxPerUser != 200 || m.RetentionDays != 30 {
		t.Fatalf("after reset: %+v", m)
	}
}

func TestFearGreedToolKey(t *testing.T) {
	if !memProv(t).Snapshot(PlatformZalo).Zalo().Tools.FearGreed {
		t.Fatal("tools.fear_greed must default to true like the other read-only data tools")
	}
	if _, ok := specFor(PlatformZalo, KeyToolFearGreed); !ok || KeyToolFearGreed != "tools.fear_greed" {
		t.Fatal("spec missing")
	}
}
