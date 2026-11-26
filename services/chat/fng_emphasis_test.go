package chat

import (
	"fmt"
	"strings"
	"testing"

	"everything-you-need/m/services/llm"
)

func TestFearGreedToolEmphasisOnlyAtExtremes(t *testing.T) {
	for _, c := range []struct {
		v    int
		warn bool
	}{{0, true}, {20, true}, {25, true}, {26, false}, {50, false}, {74, false}, {75, true}, {80, true}, {100, true}} {
		info := sampleFG
		info.Value = c.v
		r := newRig(t, withFG(fakeFG{info: info}), toolStep(call("1", "fear_greed_index", `{}`)), textStep("ok"))
		r.ask("fng")
		got := r.toolResult(1)
		if has := strings.Contains(got, "⚠️"); has != c.warn {
			t.Errorf("value %d: warning=%v want %v: %q", c.v, has, c.warn, got)
		}
	}
}

// echo plays a model that follows the tool's instruction and repeats the tool
// result; it shows what reaches the Zalo markdown transport.
func echoToolResult(msgs []llm.ChatMessage, _ []llm.ToolDef) (llm.ToolResponse, error) {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "tool" {
			return llm.ToolResponse{Text: msgs[i].Content, Assistant: llm.ChatMessage{Role: "assistant", Content: msgs[i].Content}}, nil
		}
	}
	return llm.ToolResponse{}, fmt.Errorf("no tool message")
}

func TestZaloDeliveryOfExtremeFearGreedAndVND(t *testing.T) {
	info := sampleFG
	info.Value = 20
	info.Class, info.ClassVN = "Extreme Fear", "Sợ hãi tột độ"
	r := newRig(t, func(tl *Tools) {
		withFG(fakeFG{info: info})(tl)
		withCrypto(fakeRate{26434, true})(tl)
	}, toolStep(call("1", "fear_greed_index", `{}`)), echoToolResult)
	r.ask("fng")
	r.snd.mu.Lock()
	defer r.snd.mu.Unlock()
	if len(r.snd.md) != 1 || !strings.Contains(r.snd.md[0], "⚠️ MỨC CỰC ĐOAN") || !strings.Contains(r.snd.md[0], "**...**") || len(r.snd.plain) != 0 {
		t.Fatalf("md=%q plain=%q", r.snd.md, r.snd.plain)
	}
	t.Logf("Zalo markdown payload (F&G 20):\n%s", r.snd.md[0])
}
