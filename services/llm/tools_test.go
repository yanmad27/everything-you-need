package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const toolKey = "sk-secret-tools-key"

func toolServer(t *testing.T, status int, resp string, got *map[string]any) *OpenAIClient {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if got != nil {
			*got = map[string]any{}
			_ = json.Unmarshal(b, got)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(srv.Close)
	return NewOpenAIClient(srv.URL, toolKey, "m", 5*time.Second)
}

func TestCompleteWithToolsRequestShape(t *testing.T) {
	var got map[string]any
	c := toolServer(t, 200, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`, &got)
	msgs := []ChatMessage{
		{Role: "user", Parts: []ContentPart{{Type: "text", Text: "see"}, {Type: "image_url", ImageURL: "data:image/png;base64,AAA"}}},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "c1", Name: "f", Arguments: `{"a":1}`}}},
		{Role: "tool", ToolCallID: "c1", Content: "result"},
	}
	tools := []ToolDef{{Name: "f", Description: "d", Parameters: json.RawMessage(`{"type":"object"}`)}}
	r, err := c.CompleteWithTools(context.Background(), "sys", msgs, tools, 50)
	if err != nil {
		t.Fatal(err)
	}
	if r.Text != "ok" || r.Usage.TotalTokens != 5 || r.Usage.PromptTokens != 3 || r.FinishReason != "stop" || r.Assistant.Role != "assistant" {
		t.Fatalf("resp: %+v", r)
	}
	if got["tool_choice"] != "auto" || len(got["tools"].([]any)) != 1 {
		t.Fatalf("tools: %v", got)
	}
	m := got["messages"].([]any)
	if len(m) != 4 || m[0].(map[string]any)["role"] != "system" {
		t.Fatalf("messages: %v", m)
	}
	parts := m[1].(map[string]any)["content"].([]any)
	img := parts[1].(map[string]any)
	if img["type"] != "image_url" || img["image_url"].(map[string]any)["url"] != "data:image/png;base64,AAA" {
		t.Fatalf("image part: %v", parts)
	}
	as := m[2].(map[string]any)
	if as["content"] != nil || as["tool_calls"].([]any)[0].(map[string]any)["type"] != "function" {
		t.Fatalf("assistant: %v", as)
	}
	if tm := m[3].(map[string]any); tm["tool_call_id"] != "c1" || tm["content"] != "result" {
		t.Fatalf("tool: %v", tm)
	}
}

func TestCompleteWithToolsNoToolsField(t *testing.T) {
	var got map[string]any
	c := toolServer(t, 200, `{"choices":[{"message":{"content":"hi"}}]}`, &got)
	if _, err := c.CompleteWithTools(context.Background(), "", []ChatMessage{{Role: "user", Content: "x"}}, nil, 0); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["tools"]; ok {
		t.Fatal("tools sent")
	}
	if _, ok := got["tool_choice"]; ok {
		t.Fatal("tool_choice sent")
	}
}

func TestCompleteWithToolsParsesCalls(t *testing.T) {
	c := toolServer(t, 200, `{"choices":[{"message":{"content":null,"tool_calls":[{"id":"a","type":"function","function":{"name":"f","arguments":"{\"x\":1}"}},{"id":"","type":"function","function":{"name":"g","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`, nil)
	r, err := c.CompleteWithTools(context.Background(), "", []ChatMessage{{Role: "user", Content: "x"}}, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.ToolCalls) != 2 || r.ToolCalls[0].ID != "a" || r.ToolCalls[0].Arguments != `{"x":1}` || !strings.HasPrefix(r.ToolCalls[1].ID, "call_") || r.Text != "" || r.FinishReason != "tool_calls" {
		t.Fatalf("resp: %+v", r)
	}
	if len(r.Assistant.ToolCalls) != 2 {
		t.Fatalf("assistant: %+v", r.Assistant)
	}
}

func TestCompleteWithToolsErrors(t *testing.T) {
	cases := map[string]struct {
		status int
		body   string
	}{
		"non2xx":    {500, `{"error":{"message":"bad ` + toolKey + `"}}`},
		"malformed": {200, `not json ` + toolKey},
		"nochoices": {200, `{"choices":[]}`},
		"empty":     {200, `{"choices":[{"message":{"content":null}}]}`},
	}
	for name, tc := range cases {
		c := toolServer(t, tc.status, tc.body, nil)
		_, err := c.CompleteWithTools(context.Background(), "", []ChatMessage{{Role: "user", Content: "x"}}, nil, 10)
		if err == nil {
			t.Fatalf("%s: want error", name)
		}
		if strings.Contains(err.Error(), toolKey) {
			t.Fatalf("%s: key leaked: %v", name, err)
		}
	}
	// transport error must not leak key or URL
	c := NewOpenAIClient("http://127.0.0.1:1", toolKey, "m", time.Second)
	_, err := c.CompleteWithTools(context.Background(), "", []ChatMessage{{Role: "user", Content: "x"}}, nil, 10)
	if err == nil || strings.Contains(err.Error(), "http://") || strings.Contains(err.Error(), toolKey) {
		t.Fatalf("transport err: %v", err)
	}
}

func TestCompleteWithToolsMaxCompletionRetry(t *testing.T) {
	n := 0
	var second map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		if n == 1 {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":{"message":"Unsupported parameter: max_tokens; use max_completion_tokens"}}`))
			return
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &second)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer srv.Close()
	c := NewOpenAIClient(srv.URL, toolKey, "m", time.Second)
	if _, err := c.CompleteWithTools(context.Background(), "", []ChatMessage{{Role: "user", Content: "x"}}, nil, 10); err != nil {
		t.Fatal(err)
	}
	if second["max_completion_tokens"] == nil || second["max_tokens"] != nil {
		t.Fatalf("retry body: %v", second)
	}
}

func TestCompleteWithToolsSyntheticIDsUnique(t *testing.T) {
	c := toolServer(t, 200, `{"choices":[{"message":{"tool_calls":[{"function":{"name":"f","arguments":"{}"}},{"function":{"name":"g","arguments":"{}"}}]}}]}`, nil)
	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		r, err := c.CompleteWithTools(context.Background(), "", []ChatMessage{{Role: "user", Content: "x"}}, nil, 10)
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range r.ToolCalls {
			if seen[tc.ID] {
				t.Fatalf("duplicate id %s", tc.ID)
			}
			seen[tc.ID] = true
		}
	}
}

func TestCompleteWithToolsSkipsNonFunction(t *testing.T) {
	c := toolServer(t, 200, `{"choices":[{"message":{"tool_calls":[{"id":"a","type":"custom","function":{"name":"x","arguments":"{}"}},{"id":"b","type":"function","function":{"name":"f","arguments":"{}"}},{"id":"c","function":{"name":"g","arguments":"{}"}}]}}]}`, nil)
	r, err := c.CompleteWithTools(context.Background(), "", []ChatMessage{{Role: "user", Content: "x"}}, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.ToolCalls) != 2 || r.ToolCalls[0].ID != "b" || r.ToolCalls[1].ID != "c" {
		t.Fatalf("calls: %+v", r.ToolCalls)
	}
}

func TestCompleteWithToolsObjectArguments(t *testing.T) {
	c := toolServer(t, 200, `{"choices":[{"message":{"tool_calls":[{"id":"a","type":"function","function":{"name":"f","arguments":{"x":1}}},{"id":"b","type":"function","function":{"name":"g","arguments":"{\"y\":2}"}}]}}]}`, nil)
	r, err := c.CompleteWithTools(context.Background(), "", []ChatMessage{{Role: "user", Content: "x"}}, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.ToolCalls) != 2 || r.ToolCalls[0].Arguments != `{"x":1}` || r.ToolCalls[1].Arguments != `{"y":2}` {
		t.Fatalf("calls: %+v", r.ToolCalls)
	}
}
