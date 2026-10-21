package chat

import (
	"context"
	"errors"
	"sync"
	"testing"

	"everything-you-need/m/services/llm"
)

type convoRec struct {
	recRecorder
	cmu    sync.Mutex
	convos []ConvoEvent
}

func (c *convoRec) RecordConvo(_ context.Context, e ConvoEvent) {
	c.cmu.Lock()
	c.convos = append(c.convos, e)
	c.cmu.Unlock()
}

func newConvoRig(t *testing.T, steps ...func([]llm.ChatMessage, []llm.ToolDef) (llm.ToolResponse, error)) (*rig, *convoRec) {
	cr := &convoRec{}
	r := newRig(t, func(tl *Tools) { tl.Recorder = cr; tl.Model = "m1" }, steps...)
	return r, cr
}

func TestConvoRecordsTurn(t *testing.T) {
	r, cr := newConvoRig(t, toolStep(call("1", "lunar_date", `{}`)), textStep("xong"))
	ctx := WithMeta(context.Background(), Meta{ChatType: "GROUP"})
	_ = r.svc.HandleAddressedAs(ctx, "chat-1234", "u1", "An", 0, "hôm nay âm lịch?")
	if len(cr.convos) != 2 {
		t.Fatalf("convos = %+v", cr.convos)
	}
	in, out := cr.convos[0], cr.convos[1]
	if in.Direction != "in" || in.Kind != "text" || in.UserID != "u1" || in.UserName != "An" || in.ChatType != "GROUP" || in.Platform != "zalo" {
		t.Errorf("in = %+v", in)
	}
	if out.Direction != "out" || out.Text != "xong" || out.ErrTag != "" {
		t.Errorf("out = %+v", out)
	}
	if in.TurnID == "" || in.TurnID != out.TurnID || len(cr.tools) != 1 || cr.tools[0].TurnID != in.TurnID || cr.msgs[0].TurnID != in.TurnID {
		t.Errorf("turn ids: in=%q out=%q tools=%+v usage=%+v", in.TurnID, out.TurnID, cr.tools, cr.msgs)
	}
	if cr.tools[0].UserID != "u1" || cr.msgs[0].Model != "m1" {
		t.Errorf("tool/usage meta: %+v %+v", cr.tools[0], cr.msgs[0])
	}
}

func TestConvoRecordsFailureAndRateLimitAsSystem(t *testing.T) {
	r, cr := newConvoRig(t, func([]llm.ChatMessage, []llm.ToolDef) (llm.ToolResponse, error) {
		return llm.ToolResponse{}, errors.New("HTTP 500 secret body")
	})
	r.ask("hi")
	if len(cr.convos) != 2 || cr.convos[1].Kind != "system" || cr.convos[1].ErrTag != "status=500" {
		t.Fatalf("convos = %+v", cr.convos)
	}
	// exhaust the limiter: next asks are rate limited, first one gets the notice.
	for i := 0; i < 10; i++ {
		r.ask("again")
	}
	notices := 0
	for _, c := range cr.convos {
		if c.ErrTag == "rate_limited" && c.Kind == "system" {
			notices++
		}
	}
	if notices != 1 {
		t.Errorf("rate-limit notices recorded = %d", notices)
	}
	limited := 0
	for _, c := range cr.convos {
		if c.Direction == "in" && c.ErrTag == "rate_limited" {
			limited++
			if c.Text != "" {
				t.Errorf("rate-limited text stored: %q", c.Text)
			}
		}
	}
	if limited == 0 {
		t.Error("rate-limited inbound not counted")
	}
	for _, c := range cr.convos {
		if c.Direction == "in" && c.ErrTag == "" && c.Text == "" {
			t.Error("accepted inbound lost its text")
		}
	}
}

func TestConvoImageMetadataOnly(t *testing.T) {
	r, cr := newConvoRig(t, textStep("ảnh đẹp"))
	if err := r.svc.HandleImage(context.Background(), "chat-1234", "u1", "An", 0, "xem giúp", okImg(10)); err != nil {
		t.Fatal(err)
	}
	if len(cr.convos) != 2 {
		t.Fatalf("convos = %+v", cr.convos)
	}
	in := cr.convos[0]
	if in.Kind != "image" || in.ImageMime != "image/png" || in.ImageBytes != 10 || in.Text != "xem giúp" {
		t.Errorf("in = %+v", in)
	}
}

func TestConvoImageFetchErrorIsSystem(t *testing.T) {
	r, cr := newConvoRig(t)
	bad := func(context.Context) ([]byte, string, error) {
		return nil, "", errors.New("boom https://x/y.jpg?sig=abc")
	}
	_ = r.svc.HandleImage(context.Background(), "chat-1234", "u1", "An", 0, "", bad)
	if len(cr.convos) != 2 || cr.convos[1].Kind != "system" || cr.convos[1].ErrTag != "image_fetch_failed" {
		t.Fatalf("convos = %+v", cr.convos)
	}
	for _, c := range cr.convos {
		if c.ImageBytes != 0 || c.Text == "boom" {
			t.Errorf("unexpected: %+v", c)
		}
	}
}

func TestConvoPanicNeverFailsReply(t *testing.T) {
	r := newRig(t, func(tl *Tools) { tl.Recorder = panicConvo{} }, textStep("ok"))
	r.ask("hi")
	if len(r.snd.md) != 1 {
		t.Error("reply lost")
	}
}

type panicConvo struct{ panicRecorder }

func (panicConvo) RecordConvo(context.Context, ConvoEvent) { panic("x") }

func TestConvoTelegramNotRecorded(t *testing.T) {
	cr := &convoRec{}
	f, s := &fakeCompleter{}, &mdSender{}
	svc := NewServiceWithCompleter(f, s) // Telegram: no tools, so no recorder
	_ = svc.HandleAddressedAs(context.Background(), "g", "u", "An", 0, "xin chào")
	if len(cr.convos) != 0 || len(cr.tools) != 0 || len(cr.msgs) != 0 {
		t.Errorf("recorded: %+v", cr)
	}
	if svc.tools.Recorder != nil {
		t.Error("telegram service must have no recorder")
	}
}
