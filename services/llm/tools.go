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
	"sync/atomic"
)

// synthCallSeq numbers synthetic tool-call ids so they are unique across
// responses within the process.
var synthCallSeq atomic.Uint64

// ContentPart is one piece of multimodal user content.
type ContentPart struct {
	Type     string // "text" | "image_url"
	Text     string
	ImageURL string // data: URI or https URL
}

// ToolCall is a function call requested by the model.
type ToolCall struct {
	ID        string
	Name      string
	Arguments string // raw JSON string as returned
}

// ChatMessage is a turn in a tool-calling conversation.
type ChatMessage struct {
	Role       string        // "user" | "assistant" | "tool"
	Content    string        // plain text (used when Parts empty)
	Parts      []ContentPart // optional multimodal content (user only)
	ToolCalls  []ToolCall    // assistant turn that requested tools
	ToolCallID string        // for Role=="tool"
}

// ToolDef describes a callable function.
type ToolDef struct {
	Name, Description string
	Parameters        json.RawMessage // JSON Schema object
}

// Usage is token accounting reported by the server.
type Usage struct{ PromptTokens, CompletionTokens, TotalTokens int }

// ToolResponse is the result of CompleteWithTools.
type ToolResponse struct {
	Text         string
	ToolCalls    []ToolCall
	Assistant    ChatMessage // ready-to-append assistant message
	Usage        Usage
	FinishReason string
}

// ToolCompleter is implemented by clients that support tool calling and
// image input.
type ToolCompleter interface {
	CompleteWithTools(ctx context.Context, system string, msgs []ChatMessage, tools []ToolDef, maxTokens int) (ToolResponse, error)
}

type oaiImageURL struct {
	URL string `json:"url"`
}

type oaiPart struct {
	Type     string       `json:"type"`
	Text     string       `json:"text,omitempty"`
	ImageURL *oaiImageURL `json:"image_url,omitempty"`
}

type oaiFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type oaiToolCall struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	Function oaiFunctionCall `json:"function"`
}

type oaiChatMessage struct {
	Role       string        `json:"role"`
	Content    any           `json:"content"`
	ToolCalls  []oaiToolCall `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
}

type oaiToolFunc struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type oaiTool struct {
	Type     string      `json:"type"`
	Function oaiToolFunc `json:"function"`
}

type oaiToolRequest struct {
	Model               string           `json:"model"`
	Messages            []oaiChatMessage `json:"messages"`
	Tools               []oaiTool        `json:"tools,omitempty"`
	ToolChoice          string           `json:"tool_choice,omitempty"`
	MaxTokens           int              `json:"max_tokens,omitempty"`
	MaxCompletionTokens int              `json:"max_completion_tokens,omitempty"`
}

// oaiRespToolCall tolerates servers that return arguments as a JSON object
// instead of a JSON string, and entries with a non-function type.
type oaiRespToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"function"`
}

func (t oaiRespToolCall) arguments() string {
	raw := bytes.TrimSpace(t.Function.Arguments)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
	}
	return string(raw)
}

