package news

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"everything-you-need/m/services/llm"
)

const systemInstruction = `Bạn là biên tập viên tin tức. Bạn nhận danh sách tin tổng hợp (AI, công nghệ, và tin Việt Nam).

Chọn các tin NỔI BẬT và quan trọng nhất trong ngày, loại bỏ trùng lặp và tin câu view. Nếu không có tin nào đáng chú ý, trả về: {"items": []}

Return ONLY a JSON object (no prose, no markdown fences) of the shape:
  {"items": [{"index": <int, the candidate's number>, "en_title": "<concise English headline>", "en_summary": "<one-sentence English summary>", "vi_title": "<tiêu đề tiếng Việt>", "vi_summary": "<tóm tắt một câu tiếng Việt>"}]}

Sắp xếp từ quan trọng/nổi bật nhất. Tóm tắt mỗi tin một câu bằng tiếng Việt. Không bịa đặt.`

// Ranker selects and summarizes the top news items via the Anthropic Messages API.
type Ranker struct {
	client *llm.Client
}

func NewRanker(token, model string, timeout time.Duration) *Ranker {
	return &Ranker{client: llm.NewClient(token, model, timeout)}
}

func buildRankPrompt(items []FeedItem, maxItems int) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "Pick up to %d items.\n\nCANDIDATES:\n", maxItems)
	for i, item := range items {
		desc := item.Description
		if len(desc) > 240 {
			desc = desc[:240]
		}
		fmt.Fprintf(&builder, "[%d] (%s) %s\n%s\n\n", i, item.Source, item.Title, desc)
	}
	return builder.String()
}

// Rank sends candidates to the model and returns the selected, summarized items
// with their URLs resolved from the original candidate slice.
func (r *Ranker) Rank(ctx context.Context, items []FeedItem, maxItems int) ([]RankedItem, error) {
	if len(items) == 0 {
		return nil, fmt.Errorf("no candidates to rank")
	}
	out, err := r.client.Complete(ctx, systemInstruction,
		[]llm.Message{{Role: "user", Content: buildRankPrompt(items, maxItems)}}, 2048)
	if err != nil {
		return nil, err
	}
	return parseRankJSON(out, items)
}

func parseRankJSON(modelText string, candidates []FeedItem) ([]RankedItem, error) {
	text := extractJSONObject(modelText)

	var wrapper struct {
		Items []RankedItem `json:"items"`
	}
	if err := json.Unmarshal([]byte(text), &wrapper); err != nil {
		return nil, fmt.Errorf("decode ranked JSON %q: %w", text, err)
	}

	var out []RankedItem
	for _, item := range wrapper.Items {
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
