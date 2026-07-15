package news

import (
	"strings"
	"testing"
	"time"
)

func TestFormatDigest(t *testing.T) {
	now := time.Date(2026, 6, 15, 20, 0, 0, 0, time.FixedZone("UTC+7", 7*60*60))
	ranked := []RankedItem{
		{
			URL:               "https://techcrunch.com/x",
			EnglishTitle:      "OpenAI ships model",
			EnglishSummary:    "A major release.",
			VietnameseTitle:   "OpenAI ra mắt mô hình",
			VietnameseSummary: "Bản phát hành lớn.",
		},
	}
	msg := FormatDigest(ranked, now, 6)
	for _, want := range []string{"Tin nổi bật", "OpenAI ra mắt mô hình", "Bản phát hành lớn.", "https://techcrunch.com/x", "6 nguồn"} {
		if !strings.Contains(msg, want) {
			t.Errorf("digest missing %q\n%s", want, msg)
		}
	}
	if len(msg) > telegramMaxLen {
		t.Errorf("message exceeds telegram limit: %d", len(msg))
	}
}

func TestFormatDigestTruncates(t *testing.T) {
	now := time.Now()
	long := strings.Repeat("x", 500)
	var ranked []RankedItem
	for i := 0; i < 50; i++ {
		ranked = append(ranked, RankedItem{URL: "https://e.com/" + long, EnglishTitle: long, EnglishSummary: long})
	}
	msg := FormatDigest(ranked, now, 6)
	if len(msg) > telegramMaxLen {
		t.Errorf("expected truncation to <= %d, got %d", telegramMaxLen, len(msg))
	}
}

func TestFormatFallback(t *testing.T) {
	now := time.Now()
	items := []FeedItem{
		{Title: "Story one", URL: "https://a.com/1"},
		{Title: "Story two", URL: "https://b.com/2"},
	}
	msg := FormatFallback(items, now)
	if !strings.Contains(msg, "Story one") || !strings.Contains(msg, "https://b.com/2") {
		t.Errorf("fallback missing content:\n%s", msg)
	}
	if !strings.Contains(msg, "AI tạm thời không khả dụng") {
		t.Errorf("fallback missing notice")
	}
}

func TestEscapeMarkdown(t *testing.T) {
	got := escapeMarkdown("a*b_c`d[e]")
	for _, c := range []string{"*", "_", "`", "[", "]"} {
		if strings.Contains(got, c) {
			t.Errorf("escapeMarkdown left %q in %q", c, got)
		}
	}
}
