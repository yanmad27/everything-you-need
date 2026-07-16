// Package llm is a thin wrapper over the Anthropic Messages API (via the
// official anthropic-sdk-go), shared by the reminder parser, news ranker, and
// chat services.
package llm

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

const defaultModel = "claude-haiku-4-5"

// Message is one turn. Role is "user" or "assistant" (Anthropic has no "system"
// role — the system prompt is passed separately to Complete).
type Message struct {
	Role    string
	Content string
}

// Client wraps the Anthropic SDK client. The credential may be a standard API
// key or a Claude OAuth/setup token (sk-ant-oat…) — NewClient picks the auth
// method by prefix.
type Client struct {
	api   anthropic.Client
	model anthropic.Model
}

func NewClient(token, model string, timeout time.Duration) *Client {
	if model == "" {
		model = defaultModel
	}
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	opts := []option.RequestOption{option.WithRequestTimeout(timeout)}
	if strings.HasPrefix(token, "sk-ant-oat") {
		// OAuth / setup token: Bearer auth + the oauth beta header.
		opts = append(opts,
			option.WithAuthToken(token),
			option.WithHeaderAdd("anthropic-beta", "oauth-2025-04-20"),
		)
	} else {
		opts = append(opts, option.WithAPIKey(token))
	}
	return &Client{api: anthropic.NewClient(opts...), model: anthropic.Model(model)}
}

// Complete sends system + messages and returns the concatenated text output.
func (c *Client) Complete(ctx context.Context, system string, messages []Message, maxTokens int) (string, error) {
	if maxTokens <= 0 {
		maxTokens = 1024
	}
	msgs := make([]anthropic.MessageParam, 0, len(messages))
	for _, m := range messages {
		block := anthropic.NewTextBlock(m.Content)
		if m.Role == "assistant" {
			msgs = append(msgs, anthropic.NewAssistantMessage(block))
		} else {
			msgs = append(msgs, anthropic.NewUserMessage(block))
		}
	}

	params := anthropic.MessageNewParams{
		Model:     c.model,
		MaxTokens: int64(maxTokens),
		Messages:  msgs,
	}
	if system != "" {
		params.System = []anthropic.TextBlockParam{{Text: system}}
	}

	resp, err := c.api.Messages.New(ctx, params)
	if err != nil {
		return "", fmt.Errorf("anthropic request: %w", err)
	}

	var sb strings.Builder
	for _, block := range resp.Content {
		if tb, ok := block.AsAny().(anthropic.TextBlock); ok {
			sb.WriteString(tb.Text)
		}
	}
	text := strings.TrimSpace(sb.String())
	if text == "" {
		return "", fmt.Errorf("anthropic returned no text")
	}
	return text, nil
}
