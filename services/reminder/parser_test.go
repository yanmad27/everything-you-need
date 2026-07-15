package reminder

import (
	"strings"
	"testing"
	"time"
)

func TestBuildOpenAIRequestIncludesNowAndMessage(t *testing.T) {
	now := time.Date(2026, 4, 17, 14, 32, 0, 0, time.FixedZone("UTC+7", 7*60*60))
	body, err := buildOpenAIRequest("gpt-5", now, "2h nữa nhắc tao ăn cơm")
	if err != nil {
		t.Fatalf("buildOpenAIRequest: %v", err)
	}

	s := string(body)
	if !strings.Contains(s, "2026-04-17T14:32:00+07:00") {
		t.Errorf("request body missing NOW: %s", s)
	}
	if !strings.Contains(s, "ăn cơm") {
		t.Errorf("request body missing MESSAGE: %s", s)
	}
	if !strings.Contains(s, `"model":"gpt-5"`) {
		t.Errorf("request must include model: %s", s)
	}
}

func TestParseOpenAIResponseSuccess(t *testing.T) {
	// Simulated OpenAI response body; the model's text is nested inside choices[0].message.content.
	raw := []byte(`{
		"choices": [{
			"message": {"content": "{\"ok\": true, \"when\": \"2026-04-17T21:00:00+07:00\", \"task\": \"ăn cơm\"}"}
		}]
	}`)

	got, err := parseOpenAIResponse(raw)
	if err != nil {
		t.Fatalf("parseOpenAIResponse: %v", err)
	}
	if !got.OK {
		t.Fatalf("OK: got false, want true (reason=%q)", got.Reason)
	}
	if got.Task != "ăn cơm" {
		t.Errorf("Task: got %q", got.Task)
	}
	expectedWhen := time.Date(2026, 4, 17, 14, 0, 0, 0, time.UTC)
	if !got.When.Equal(expectedWhen) {
		t.Errorf("When: got %v want %v (UTC equivalent of 21:00+07:00)", got.When, expectedWhen)
	}
}

func TestParseOpenAIResponseNotOK(t *testing.T) {
	raw := []byte(`{
		"choices": [{
			"message": {"content": "{\"ok\": false, \"reason\": \"Thời điểm mơ hồ\"}"}
		}]
	}`)

	got, err := parseOpenAIResponse(raw)
	if err != nil {
		t.Fatalf("parseOpenAIResponse: %v", err)
	}
	if got.OK {
		t.Fatalf("OK: got true, want false")
	}
	if got.Reason == "" {
		t.Errorf("Reason must be populated when ok=false")
	}
}

func TestParseOpenAIResponseMalformed(t *testing.T) {
	// No choices → should error.
	raw := []byte(`{"choices": []}`)
	if _, err := parseOpenAIResponse(raw); err == nil {
		t.Errorf("expected error for empty choices")
	}

	// Message content is not JSON → should error.
	raw = []byte(`{"choices":[{"message":{"content":"hello world"}}]}`)
	if _, err := parseOpenAIResponse(raw); err == nil {
		t.Errorf("expected error for non-JSON message content")
	}
}

func TestKeywordPrefilter(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		{"2h nữa nhắc tao ăn cơm", true},
		{"19h ngày mai nhắc tôi họp", true},
		{"thứ 2 nhắc mình nộp báo cáo", true},
		{"nhắc ae đi họp 9h sáng", true},
		{"giá vàng hôm nay", false},
		{"hello world", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := MatchesReminderKeyword(tc.text); got != tc.want {
			t.Errorf("MatchesReminderKeyword(%q) = %v, want %v", tc.text, got, tc.want)
		}
	}
}
