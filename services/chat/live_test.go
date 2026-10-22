package chat

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"everything-you-need/m/services/llm"
)

type liveBox struct {
	mu sync.Mutex
	lv Live
	n  atomic.Int64
}

func (b *liveBox) get() Live  { b.n.Add(1); b.mu.Lock(); defer b.mu.Unlock(); return b.lv }
func (b *liveBox) set(l Live) { b.mu.Lock(); b.lv = l; b.mu.Unlock() }

func toolNames(defs []llm.ToolDef) string {
	var n []string
	for _, d := range defs {
		n = append(n, d.Name)
	}
	return strings.Join(n, ",")
}

func TestLiveSettingsApplyOnNextTurn(t *testing.T) {
	r := newRig(t, nil, textStep("a"), textStep("b"), textStep("c"), textStep("d"))
	box := &liveBox{}
	r.svc.WithLive(box.get)
	box.set(Live{})
	r.ask("hello")
	if !strings.Contains(toolNames(r.sc.tools[0]), "web_search") || !strings.Contains(toolNames(r.sc.tools[0]), "fetch_url") {
		t.Fatalf("baseline tools: %s", toolNames(r.sc.tools[0]))
	}
	box.set(Live{DisabledTools: map[string]bool{"web_search": true, "reminder_create": true}, Persona: "Bạn là TEST-PERSONA.", DueChats: []string{"other"}, FetchAllow: []string{"only.example.org"}})
	r.ask("hello again")
	names := toolNames(r.sc.tools[1])
	if strings.Contains(names, "web_search") || strings.Contains(names, "reminder_create") || strings.Contains(names, "due_dates") || !strings.Contains(names, "fetch_url") {
		t.Fatalf("tools after save: %s", names)
	}
	if !strings.Contains(r.sc.sys[1], "TEST-PERSONA") || strings.Contains(r.sc.sys[0], "TEST-PERSONA") {
		t.Fatal("persona not applied to the next turn only")
	}
	// built-in prompt is restored when persona is reset
	box.set(Live{})
	r.ask("third")
	if strings.Contains(r.sc.sys[2], "TEST-PERSONA") || r.sc.sys[2] != r.sc.sys[0] {
		t.Fatal("persona reset")
	}
}

func TestLiveClientModelSearchAndRate(t *testing.T) {
	r := newRig(t, nil, textStep("static"))
	alt := &scripted{steps: []func([]llm.ChatMessage, []llm.ToolDef) (llm.ToolResponse, error){
		toolStep(call("1", "web_search", `{"query":"x"}`)), textStep("alt-answer")}}
	altSearch := &fakeSearch{}
	r.svc.WithLive(func() Live { return Live{Client: alt, Model: "live-model", Search: altSearch, RatePerMin: 6, Burst: 1} })
	r.ask("search please")
	if len(r.sc.calls) != 0 || len(alt.calls) != 2 {
		t.Fatalf("static=%d alt=%d", len(r.sc.calls), len(alt.calls))
	}
	if altSearch.gotKind != "web" || r.srch.gotKind != "" {
		t.Fatalf("search override: alt=%v static=%v", altSearch.gotKind, r.srch.gotKind)
	}
	// burst resized to 1: the second immediate message is limited (per-chat state kept)
	r.ask("again")
	if len(alt.calls) != 2 {
		t.Fatalf("rate limit not resized: %d calls", len(alt.calls))
	}
}

func TestLiveImagesOffAndPaused(t *testing.T) {
	r := newRig(t, nil)
	r.svc.WithLive(func() Live { return Live{ImagesOff: true} })
	fetched := false
	err := r.svc.HandleImage(context.Background(), "chat-1234", "u1", "An", 0, "", func(context.Context) ([]byte, string, error) {
		fetched = true
		return nil, "", nil
	})
	if err != nil || fetched || len(r.snd.plain) != 1 || !strings.Contains(r.snd.plain[0], "tắt xử lý ảnh") {
		t.Fatalf("fetched=%v plain=%v err=%v", fetched, r.snd.plain, err)
	}
	// Paused is surfaced by CaptureLive; NotePaused records without any LLM call.
	r2 := newRig(t, nil)
	r2.svc.WithLive(func() Live { return Live{Paused: true} })
	_, lv := r2.svc.CaptureLive(context.Background())
	if !lv.Paused {
		t.Fatal("paused not captured")
	}
	r2.svc.NotePaused(context.Background(), "chat-1234", "u1", "An", "hi")
	if len(r2.sc.calls) != 0 || len(r2.snd.plain)+len(r2.snd.md) != 0 {
		t.Fatal("paused must not answer")
	}
}

func TestLiveOneSnapshotPerTurnAndConcurrentSave(t *testing.T) {
	r := newRig(t, nil)
	r.sc.steps = nil
	box := &liveBox{}
	r.svc.WithLive(box.get)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	go func() {
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			box.set(Live{Persona: "P" + string(rune('A'+i%20))})
		}
	}()
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = r.svc.HandleAddressedAs(context.Background(), "chat-"+string(rune('a'+i)), "u", "n", 0, "hi")
		}(i)
	}
	wg.Wait()
	close(stop)
	if box.n.Load() != 8 {
		t.Fatalf("expected exactly one capture per turn, got %d", box.n.Load())
	}
}

func TestLiveUnsetServiceIgnoresEverything(t *testing.T) {
	// Telegram-style service: no source → CaptureLive is a no-op.
	f, s := &fakeCompleter{}, &mdSender{}
	svc := NewServiceWithCompleter(f, s)
	ctx, lv := svc.CaptureLive(context.Background())
	if lv.Set || ctx != context.Background() {
		t.Fatal("no source must not capture")
	}
	_ = svc.HandleAddressed(context.Background(), "g", 0, "xin chào")
	if len(f.prompts) != 1 {
		t.Fatal("plain path broken")
	}
}
