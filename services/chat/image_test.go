package chat

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"everything-you-need/m/services/llm"
)

func okImg(n int) ImageFetcher {
	return func(context.Context) ([]byte, string, error) { return bytes.Repeat([]byte{1}, n), "image/png", nil }
}

func imgParts(t *testing.T, r *rig) (text, url string) {
	t.Helper()
	m := r.sc.calls[0][len(r.sc.calls[0])-1]
	if len(m.Parts) != 2 || m.Parts[0].Type != "text" || m.Parts[1].Type != "image_url" {
		t.Fatalf("parts = %+v", m.Parts)
	}
	return m.Parts[0].Text, m.Parts[1].ImageURL
}

func TestImageWithCaption(t *testing.T) {
	r := newRig(t, nil, textStep("thấy rồi"))
	if err := r.svc.HandleImage(context.Background(), "chat-1234", "u", "An", 0, "  giá  bao nhiêu? ", okImg(10)); err != nil {
		t.Fatal(err)
	}
	text, url := imgParts(t, r)
	if text != "giá bao nhiêu?" || !strings.HasPrefix(url, "data:image/png;base64,") {
		t.Errorf("text=%q url=%.30s", text, url)
	}
	if len(r.snd.md) != 1 {
		t.Errorf("reply: %v", r.snd.md)
	}
	// history holds a placeholder only, never bytes
	for _, m := range r.svc.history["chat-1234"] {
		if strings.Contains(m.Content, "base64") || (m.Role == "user" && m.Content != "[ảnh] giá bao nhiêu?") {
			t.Errorf("history: %+v", m)
		}
	}
}

func TestImageUsesRecentQuestionElseDescribe(t *testing.T) {
	now := time.Date(2026, 10, 21, 9, 0, 0, 0, ictZone)
	r := newRig(t, func(tl *Tools) { tl.Now = func() time.Time { return now } }, textStep("a"), textStep("b"), textStep("c"), textStep("d"))
	ask := func(c string) {
		_ = r.svc.HandleImage(context.Background(), "chat-1234", "u1", "An", 0, c, okImg(5))
	}
	r.ask("đây là bảng giá gì vậy?")
	now = now.Add(time.Minute)
	ask("")
	last := func() string { m := r.sc.calls[len(r.sc.calls)-1]; return m[len(m)-1].Parts[0].Text }
	if last() != "đây là bảng giá gì vậy?" {
		t.Errorf("recent question not used: %q", last())
	}
	now = now.Add(3 * time.Minute) // stale
	ask("")
	if last() != describePrompt {
		t.Errorf("stale question used: %q", last())
	}
	r.ask("cảm ơn nhé") // not a question
	ask("")
	if last() != describePrompt {
		t.Errorf("non-question used: %q", last())
	}
}

func TestImageFetchErrorAndTooLargeNoLLM(t *testing.T) {
	r := newRig(t, nil)
	_ = r.svc.HandleImage(context.Background(), "c", "u", "", 0, "x", func(context.Context) ([]byte, string, error) {
		return nil, "", errors.New("get https://img.test/secret/path?token=abc: boom")
	})
	if len(r.sc.calls) != 0 || len(r.snd.plain) != 1 || !strings.Contains(r.snd.plain[0], "không tải được ảnh") {
		t.Fatalf("fetch error: calls=%d plain=%v", len(r.sc.calls), r.snd.plain)
	}
	_ = r.svc.HandleImage(context.Background(), "c", "u", "", 0, "x", okImg(6<<20))
	if len(r.sc.calls) != 0 || len(r.snd.plain) != 2 || !strings.Contains(r.snd.plain[1], "lớn quá") {
		t.Fatalf("too large: calls=%d plain=%v", len(r.sc.calls), r.snd.plain)
	}
}

func TestImageRoundCapAndNoURLInLogs(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)
	steps := []func([]llm.ChatMessage, []llm.ToolDef) (llm.ToolResponse, error){
		toolStep(call("1", "lunar_date", `{}`)), toolStep(call("2", "lunar_date", `{}`)), toolStep(call("3", "lunar_date", `{}`)), textStep("final"),
	}
	r := newRig(t, nil, steps...)
	_ = r.svc.HandleImage(context.Background(), "chat-1234", "u", "", 0, "hi", func(context.Context) ([]byte, string, error) {
		return nil, "", errors.New("https://img.test/PLANTED/path?sig=SECRET")
	})
	_ = r.svc.HandleImage(context.Background(), "chat-1234", "u", "", 0, "hi", okImg(5))
	if len(r.sc.calls) != 3 || r.sc.tools[2] != nil { // 2 tool rounds + tool-less final
		t.Errorf("image turn calls=%d", len(r.sc.calls))
	}
	out := buf.String()
	if strings.Contains(out, "PLANTED") || strings.Contains(out, "SECRET") || strings.Contains(out, "img.test") {
		t.Errorf("url leaked into logs:\n%s", out)
	}
	if !strings.Contains(out, "chat image: kind=image bytes=5 mime=image/png outcome=ok") || !strings.Contains(out, "outcome=fetch_error reason=failed") {
		t.Errorf("image log lines missing:\n%s", out)
	}
}

func TestImageReminderStillGuarded(t *testing.T) {
	r := newRig(t, nil, toolStep(call("c", "reminder_create", `{"task":"x","when":"2026-10-21T19:00:00+07:00"}`)), textStep("ok"))
	_ = r.svc.HandleImage(context.Background(), "chat-1234", "u", "", 0, "", okImg(5)) // no reminder wording
	if len(r.rem.creates) != 0 {
		t.Error("reminder created from an image without intent")
	}
}

func TestImageWithoutToolsPath(t *testing.T) {
	s := &mdSender{}
	svc := NewServiceWithCompleter(&fakeCompleter{}, s)
	_ = svc.HandleImage(context.Background(), "c", "", "", 0, "", okImg(1))
	if len(s.plain) != 1 {
		t.Errorf("plain=%v", s.plain)
	}
}

func TestRecentQuestionKeyedByUserClearedAndPruned(t *testing.T) {
	now := time.Date(2026, 10, 21, 9, 0, 0, 0, ictZone)
	r := newRig(t, func(tl *Tools) { tl.Now = func() time.Time { return now } })
	ctx := context.Background()
	_ = r.svc.HandleAddressedAs(ctx, "g", "alice", "", 0, "cái này là gì?")
	if r.svc.recentQuestion("g", "bob") != "" || r.svc.recentQuestion("g", "alice") == "" {
		t.Error("question must be keyed by chat+user")
	}
	_ = r.svc.HandleAddressedAs(ctx, "g", "alice", "", 0, "xóa ngữ cảnh")
	if r.svc.recentQuestion("g", "alice") != "" {
		t.Error("clear command must drop remembered text")
	}
	_ = r.svc.HandleAddressedAs(ctx, "g", "alice", "", 0, "cái này là gì?")
	now = now.Add(11 * time.Minute)
	r.svc.noteUserText("g2", "carol", "x?")
	if len(r.svc.last) != 1 {
		t.Errorf("old entries not pruned: %d", len(r.svc.last))
	}
}

func TestProviderErrorTextNotLogged(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)
	r := newRig(t, nil, func([]llm.ChatMessage, []llm.ToolDef) (llm.ToolResponse, error) {
		return llm.ToolResponse{}, errors.New("openai-compat HTTP 429: PROVIDER-SECRET-DETAIL")
	})
	r.ask("hi")
	if out := buf.String(); strings.Contains(out, "PROVIDER-SECRET") || !strings.Contains(out, "status=429") {
		t.Errorf("log: %s", out)
	}
}
