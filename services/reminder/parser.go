package reminder

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"everything-you-need/m/services/llm"
)

// ParseResult is the normalized output of the NL parser.
type ParseResult struct {
	OK     bool
	When   time.Time // UTC, only valid if OK=true
	Task   string    // only valid if OK=true
	Reason string    // short Vietnamese explanation when OK=false
}

// Parser converts a raw Vietnamese reminder request into a resolved time + task.
type Parser interface {
	Parse(ctx context.Context, now time.Time, text string) (ParseResult, error)
}

var reminderKeywordRegex = regexp.MustCompile(`(?i)nhắc\s+(tao|tôi|mình|t|ae|mọi người|anh em)`)

// MatchesReminderKeyword runs the cheap prefilter used before calling the LLM.
func MatchesReminderKeyword(text string) bool {
	return reminderKeywordRegex.MatchString(text)
}

const systemInstruction = `You are a strict parser. Given a Vietnamese reminder request and the current time (Asia/Ho_Chi_Minh), return ONLY JSON, no prose.
Output shape:
  {"ok": true,  "when": "<ISO 8601 with +07:00 offset>", "task": "<short Vietnamese string>"}
  {"ok": false, "reason": "<short Vietnamese explanation>"}
Rules:
- "when" must be strictly in the future relative to NOW.
- Resolve relative phrases like "2h nữa", "19h mai", "thứ 2 tới" using NOW.
- "task" excludes the time phrase and the "nhắc tao/tôi/mình" prefix.
- If the user's time is ambiguous or already past, return ok=false.`

type reminderJSON struct {
	OK     bool   `json:"ok"`
	When   string `json:"when"`
	Task   string `json:"task"`
	Reason string `json:"reason"`
}

// extractJSONObject strips markdown fences / surrounding prose and returns the
// JSON object substring, tolerating models that wrap their output.
func extractJSONObject(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start >= 0 && end > start {
		return s[start : end+1]
	}
	return strings.TrimSpace(s)
}

// parseReminderJSON decodes the JSON object the model produced.
func parseReminderJSON(modelText string) (ParseResult, error) {
	text := extractJSONObject(modelText)
	var inner reminderJSON
	if err := json.Unmarshal([]byte(text), &inner); err != nil {
		return ParseResult{}, fmt.Errorf("decode reminder JSON %q: %w", text, err)
	}

	if !inner.OK {
		reason := inner.Reason
		if reason == "" {
			reason = "không xác định được thời điểm"
		}
		return ParseResult{OK: false, Reason: reason}, nil
	}

	when, err := time.Parse(time.RFC3339, inner.When)
	if err != nil {
		return ParseResult{}, fmt.Errorf("parse when %q: %w", inner.When, err)
	}
	return ParseResult{OK: true, When: when.UTC(), Task: strings.TrimSpace(inner.Task)}, nil
}

// LLMParser resolves reminder requests via the Anthropic Messages API.
type LLMParser struct {
	client *llm.Client
}

func NewOpenAIParser(token, model string, timeout time.Duration) *LLMParser {
	return &LLMParser{client: llm.NewClient(token, model, timeout)}
}

func (p *LLMParser) Parse(ctx context.Context, now time.Time, text string) (ParseResult, error) {
	userContent := fmt.Sprintf("NOW=%s\nMESSAGE=%s",
		now.Format("2006-01-02T15:04:05-07:00"), text)

	out, err := p.client.Complete(ctx, systemInstruction,
		[]llm.Message{{Role: "user", Content: userContent}}, 1024)
	if err != nil {
		return ParseResult{}, err
	}
	return parseReminderJSON(out)
}
