package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Completer is the minimal LLM surface used by the chat assistant and the
// reminder parser. Both *Client (Anthropic) and *OpenAIClient implement it.
type Completer interface {
	Complete(ctx context.Context, system string, messages []Message, maxTokens int) (string, error)
}

// OpenAIClient talks to an OpenAI-compatible /v1/chat/completions endpoint.
type OpenAIClient struct {
	baseURL string
	apiKey  string
	model   string
	http    *http.Client
}

// NewOpenAIClient builds a client. baseURL is the server root (e.g.
// https://host) or a root that already ends in /v1.
func NewOpenAIClient(baseURL, apiKey, model string, timeout time.Duration) *OpenAIClient {
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	base := strings.TrimRight(baseURL, "/")
	base = strings.TrimSuffix(base, "/v1")
	return &OpenAIClient{baseURL: base, apiKey: apiKey, model: model, http: &http.Client{Timeout: timeout}}
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIRequest struct {
	Model     string          `json:"model"`
	Messages  []openAIMessage `json:"messages"`
	MaxTokens int             `json:"max_tokens,omitempty"`
}

type openAIResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Complete sends system + messages and returns the first choice's text.
func (c *OpenAIClient) Complete(ctx context.Context, system string, messages []Message, maxTokens int) (string, error) {
	if maxTokens <= 0 {
		maxTokens = 1024
	}
	msgs := make([]openAIMessage, 0, len(messages)+1)
	if system != "" {
		msgs = append(msgs, openAIMessage{Role: "system", Content: system})
	}
	for _, m := range messages {
		role := "user"
		if m.Role == "assistant" {
			role = "assistant"
		}
		msgs = append(msgs, openAIMessage{Role: role, Content: m.Content})
	}
	body, err := json.Marshal(openAIRequest{Model: c.model, Messages: msgs, MaxTokens: maxTokens})
	if err != nil {
		return "", fmt.Errorf("openai-compat marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("openai-compat request: %s", c.redact(err.Error()))
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		return "", fmt.Errorf("openai-compat request failed: %s", c.redact(err.Error()))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", fmt.Errorf("openai-compat read: %s", c.redact(err.Error()))
	}

	var out openAIResponse
	jsonErr := json.Unmarshal(raw, &out)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		detail := ""
		if jsonErr == nil && out.Error != nil {
			detail = ": " + c.redact(out.Error.Message)
		}
		return "", fmt.Errorf("openai-compat HTTP %d%s", resp.StatusCode, detail)
	}
	if jsonErr != nil {
		return "", fmt.Errorf("openai-compat: malformed response JSON")
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("openai-compat: response has no choices")
	}
	text := strings.TrimSpace(out.Choices[0].Message.Content)
	if text == "" {
		return "", fmt.Errorf("openai-compat returned no text")
	}
	return text, nil
}

func (c *OpenAIClient) redact(s string) string {
	if c.apiKey == "" {
		return s
	}
	return strings.ReplaceAll(s, c.apiKey, "<redacted>")
}
