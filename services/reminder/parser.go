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

const geminiSystemInstruction = `You are a strict parser. Given a Vietnamese reminder request and the current time (Asia/Ho_Chi_Minh), return ONLY JSON, no prose.
Output shape:
  {"ok": true,  "when": "<ISO 8601 with +07:00 offset>", "task": "<short Vietnamese string>"}
  {"ok": false, "reason": "<short Vietnamese explanation>"}
Rules:
- "when" must be strictly in the future relative to NOW.
- Resolve relative phrases like "2h nữa", "19h mai", "thứ 2 tới" using NOW.
- "task" excludes the time phrase and the "nhắc tao/tôi/mình" prefix.
- If the user's time is ambiguous or already past, return ok=false.`

// buildGeminiRequest builds the JSON body sent to the Gemini generateContent endpoint.
func buildGeminiRequest(now time.Time, text string) ([]byte, error) {
	userContent := fmt.Sprintf("NOW=%s\nMESSAGE=%s",
		now.Format("2006-01-02T15:04:05-07:00"), text)

	payload := map[string]any{
		"system_instruction": map[string]any{
			"parts": []map[string]string{{"text": geminiSystemInstruction}},
		},
		"contents": []map[string]any{{
			"role":  "user",
			"parts": []map[string]string{{"text": userContent}},
		}},
		"generationConfig": map[string]any{
			"response_mime_type": "application/json",
			"temperature":        0,
		},
	}
	return json.Marshal(payload)
}

type geminiAPIResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
}

type geminiInnerJSON struct {
	OK     bool   `json:"ok"`
	When   string `json:"when"`
	Task   string `json:"task"`
	Reason string `json:"reason"`
}

// parseGeminiResponse decodes Gemini's outer envelope and then the inner JSON the model produced.
func parseGeminiResponse(raw []byte) (ParseResult, error) {
	var outer geminiAPIResponse
	if err := json.Unmarshal(raw, &outer); err != nil {
		return ParseResult{}, fmt.Errorf("decode gemini outer: %w", err)
	}
	if len(outer.Candidates) == 0 || len(outer.Candidates[0].Content.Parts) == 0 {
		return ParseResult{}, fmt.Errorf("gemini returned no candidates")
	}
	text := strings.TrimSpace(outer.Candidates[0].Content.Parts[0].Text)

	var inner geminiInnerJSON
	if err := json.Unmarshal([]byte(text), &inner); err != nil {
		return ParseResult{}, fmt.Errorf("decode gemini inner JSON %q: %w", text, err)
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

// GeminiParser calls Google's Generative Language API.
type GeminiParser struct {
	apiKey string
	model  string
	client *http.Client
}

func NewGeminiParser(apiKey, model string, timeout time.Duration) *GeminiParser {
	if model == "" {
		model = "gemini-2.5-flash"
	}
	if timeout == 0 {
		timeout = 15 * time.Second
	}
	return &GeminiParser{
		apiKey: apiKey,
		model:  model,
		client: &http.Client{Timeout: timeout},
	}
}

func (g *GeminiParser) Parse(ctx context.Context, now time.Time, text string) (ParseResult, error) {
	body, err := buildGeminiRequest(now, text)
	if err != nil {
		return ParseResult{}, err
	}

	url := fmt.Sprintf(
		"https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s",
		g.model, g.apiKey)

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return ParseResult{}, fmt.Errorf("build gemini request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.client.Do(req)
	if err != nil {
		return ParseResult{}, fmt.Errorf("gemini request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return ParseResult{}, fmt.Errorf("read gemini response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return ParseResult{}, fmt.Errorf("gemini status %d: %s", resp.StatusCode, string(raw))
	}
	return parseGeminiResponse(raw)
}
