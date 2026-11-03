package chat

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeFG struct {
	info FearGreedInfo
	err  error
}

func (f fakeFG) Get(context.Context) (FearGreedInfo, error) { return f.info, f.err }

var sampleFG = FearGreedInfo{Value: 25, Class: "Fear", ClassVN: "Sợ hãi", Date: time.Date(2025, 11, 3, 0, 0, 0, 0, time.UTC), HasChange: true, Change: -3}

func withFG(fg FearGreedReader) func(*Tools) { return func(tl *Tools) { tl.FearGreed = fg } }

func TestFearGreedToolRegisteredAndOutput(t *testing.T) {
	r := newRig(t, withFG(fakeFG{info: sampleFG}), toolStep(call("1", "fear_greed_index", `{}`)), textStep("ok"))
	r.ask("fear greed hôm nay?")
	if !strings.Contains(toolNames(r.sc.tools[0]), "fear_greed_index") {
		t.Fatalf("not registered: %s", toolNames(r.sc.tools[0]))
	}
	got := r.toolResult(1)
	for _, w := range []string{"25/100", "Sợ hãi (Fear)", "2025-11-03", "-3"} {
		if !strings.Contains(got, w) {
			t.Errorf("result missing %q: %q", w, got)
		}
	}
	t.Logf("sample tool call: fear_greed_index {} ->\n%s", got)
}

func TestFearGreedToolMissingYesterday(t *testing.T) {
	i := sampleFG
	i.HasChange = false
	r := newRig(t, withFG(fakeFG{info: i}), toolStep(call("1", "fear_greed_index", `{}`)), textStep("ok"))
	r.ask("fng")
	if got := r.toolResult(1); !strings.Contains(got, "không có dữ liệu") {
		t.Errorf("%q", got)
	}
}

func TestFearGreedToolAbsentWhenNilOrDisabled(t *testing.T) {
	r := newRig(t, nil, textStep("a"))
	r.ask("hi")
	if strings.Contains(toolNames(r.sc.tools[0]), "fear_greed_index") {
		t.Fatal("registered without a reader")
	}
	r = newRig(t, withFG(fakeFG{info: sampleFG}), textStep("a"), textStep("b"))
	box := &liveBox{}
	r.svc.WithLive(box.get)
	box.set(Live{DisabledTools: map[string]bool{"fear_greed_index": true}})
	r.ask("hi")
	names := toolNames(r.sc.tools[0])
	if strings.Contains(names, "fear_greed_index") || !strings.Contains(names, "get_prices") {
		t.Fatalf("per-bot toggle ignored: %s", names)
	}
}

func TestFearGreedToolErrorIsSanitized(t *testing.T) {
	r := newRig(t, withFG(fakeFG{err: errors.New("secret-internal")}), toolStep(call("1", "fear_greed_index", `{}`)), textStep("ok"))
	r.ask("fng")
	if got := r.toolResult(1); !strings.Contains(got, "error") || strings.Contains(got, "secret-internal") {
		t.Errorf("%q", got)
	}
}

// Public data: it must not mark the turn private, so web tools stay usable
// afterwards (the exfiltration guard is unchanged).
func TestFearGreedToolDoesNotSetPrivate(t *testing.T) {
	r := newRig(t, withFG(fakeFG{info: sampleFG}), toolStep(call("1", "fear_greed_index", `{}`)),
		toolStep(call("2", "fetch_url", `{"url":"https://github.com/trending"}`)), textStep("ok"))
	r.ask("hi")
	if got := lastTool(r.sc.calls[2]); !strings.Contains(got, "page of https://github.com/trending") {
		t.Fatalf("fetch refused after fear_greed_index: %q", got)
	}
	for _, ti := range r.svc.buildTools() {
		if ti.def.Name == "fear_greed_index" && (ti.sensitive || ti.taints) {
			t.Fatalf("fear_greed_index must be neither sensitive nor tainting: %+v", ti)
		}
	}
}

// The model-driven fetch_url allowlist is untouched: the index host is fetched
// server-side only.
func TestFetchAllowlistExcludesAlternativeMe(t *testing.T) {
	for _, d := range defaultFetchAllow {
		if strings.Contains(d, "alternative.me") {
			t.Fatalf("defaultFetchAllow widened: %v", defaultFetchAllow)
		}
	}
	if len(defaultFetchAllow) != 12 {
		t.Fatalf("defaultFetchAllow changed: %v", defaultFetchAllow)
	}
}
