package news

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const geminiSystemInstruction = `You are a news editor for a Vietnamese tech-savvy audience. You receive a numbered list of candidate news items (from AI, tech, and Vietnamese general feeds) collected in the last 24 hours.

Pick the MOST important / trending / hot items, up to the requested count. Prefer significant AI and technology developments, plus genuinely major Vietnamese or world news. Drop clickbait, duplicates, and minor items.

Return ONLY a JSON array (no prose, no markdown fences). Each element:
  {"index": <int, the candidate's number>, "en_title": "<concise English headline>", "en_summary": "<one-sentence English summary>", "vi_title": "<Vietnamese headline>", "vi_summary": "<one-sentence Vietnamese summary>"}

Order the array from hottest to least hot. Keep summaries to one sentence each. Never invent items not in the list.`

// Ranker selects and summarizes the top items via Gemini.
type Ranker struct {
	apiKey string
	model  string
	client *http.Client
}

func NewRanker(apiKey, model string, timeout time.Duration) *Ranker {
	if model == "" {
		model = "gemini-2.5-flash"
	}
	if timeout == 0 {
		timeout = 45 * time.Second
	}
	return &Ranker{apiKey: apiKey, model: model, client: &http.Client{Timeout: timeout}}
}

func buildRankRequest(items []FeedItem, maxItems int) ([]byte, error) {
	var builder strings.Builder
	fmt.Fprintf(&builder, "Pick up to %d items.\n\nCANDIDATES:\n", maxItems)
	for i, item := range items {
		desc := item.Description
		if len(desc) > 240 {
			desc = desc[:240]
		}
		fmt.Fprintf(&builder, "[%d] (%s) %s\n%s\n\n", i, item.Source, item.Title, desc)
	}

	payload := map[string]any{
		"system_instruction": map[string]any{
			"parts": []map[string]string{{"text": geminiSystemInstruction}},
		},
		"contents": []map[string]any{{
			"role":  "user",
			"parts": []map[string]string{{"text": builder.String()}},
		}},
		"generationConfig": map[string]any{
			"response_mime_type": "application/json",
			"temperature":        0.2,
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

// Rank sends candidates to Gemini and returns the selected, summarized items
// with their URLs resolved from the original candidate slice.
func (r *Ranker) Rank(ctx context.Context, items []FeedItem, maxItems int) ([]RankedItem, error) {
	if len(items) == 0 {
		return nil, fmt.Errorf("no candidates to rank")
	}
	body, err := buildRankRequest(items, maxItems)
	if err != nil {
		return nil, err
	}

	endpoint := fmt.Sprintf(
		"https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s",
		r.model, r.apiKey)

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build gemini request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gemini request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read gemini response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gemini status %d: %s", resp.StatusCode, string(raw))
	}
	return parseRankResponse(raw, items)
}

func parseRankResponse(raw []byte, candidates []FeedItem) ([]RankedItem, error) {
	var outer geminiAPIResponse
	if err := json.Unmarshal(raw, &outer); err != nil {
		return nil, fmt.Errorf("decode gemini outer: %w", err)
	}
	if len(outer.Candidates) == 0 || len(outer.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("gemini returned no candidates")
	}
	text := strings.TrimSpace(outer.Candidates[0].Content.Parts[0].Text)

	var ranked []RankedItem
	if err := json.Unmarshal([]byte(text), &ranked); err != nil {
		return nil, fmt.Errorf("decode ranked JSON %q: %w", text, err)
	}

	var out []RankedItem
	for _, item := range ranked {
		if item.SourceIndex < 0 || item.SourceIndex >= len(candidates) {
			continue
		}
		item.URL = candidates[item.SourceIndex].URL
		if item.EnglishTitle == "" && item.VietnameseTitle == "" {
			item.EnglishTitle = candidates[item.SourceIndex].Title
		}
		out = append(out, item)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no valid ranked items after mapping")
	}
	return out, nil
}
