package reminder

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
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

const openAISystemInstruction = `You are a strict parser. Given a Vietnamese reminder request and the current time (Asia/Ho_Chi_Minh), return ONLY JSON, no prose.
Output shape:
  {"ok": true,  "when": "<ISO 8601 with +07:00 offset>", "task": "<short Vietnamese string>"}
  {"ok": false, "reason": "<short Vietnamese explanation>"}
Rules:
- "when" must be strictly in the future relative to NOW.
- Resolve relative phrases like "2h nữa", "19h mai", "thứ 2 tới" using NOW.
- "task" excludes the time phrase and the "nhắc tao/tôi/mình" prefix.
- If the user's time is ambiguous or already past, return ok=false.`

// buildOpenAIRequest builds the JSON body sent to the OpenAI chat completions endpoint.
func buildOpenAIRequest(model string, now time.Time, text string) ([]byte, error) {
	userContent := fmt.Sprintf("NOW=%s\nMESSAGE=%s",
		now.Format("2006-01-02T15:04:05-07:00"), text)

	payload := map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": openAISystemInstruction},
			{"role": "user", "content": userContent},
		},
	}
	return json.Marshal(payload)
}

type openAIAPIResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

type openAIInnerJSON struct {
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

// parseOpenAIResponse decodes the outer envelope and then the inner JSON the model produced.
func parseOpenAIResponse(raw []byte) (ParseResult, error) {
	var outer openAIAPIResponse
	if err := json.Unmarshal(raw, &outer); err != nil {
		return ParseResult{}, fmt.Errorf("decode openai outer: %w", err)
	}
	if len(outer.Choices) == 0 || outer.Choices[0].Message.Content == "" {
		return ParseResult{}, fmt.Errorf("openai returned no choices")
	}
	text := extractJSONObject(outer.Choices[0].Message.Content)

	var inner openAIInnerJSON
	if err := json.Unmarshal([]byte(text), &inner); err != nil {
		return ParseResult{}, fmt.Errorf("decode openai inner JSON %q: %w", text, err)
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

// OpenAIParser calls OpenAI's chat completions API.
type OpenAIParser struct {
	apiKey string
	model  string
	client *http.Client
}

func NewOpenAIParser(apiKey, model string, timeout time.Duration) *OpenAIParser {
	if model == "" {
		model = "gemini-2.5-flash"
	}
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &OpenAIParser{
		apiKey: apiKey,
		model:  model,
		client: &http.Client{Timeout: timeout},
	}
}

func (p *OpenAIParser) Parse(ctx context.Context, now time.Time, text string) (ParseResult, error) {
	body, err := buildOpenAIRequest(p.model, now, text)
	if err != nil {
		return ParseResult{}, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://generativelanguage.googleapis.com/v1beta/openai/chat/completions", bytes.NewReader(body))
	if err != nil {
		return ParseResult{}, fmt.Errorf("build openai request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return ParseResult{}, fmt.Errorf("openai request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return ParseResult{}, fmt.Errorf("read openai response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return ParseResult{}, fmt.Errorf("openai status %d: %s", resp.StatusCode, string(raw))
	}
	return parseOpenAIResponse(raw)
}
