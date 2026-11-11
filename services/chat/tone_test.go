package chat

import (
	"strings"
	"testing"
)

func TestSystemPromptToneRules(t *testing.T) {
	lines := strings.Split(systemPrompt, "\n")
	if len(lines) != 5 {
		t.Fatalf("want 5 prompt lines, got %d", len(lines))
	}
	if strings.ContainsAny(lines[0], "🩷💗") {
		t.Errorf("name line must have no heart: %q", lines[0])
	}
	if !strings.Contains(lines[2], "emoji thật tiết kiệm") || !strings.Contains(lines[2], "không bao giờ dùng như chữ ký") {
		t.Errorf("missing sparing-emoji rule: %q", lines[2])
	}
	if !strings.HasPrefix(lines[3], "Nếu ảnh là thuốc") || !strings.HasPrefix(lines[4], "Khi tin nhắn có kèm") {
		t.Error("medicine/separator lines must stay in place")
	}
	if !strings.Contains(systemPrompt, "\nKhi tin nhắn có kèm") {
		t.Error("toolloop separator missing")
	}
}

func TestCannedStringsHaveNoHeart(t *testing.T) {
	for _, s := range []string{msgRateLimit, msgImagesOff} {
		if strings.ContainsAny(s, "🩷💗") {
			t.Errorf("canned string has heart: %q", s)
		}
	}
}

// TestPrintSampleStrings prints the prompt head and canned strings (go test -v -run Print).
func TestPrintSampleStrings(t *testing.T) {
	t.Log("systemPrompt head:\n" + strings.Join(strings.Split(systemPrompt, "\n")[:3], "\n"))
	t.Log("rate limit: " + msgRateLimit)
	t.Log("images off: " + msgImagesOff)
}
