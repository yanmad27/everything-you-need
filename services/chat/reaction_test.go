package chat

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"everything-you-need/m/services/llm"
	telebot "everything-you-need/m/services/tele-bot"
	"golang.org/x/text/unicode/norm"
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
		// negation / idioms
		{"không buồn", ""}, {"đâu có buồn", ""}, {"chẳng buồn", ""}, {"buồn cười quá", "🤣"}, {"không buồn cười", ""},
		{"không buồn, cảm ơn nhé", "🙏"},
		// accent collisions
		{"yếu quá", ""}, {"tuyết rơi", ""}, {"buôn bán", ""}, {"yêu", "❤"}, {"tuyệt", "👏"},
		// repeated / adjacent exclusions
		{"yêu cầu yêu cầu", ""}, {"yêu cầu yêu cầu yêu cầu", ""}, {"yêu cầu rồi yêu em", "❤"},
		// decomposed (NFD) input
		{norm.NFD.String("yêu bạn"), "❤"}, {norm.NFD.String("yếu quá"), ""}, {norm.NFD.String("không buồn"), ""},
		{norm.NFD.String("cảm ơn"), "🙏"}, {norm.NFD.String("tuyết rơi"), ""},
	}
	for _, c := range cases {
		if got := chooseReaction(c.in); got != c.want {
			t.Errorf("chooseReaction(%q)=%q want %q", c.in, got, c.want)
		}
		if got := chooseReaction(c.in); got != "" && !telebot.AllowedReaction(got) {
			t.Errorf("%q returned non-whitelisted %q", c.in, got)
		}
	}
	for _, e := range emojiMap {
		if !telebot.AllowedReaction(e.out) {
			t.Errorf("emojiMap out %q not allowed", e.out)
		}
	}
	for _, r := range reactionRules {
		if !telebot.AllowedReaction(r.out) {
			t.Errorf("rule out %q not allowed", r.out)
		}
	}
	if telebot.AllowedReaction("🩷") || telebot.AllowedReaction("💗") {
		t.Error("pink heart must not be allowed")
	}
}

type reactSender struct {
	replies chan string
	started chan string // emoji, signalled when React is entered
	release chan struct{}
	done    chan error // React return value
	block   bool
	err     error
	calls   atomic.Int32
}

func newReactSender() *reactSender {
	return &reactSender{replies: make(chan string, 64), started: make(chan string, 8), release: make(chan struct{}), done: make(chan error, 8)}
}
func (r *reactSender) SendReply(_ context.Context, _ string, _ int64, text string) error {
	r.replies <- text
	return nil
}
func (r *reactSender) React(ctx context.Context, _ string, _ int64, emoji string) error {
	r.calls.Add(1)
	r.started <- emoji
	var err error
	if r.block {
		select {
		case <-r.release:
		case <-ctx.Done():
			err = ctx.Err()
		}
	}
	if r.err != nil {
		err = r.err
	}
	r.done <- err
	return err
}

func recv[T any](t *testing.T, ch chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(3 * time.Second):
		t.Fatalf("timeout waiting for %s", what)
		panic("unreachable")
	}
}

// The reply must be delivered while React is still blocked.
func TestReplySentWhileReactionBlocked(t *testing.T) {
	s := newReactSender()
	s.block = true
	svc := NewServiceWithCompleter(&fakeCompleter{}, s).WithReactions(true)
	if err := svc.HandleAddressed(context.Background(), "c", 7, "cảm ơn nhé"); err != nil {
		t.Fatal(err)
	}
	if e := recv(t, s.started, "React start"); e != "🙏" {
		t.Errorf("emoji %q", e)
	}
	// React is blocked right now (release not closed, done empty)
	select {
	case <-s.done:
		t.Fatal("React returned before release")
	default:
	}
	if got := recv(t, s.replies, "reply"); got != "ok" {
		t.Errorf("reply=%q", got)
	}
	close(s.release)
	if err := recv(t, s.done, "React join"); err != nil {
		t.Errorf("React err %v", err)
	}
}

func TestReactionErrorDoesNotAffectReply(t *testing.T) {
	s := newReactSender()
	s.err = errors.New("boom")
	svc := NewServiceWithCompleter(&fakeCompleter{}, s).WithReactions(true)
	if err := svc.HandleAddressed(context.Background(), "c", 7, "cảm ơn nhé"); err != nil {
		t.Fatalf("reply error: %v", err)
	}
	if got := recv(t, s.replies, "reply"); got != "ok" {
		t.Errorf("reply=%q", got)
	}
	recv(t, s.done, "React join")
}

