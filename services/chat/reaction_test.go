package chat

import (
	"context"
	"errors"
	"strconv"
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
		{"yêu bạn", "❤"}, {"love you", "❤"}, {"quá đỉnh", "👏"}, {"❤️", "❤"}, {"🩷", "❤"}, {"giỏi quá", "👏"}, {"tuyệt vời", "👏"},
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

// decisions records admission decisions synchronously.
type decisions struct{ got []string }

func (d *decisions) hook(r string) { d.got = append(d.got, r) }

func TestAdmissionDecisions(t *testing.T) {
	cases := []struct {
		name string
		on   bool
		id   int64
		text string
		tool bool
		want string
	}{
		{"toggle off", false, 7, "cảm ơn", false, "skipped:off"},
		{"nothing fits", true, 7, "giá vàng hôm nay?", false, "skipped:no-emoji"},
		{"no reply id", true, 0, "cảm ơn", false, "skipped:no-msg-id"},
		{"tool client", true, 7, "cảm ơn", true, "skipped:tool-client"},
		{"admitted", true, 7, "cảm ơn", false, "admitted"},
	}
	for _, c := range cases {
		s := newReactSender()
		svc := NewServiceWithCompleter(&fakeCompleter{}, s).WithReactions(c.on)
		if c.tool {
			svc.toolClient = toolStub{}
		}
		d := &decisions{}
		svc.reactHook = d.hook
		svc.maybeReact("c", c.id, c.text)
		if len(d.got) != 1 || d.got[0] != c.want {
			t.Errorf("%s: decision %v want %q", c.name, d.got, c.want)
		}
		if c.want == "admitted" {
			recv(t, s.started, "React start")
			recv(t, s.done, "React join")
		}
	}
}

// Ignored (no wake word), clear and empty-prompt turns never reach the hook.
func TestNoDecisionOnIgnoredClearEmptyAndFailedSend(t *testing.T) {
	s := newReactSender()
	svc := NewServiceWithCompleter(&fakeCompleter{}, s).WithReactions(true)
	d := &decisions{}
	svc.reactHook = d.hook
	ctx := context.Background()
	_ = svc.Handle(ctx, "c", 7, "cảm ơn bạn") // no wake word
	_ = svc.Handle(ctx, "c", 7, "pink")       // empty prompt
	_ = svc.HandleAddressed(ctx, "c", 7, "xóa ngữ cảnh")
	if len(d.got) != 0 {
		t.Errorf("decisions on non-answer turns: %v", d.got)
	}
	// failed send: the reply errors, so no decision (no admission) is made
	fs := &failSender{}
	svc2 := NewServiceWithCompleter(&fakeCompleter{}, fs).WithReactions(true)
	d2 := &decisions{}
	svc2.reactHook = d2.hook
	if err := svc2.HandleAddressed(ctx, "c", 7, "cảm ơn"); err == nil {
		t.Fatal("want send error")
	}
	if len(d2.got) != 0 {
		t.Errorf("failed send produced decision %v", d2.got)
	}
}

type failSender struct{ reactSender }

func (f *failSender) SendReply(context.Context, string, int64, string) error {
	return errors.New("send failed")
}

type plainSender struct{ replies chan string }

func (p *plainSender) SendReply(_ context.Context, _ string, _ int64, text string) error {
	p.replies <- text
	return nil
}

func TestNoReactionWhenSenderIsNotReactor(t *testing.T) {
	p := &plainSender{replies: make(chan string, 2)}
	svc := NewServiceWithCompleter(&fakeCompleter{}, p).WithReactions(true)
	d := &decisions{}
	svc.reactHook = d.hook
	if err := svc.HandleAddressed(context.Background(), "c", 7, "cảm ơn"); err != nil {
		t.Fatal(err)
	}
	recv(t, p.replies, "reply")
	if len(d.got) != 1 || d.got[0] != "skipped:not-reactor" {
		t.Errorf("decision %v", d.got)
	}
}

type failCompleter struct{}

func (failCompleter) Complete(context.Context, string, []llm.Message, int) (string, error) {
	return "", errors.New("llm down")
}

func TestNoReactionWhenCompletionFails(t *testing.T) {
	s := newReactSender()
	svc := NewServiceWithCompleter(failCompleter{}, s).WithReactions(true)
	d := &decisions{}
	svc.reactHook = d.hook
	if err := svc.HandleAddressed(context.Background(), "c", 7, "cảm ơn"); err != nil {
		t.Fatal(err)
	}
	if got := recv(t, s.replies, "busy reply"); got == "" {
		t.Fatal("expected busy reply")
	}
	if len(d.got) != 0 {
		t.Errorf("decision on an error/busy turn: %v", d.got)
	}
}

type toolStub struct{ llm.ToolCompleter }

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

func TestReactionMapHardCap(t *testing.T) {
	svc := &Service{}
	now := time.Unix(1000, 0)
	svc.reactNow = func() time.Time { return now }
	admitted := 0
	for i := 0; i < 600; i++ {
		if svc.reactAllowed("chat-" + strconv.Itoa(i)) {
			admitted++
		}
	}
	if admitted > reactionMaxChats || len(svc.reactLast) > reactionMaxChats {
		t.Fatalf("admitted=%d map=%d, cap %d", admitted, len(svc.reactLast), reactionMaxChats)
	}
	// a known chat keeps the normal gap rule even when full
	if svc.reactAllowed("chat-0") {
		t.Error("known chat within gap admitted")
	}
	if svc.reactAllowed("brand-new") {
		t.Error("new chat admitted while full")
	}
	now = now.Add(reactionGap + time.Second)
	if !svc.reactAllowed("brand-new") {
		t.Error("new chat refused after the gap passed")
	}
	if len(svc.reactLast) > reactionMaxChats {
		t.Errorf("map %d over cap after prune", len(svc.reactLast))
	}
}
