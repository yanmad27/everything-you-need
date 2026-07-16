package chat

import "testing"

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
		{"pinky ơi", "", false},   // not a wake word
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
