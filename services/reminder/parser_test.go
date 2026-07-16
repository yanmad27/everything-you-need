package reminder

import (
	"testing"
	"time"
)

func TestParseReminderJSONSuccess(t *testing.T) {
	got, err := parseReminderJSON(`{"ok": true, "when": "2026-04-17T21:00:00+07:00", "task": "ăn cơm"}`)
	if err != nil {
		t.Fatalf("parseReminderJSON: %v", err)
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

func TestParseReminderJSONHandlesFences(t *testing.T) {
	// Model wraps the JSON in a markdown fence — extractJSONObject must strip it.
	got, err := parseReminderJSON("```json\n{\"ok\": true, \"when\": \"2026-04-17T21:00:00+07:00\", \"task\": \"họp\"}\n```")
	if err != nil {
		t.Fatalf("parseReminderJSON: %v", err)
	}
	if !got.OK || got.Task != "họp" {
		t.Errorf("got %+v", got)
	}
}

func TestParseReminderJSONNotOK(t *testing.T) {
	got, err := parseReminderJSON(`{"ok": false, "reason": "Thời điểm mơ hồ"}`)
	if err != nil {
		t.Fatalf("parseReminderJSON: %v", err)
	}
	if got.OK {
		t.Fatalf("OK: got true, want false")
	}
	if got.Reason == "" {
		t.Errorf("Reason must be populated when ok=false")
	}
}

func TestParseReminderJSONMalformed(t *testing.T) {
	if _, err := parseReminderJSON("hello world"); err == nil {
		t.Errorf("expected error for non-JSON content")
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
