package chat

import (
	"context"
	"testing"

	"everything-you-need/m/services/llm"
)

func TestSplitWake(t *testing.T) {
	cases := []struct {
		in   string
		rest string
		ok   bool
	}{
		{"pink hôm nay ăn gì ngon?", "hôm nay ăn gì ngon?", true},
		{"Pink, dịch câu này giúp", "dịch câu này giúp", true},
		{"trợ lý ơi kể chuyện cười", "ơi kể chuyện cười", true},
		{"Trợ Lý giúp tôi với", "giúp tôi với", true},
		{"troly: 1+1 bằng mấy", "1+1 bằng mấy", true},
		{"pink", "", true},
		{"hi pink", "hi", true}, // wake word at the end
		{"nay là thứ mấy pink", "nay là thứ mấy", true},  // at the end
		{"nay là thứ mấy pink?", "nay là thứ mấy", true}, // end + punctuation
		{"ê pink giúp tao với", "ê giúp tao với", true},  // middle
		{"pinky ơi", "", false},                          // not a wake word
		{"2h nữa nhắc tao ăn cơm", "", false},
		{"chào mọi người", "", false},
	}
	for _, c := range cases {
		rest, ok := SplitWake(c.in)
		if ok != c.ok {
			t.Errorf("SplitWake(%q) ok=%v want %v", c.in, ok, c.ok)
			continue
		}
		if ok && rest != c.rest {
			t.Errorf("SplitWake(%q) rest=%q want %q", c.in, rest, c.rest)
		}
	}
}

func TestIsClearCommand(t *testing.T) {
	clear := []string{"clear", "/clear", "reset", "clear context", "quên đi", "quên hết", "xoá ngữ cảnh", "xóa lịch sử", "new chat", "làm mới", "bắt đầu lại"}
	for _, s := range clear {
		if !IsClearCommand(s) {
			t.Errorf("IsClearCommand(%q) = false, want true", s)
		}
	}
	notClear := []string{"quên mật khẩu thì sao", "2+2 bằng mấy", "reset lại router thế nào", "clearance là gì", ""}
	for _, s := range notClear {
		if IsClearCommand(s) {
			t.Errorf("IsClearCommand(%q) = true, want false", s)
		}
	}
}

func TestHTMLToText(t *testing.T) {
	in := `<html><head><style>.x{color:red}</style><script>var a=1;</script></head>` +
		`<body><h1>Bảo hiểm thai sản</h1><p>Quyền lợi &amp; chi phí:</p><ul><li>Khám thai</li></ul></body></html>`
	got := htmlToText(in)
	if want := "Bảo hiểm thai sản Quyền lợi & chi phí: Khám thai"; got != want {
		t.Errorf("htmlToText = %q, want %q", got, want)
	}
}

type fakeCompleter struct{ prompts []string }

func (f *fakeCompleter) Complete(_ context.Context, _ string, msgs []llm.Message, _ int) (string, error) {
	f.prompts = append(f.prompts, msgs[len(msgs)-1].Content)
	return "ok", nil
}

type recSender struct{ n int }

func (r *recSender) SendReply(context.Context, string, int64, string) error { r.n++; return nil }

func TestHandleAddressedSkipsWakeWordButHandleRequiresIt(t *testing.T) {
	f, s := &fakeCompleter{}, &recSender{}
	svc := NewServiceWithCompleter(f, s)
	ctx := context.Background()

	if err := svc.Handle(ctx, "g", 0, "hôm nay trời sao"); err != nil || s.n != 0 {
		t.Fatalf("Handle without wake word must stay silent: n=%d err=%v", s.n, err)
	}
	if err := svc.HandleAddressed(ctx, "g", 0, "hôm nay trời sao"); err != nil || s.n != 1 {
		t.Fatalf("HandleAddressed must answer: n=%d err=%v", s.n, err)
	}
	if f.prompts[0] != "hôm nay trời sao" {
		t.Errorf("prompt = %q", f.prompts[0])
	}
	_ = svc.HandleAddressed(ctx, "g", 0, "pink xóa ngữ cảnh")
	if len(f.prompts) != 1 { // clear command: no LLM call
		t.Errorf("clear command should not call the model: %v", f.prompts)
	}
}