func TestReactionDetachedContextTimesOut(t *testing.T) {
	old := reactionTimeout
	reactionTimeout = 40 * time.Millisecond
	defer func() { reactionTimeout = old }()
	s := newReactSender()
	s.block = true
	svc := NewServiceWithCompleter(&fakeCompleter{}, s).WithReactions(true)
	reqCtx, cancel := context.WithCancel(context.Background())
	if err := svc.HandleAddressed(reqCtx, "c", 7, "cảm ơn"); err != nil {
		t.Fatal(err)
	}
	cancel() // detached: request cancellation must not matter, only the own timeout
	recv(t, s.started, "React start")
	if err := recv(t, s.done, "React timeout"); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("want deadline exceeded, got %v", err)
	}
}

func TestNoReaction(t *testing.T) {
	cases := []struct {
		name    string
		on      bool
		id      int64
		text    string
		wake    bool // use Handle (needs wake word)
		noReply bool // message is ignored entirely
	}{
		{"toggle off", false, 7, "cảm ơn", false, false},
		{"nothing fits", true, 7, "giá vàng hôm nay?", false, false},
		{"no reply id", true, 0, "cảm ơn", false, false},
		{"ignored: no wake word", true, 7, "cảm ơn bạn", true, true},
		{"empty prompt", true, 7, "pink", true, false},
		{"clear command", true, 7, "xóa ngữ cảnh", false, false},
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
		if !c.noReply {
			recv(t, s.replies, c.name+" reply") // any reaction goroutine would be spawned before this returns
		}
		if n := s.calls.Load(); n != 0 || len(s.started) != 0 {
			t.Errorf("%s: unexpected reaction", c.name)
		}
	}
}

type plainSender struct{ replies chan string }

func (p *plainSender) SendReply(_ context.Context, _ string, _ int64, text string) error {
	p.replies <- text
	return nil
}

func TestNoReactionWhenSenderIsNotReactor(t *testing.T) {
	p := &plainSender{replies: make(chan string, 2)}
	svc := NewServiceWithCompleter(&fakeCompleter{}, p).WithReactions(true)
	if err := svc.HandleAddressed(context.Background(), "c", 7, "cảm ơn"); err != nil {
		t.Fatal(err)
	}
	recv(t, p.replies, "reply") // and no panic
}

type failCompleter struct{}

func (failCompleter) Complete(context.Context, string, []llm.Message, int) (string, error) {
	return "", errors.New("llm down")
}

func TestNoReactionWhenCompletionFails(t *testing.T) {
	s := newReactSender()
	svc := NewServiceWithCompleter(failCompleter{}, s).WithReactions(true)
	if err := svc.HandleAddressed(context.Background(), "c", 7, "cảm ơn"); err != nil {
		t.Fatal(err)
	}
	if got := recv(t, s.replies, "busy reply"); got == "" {
		t.Fatal("expected busy reply")
	}
	if s.calls.Load() != 0 || len(s.started) != 0 {
		t.Error("reaction fired on an error/busy turn")
	}
}

type toolStub struct{ llm.ToolCompleter }

func TestNoReactionWithToolClient(t *testing.T) {
	s := newReactSender()
	svc := NewServiceWithCompleter(&fakeCompleter{}, s).WithReactions(true)
	svc.toolClient = toolStub{}
	svc.maybeReact("c", 7, "cảm ơn")
	if s.calls.Load() != 0 || len(s.started) != 0 {
		t.Error("reaction fired on the tool (Zalo) path")
	}
}

func TestReactionRateLimitedPerChat(t *testing.T) {
	s := newReactSender()
	svc := NewServiceWithCompleter(&fakeCompleter{}, s).WithReactions(true)
	now := time.Unix(1000, 0)
	svc.reactNow = func() time.Time { return now }
	for i := 0; i < 20; i++ {
		if err := svc.HandleAddressed(context.Background(), "c", int64(i+1), "cảm ơn"); err != nil {
			t.Fatal(err)
		}
	}
	recv(t, s.started, "first reaction")
	for i := 0; i < 20; i++ {
		recv(t, s.replies, "reply")
	}
	if n := s.calls.Load(); n != 1 {
		t.Fatalf("burst produced %d reactions, want 1", n)
	}
	// another chat is independent
	if err := svc.HandleAddressed(context.Background(), "other", 1, "cảm ơn"); err != nil {
		t.Fatal(err)
	}
	recv(t, s.started, "other chat reaction")
	// after the gap the first chat may react again
	now = now.Add(reactionGap + time.Second)
	if err := svc.HandleAddressed(context.Background(), "c", 99, "cảm ơn"); err != nil {
		t.Fatal(err)
	}
	recv(t, s.started, "reaction after gap")
}

func TestReactAllowedPrunes(t *testing.T) {
	svc := &Service{}
	now := time.Unix(1000, 0)
	svc.reactNow = func() time.Time { return now }
	for i := 0; i < 600; i++ {
		svc.reactAllowed(string(rune('a'+i%26)) + string(rune(0x4e00+i)))
	}
	now = now.Add(time.Minute)
	svc.reactAllowed("fresh")
	if len(svc.reactLast) > 2 {
		t.Errorf("expired entries not pruned: %d", len(svc.reactLast))
	}
}
