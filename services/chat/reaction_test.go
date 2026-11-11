package chat

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestChooseReaction(t *testing.T) {
	cases := []struct{ in, want string }{
		{"cảm ơn bạn nhiều", "🙏"}, {"cám ơn nha", "🙏"}, {"thanks!", "🙏"}, {"tks", "🙏"},
		{"haha buồn cười quá", "🤣"}, {"hihi", "🤣"}, {"kkk", "🤣"}, {"😂", "🤣"}, {"🤣🤣", "🤣"},
		{"yêu bạn", "❤"}, {"❤️", "❤"}, {"🩷", "❤"}, {"giỏi quá", "👏"}, {"tuyệt vời", "👏"},
		{"hôm nay buồn quá", "😢"}, {"mệt quá", "😢"}, {"😢", "😢"}, {"😭", "😭"},
		{"trời ơi", "😱"}, {"thật á?", "😱"}, {"wow", "😱"}, {"không thể tin được", "🤯"},
		{"chúc mừng sinh nhật", "🎉"}, {"sinh nhật vui vẻ", "🎉"},
		{"giá vàng hôm nay bao nhiêu?", ""}, {"viết giúp tôi email", ""}, {"yêu cầu gì đó", ""},
		{"thanksgiving là gì", ""}, {"", ""},
	}
	for _, c := range cases {
		if got := chooseReaction(c.in); got != c.want {
			t.Errorf("chooseReaction(%q)=%q want %q", c.in, got, c.want)
		}
		if got := chooseReaction(c.in); got != "" && !allowedReactions[got] {
			t.Errorf("%q returned non-whitelisted %q", c.in, got)
		}
	}
	for _, e := range emojiMap {
		if !allowedReactions[e.out] {
			t.Errorf("emojiMap out %q not allowed", e.out)
		}
	}
	for _, r := range reactionRules {
		if !allowedReactions[r.out] {
			t.Errorf("rule out %q not allowed", r.out)
		}
	}
	if allowedReactions["🩷"] || allowedReactions["💗"] {
		t.Error("pink heart must not be allowed")
	}
}

type reactSender struct {
	replies chan string
	reacts  chan string
	react   func(ctx context.Context) error
	calls   atomic.Int32
}

func newReactSender() *reactSender {
	return &reactSender{replies: make(chan string, 4), reacts: make(chan string, 4)}
}
func (r *reactSender) SendReply(_ context.Context, _ string, _ int64, text string) error {
	r.replies <- text
	return nil
}
func (r *reactSender) React(ctx context.Context, _ string, _ int64, emoji string) error {
	r.calls.Add(1)
	r.reacts <- emoji
	if r.react != nil {
		return r.react(ctx)
	}
	return nil
}

func TestReactionFiresAndNeverBlocksReply(t *testing.T) {
	for name, fn := range map[string]func(context.Context) error{
		"blocks": func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() },
		"errors": func(context.Context) error { return errors.New("boom") },
	} {
		s := newReactSender()
		s.react = fn
		svc := NewServiceWithCompleter(&fakeCompleter{}, s).WithReactions(true)
		done := make(chan error, 1)
		go func() { done <- svc.HandleAddressed(context.Background(), "c", 7, "cảm ơn nhé") }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("%s: reply error %v", name, err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s: reply delayed by reaction", name)
		}
		if got := <-s.replies; got != "ok" {
			t.Errorf("%s: reply=%q", name, got)
		}
		select {
		case e := <-s.reacts:
			if e != "🙏" {
				t.Errorf("%s: emoji %q", name, e)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s: no reaction fired", name)
		}
	}
}

func TestNoReaction(t *testing.T) {
	cases := []struct {
		name string
		on   bool
		id   int64
		text string
		wake bool // use Handle (needs wake word)
	}{
		{"toggle off", false, 7, "cảm ơn", false},
		{"nothing fits", true, 7, "giá vàng hôm nay?", false},
		{"no reply id", true, 0, "cảm ơn", false},
		{"ignored: no wake word", true, 7, "cảm ơn bạn", true},
		{"empty prompt", true, 7, "pink", true},
		{"clear command", true, 7, "xóa ngữ cảnh", false},
	}
	for _, c := range cases {
		s := newReactSender()
		svc := NewServiceWithCompleter(&fakeCompleter{}, s).WithReactions(c.on)
		var err error
		if c.wake {
			err = svc.Handle(context.Background(), "c", c.id, c.text)
		} else {
			err = svc.HandleAddressed(context.Background(), "c", c.id, c.text)
		}
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		time.Sleep(20 * time.Millisecond) // let a wrongly-fired goroutine show up
		if n := s.calls.Load(); n != 0 {
			t.Errorf("%s: unexpected reaction (%d)", c.name, n)
		}
	}
}
