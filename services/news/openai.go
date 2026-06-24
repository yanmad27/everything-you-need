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

const openAISystemInstruction = `Bạn là bộ lọc tin tức nghiêm ngặt. Bạn nhận danh sách các tin tức thu thập trong 24 giờ qua.

CHỈ chọn các tin TRỰC TIẾP liên quan đến:
- Tội phạm ma túy tại Việt Nam: bắt giữ, khởi tố, xét xử, truy tố
- Các vụ án ma túy tại TP.HCM, đặc biệt Quận 4
- Các cá nhân: Phan Nguyễn Hoàng Duật và những người liên quan trong các vụ án ma túy
- Đường dây buôn bán, vận chuyển ma túy tại Việt Nam

Nếu KHÔNG có tin nào phù hợp, trả về mảng rỗng: {"items": []}
KHÔNG được chọn tin về công nghệ, kinh tế, chính trị hay chủ đề khác.

Return ONLY a JSON object (no prose, no markdown fences) of the shape:
  {"items": [{"index": <int, the candidate's number>, "en_title": "<concise English headline>", "en_summary": "<one-sentence English summary>", "vi_title": "<Vietnamese headline>", "vi_summary": "<one-sentence Vietnamese summary>"}]}

Sắp xếp từ quan trọng nhất đến ít quan trọng nhất. Tóm tắt mỗi tin một câu. Không bịa đặt tin không có trong danh sách.`

// Ranker selects and summarizes the top items via OpenAI.
type Ranker struct {
	apiKey string
	model  string
	client *http.Client
}

func NewRanker(apiKey, model string, timeout time.Duration) *Ranker {
	if model == "" {
		model = "gpt-5"
	}
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	return &Ranker{apiKey: apiKey, model: model, client: &http.Client{Timeout: timeout}}
}

func buildRankRequest(model string, items []FeedItem, maxItems int) ([]byte, error) {
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
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": openAISystemInstruction},
			{"role": "user", "content": builder.String()},
		},
		"response_format": map[string]string{"type": "json_object"},
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

// Rank sends candidates to OpenAI and returns the selected, summarized items
// with their URLs resolved from the original candidate slice.
func (r *Ranker) Rank(ctx context.Context, items []FeedItem, maxItems int) ([]RankedItem, error) {
	if len(items) == 0 {
		return nil, fmt.Errorf("no candidates to rank")
	}
	body, err := buildRankRequest(r.model, items, maxItems)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://api.openai.com/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build openai request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+r.apiKey)

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read openai response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openai status %d: %s", resp.StatusCode, string(raw))
	}
	return parseRankResponse(raw, items)
}

func parseRankResponse(raw []byte, candidates []FeedItem) ([]RankedItem, error) {
	var outer openAIAPIResponse
	if err := json.Unmarshal(raw, &outer); err != nil {
		return nil, fmt.Errorf("decode openai outer: %w", err)
	}
	if len(outer.Choices) == 0 || outer.Choices[0].Message.Content == "" {
		return nil, fmt.Errorf("openai returned no choices")
	}
	text := strings.TrimSpace(outer.Choices[0].Message.Content)

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