type oaiToolResponse struct {
	Choices []struct {
		Message struct {
			Content   *string           `json:"content"`
			ToolCalls []oaiRespToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// CompleteWithTools sends system + msgs (+ tools) and returns the first
// choice's text and/or tool calls. It never logs message contents.
func (c *OpenAIClient) CompleteWithTools(ctx context.Context, system string, msgs []ChatMessage, tools []ToolDef, maxTokens int) (ToolResponse, error) {
	if maxTokens <= 0 {
		maxTokens = 1024
	}
	wire := make([]oaiChatMessage, 0, len(msgs)+1)
	if system != "" {
		wire = append(wire, oaiChatMessage{Role: "system", Content: system})
	}
	for _, m := range msgs {
		wire = append(wire, toWireMessage(m))
	}
	var wtools []oaiTool
	for _, t := range tools {
		wtools = append(wtools, oaiTool{Type: "function", Function: oaiToolFunc{Name: t.Name, Description: t.Description, Parameters: t.Parameters}})
	}
	resp, status, detail, err := c.doTools(ctx, wire, wtools, maxTokens, false)
	if err == nil {
		return resp, nil
	}
	if status == http.StatusBadRequest && maxTokensUnsupported(detail) {
		resp, _, _, err = c.doTools(ctx, wire, wtools, maxTokens, true)
		return resp, err
	}
	return ToolResponse{}, err
}

func toWireMessage(m ChatMessage) oaiChatMessage {
	w := oaiChatMessage{Role: m.Role, ToolCallID: m.ToolCallID}
	switch {
	case len(m.Parts) > 0:
		parts := make([]oaiPart, 0, len(m.Parts))
		for _, p := range m.Parts {
			if p.Type == "image_url" {
				parts = append(parts, oaiPart{Type: "image_url", ImageURL: &oaiImageURL{URL: p.ImageURL}})
			} else {
				parts = append(parts, oaiPart{Type: "text", Text: p.Text})
			}
		}
		w.Content = parts
	case m.Role == "assistant" && len(m.ToolCalls) > 0 && m.Content == "":
		w.Content = nil
	default:
		w.Content = m.Content
	}
	for _, tc := range m.ToolCalls {
		w.ToolCalls = append(w.ToolCalls, oaiToolCall{ID: tc.ID, Type: "function", Function: oaiFunctionCall{Name: tc.Name, Arguments: tc.Arguments}})
	}
	return w
}

func (c *OpenAIClient) doTools(ctx context.Context, msgs []oaiChatMessage, tools []oaiTool, maxTokens int, completionField bool) (ToolResponse, int, string, error) {
	reqBody := oaiToolRequest{Model: c.model, Messages: msgs, Tools: tools}
	if len(tools) > 0 {
		reqBody.ToolChoice = "auto"
	}
	if completionField {
		reqBody.MaxCompletionTokens = maxTokens
	} else {
		reqBody.MaxTokens = maxTokens
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return ToolResponse{}, 0, "", fmt.Errorf("openai-compat marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return ToolResponse{}, 0, "", fmt.Errorf("openai-compat request: %s", c.redact(err.Error()))
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		return ToolResponse{}, 0, "", fmt.Errorf("openai-compat request failed: %s", c.redact(err.Error()))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return ToolResponse{}, 0, "", fmt.Errorf("openai-compat read: %s", c.redact(err.Error()))
	}

	var out oaiToolResponse
	jsonErr := json.Unmarshal(raw, &out)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		detail := ""
		if jsonErr == nil && out.Error != nil {
			detail = c.redact(out.Error.Message)
		}
		msg := fmt.Sprintf("openai-compat HTTP %d", resp.StatusCode)
		if detail != "" {
			msg += ": " + detail
		}
		return ToolResponse{}, resp.StatusCode, detail, errors.New(msg)
	}
	if jsonErr != nil {
		return ToolResponse{}, resp.StatusCode, "", errors.New("openai-compat: malformed response JSON")
	}
	if len(out.Choices) == 0 {
		return ToolResponse{}, resp.StatusCode, "", errors.New("openai-compat: response has no choices")
	}
	ch := out.Choices[0]
	text := ""
	if ch.Message.Content != nil {
		text = strings.TrimSpace(*ch.Message.Content)
	}
	var calls []ToolCall
	for _, tc := range ch.Message.ToolCalls {
		if tc.Type != "" && tc.Type != "function" {
			continue
		}
		id := tc.ID
		if id == "" {
			id = fmt.Sprintf("call_%d", synthCallSeq.Add(1))
		}
		calls = append(calls, ToolCall{ID: id, Name: tc.Function.Name, Arguments: tc.arguments()})
	}
	if text == "" && len(calls) == 0 {
		return ToolResponse{}, resp.StatusCode, "", errors.New("openai-compat returned no text")
	}
	return ToolResponse{
		Text:         text,
		ToolCalls:    calls,
		Assistant:    ChatMessage{Role: "assistant", Content: text, ToolCalls: calls},
		Usage:        Usage{out.Usage.PromptTokens, out.Usage.CompletionTokens, out.Usage.TotalTokens},
		FinishReason: ch.FinishReason,
	}, resp.StatusCode, "", nil
}

var _ ToolCompleter = (*OpenAIClient)(nil)
