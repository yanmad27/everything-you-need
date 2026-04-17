package reminder

import (
	"strings"
	"testing"
	"time"
)

func TestBuildGeminiRequestIncludesNowAndMessage(t *testing.T) {
	now := time.Date(2026, 4, 17, 14, 32, 0, 0, time.FixedZone("UTC+7", 7*60*60))
	body, err := buildGeminiRequest(now, "2h nữa nhắc tao ăn cơm")
	if err != nil {
		t.Fatalf("buildGeminiRequest: %v", err)
	}

	s := string(body)
	if !strings.Contains(s, "2026-04-17T14:32:00+07:00") {
		t.Errorf("request body missing NOW: %s", s)
	}
	if !strings.Contains(s, "ăn cơm") {
		t.Errorf("request body missing MESSAGE: %s", s)
	}
	if !strings.Contains(s, `"response_mime_type":"application/json"`) {
		t.Errorf("request must request JSON mime type: %s", s)
	}
}

func TestParseGeminiResponseSuccess(t *testing.T) {
	// Simulated Gemini response body; the model's text is nested inside candidates[0].content.parts[0].text.
	raw := []byte(`{
		"candidates": [{
			"content": {
				"parts": [{"text": "{\"ok\": true, \"when\": \"2026-04-17T21:00:00+07:00\", \"task\": \"ăn cơm\"}"}]
			}
		}]
	}`)

	got, err := parseGeminiResponse(raw)
	if err != nil {
		t.Fatalf("parseGeminiResponse: %v", err)
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

func TestParseGeminiResponseNotOK(t *testing.T) {
	raw := []byte(`{
		"candidates": [{
			"content": {
				"parts": [{"text": "{\"ok\": false, \"reason\": \"Thời điểm mơ hồ\"}"}]
			}
		}]
	}`)

	got, err := parseGeminiResponse(raw)
	if err != nil {
		t.Fatalf("parseGeminiResponse: %v", err)
	}
	if got.OK {
		t.Fatalf("OK: got true, want false")
	}
	if got.Reason == "" {
		t.Errorf("Reason must be populated when ok=false")
	}
}

func TestParseGeminiResponseMalformed(t *testing.T) {
	// No candidates → should error.
	raw := []byte(`{"candidates": []}`)
	if _, err := parseGeminiResponse(raw); err == nil {
		t.Errorf("expected error for empty candidates")
	}

	// Candidate text is not JSON → should error.
	raw = []byte(`{"candidates":[{"content":{"parts":[{"text":"hello world"}]}}]}`)
	if _, err := parseGeminiResponse(raw); err == nil {
		t.Errorf("expected error for non-JSON candidate text")
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
