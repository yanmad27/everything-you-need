package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const fakeKey = "sk-test-not-a-real-key"

func TestOpenAIClientRequestShape(t *testing.T) {
	var path, auth, ct string
	var body openAIRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, auth, ct = r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"  chào  "}}]}`))
	}))
	defer srv.Close()

	// base URL with trailing /v1 must not double up
	c := NewOpenAIClient(srv.URL+"/v1/", fakeKey, "m-1", 0)
	out, err := c.Complete(context.Background(), "sys", []Message{
		{Role: "user", Content: "a"}, {Role: "assistant", Content: "b"}, {Role: "user", Content: "c"},
	}, 256)
	if err != nil || out != "chào" {
		t.Fatalf("out=%q err=%v", out, err)
	}
	if path != "/v1/chat/completions" || auth != "Bearer "+fakeKey || ct != "application/json" {
		t.Errorf("path=%q auth set=%v ct=%q", path, auth == "Bearer "+fakeKey, ct)
	}
	if body.Model != "m-1" || body.MaxTokens != 256 || len(body.Messages) != 4 ||
		body.Messages[0] != (openAIMessage{"system", "sys"}) ||
		body.Messages[2] != (openAIMessage{"assistant", "b"}) {
		t.Errorf("body = %+v", body)
	}
}

func TestOpenAIClientErrors(t *testing.T) {
	cases := map[string]struct {
		status int
		body   string
	}{
		"non-2xx":         {401, `{"error":{"message":"bad key ` + fakeKey + `"}}`},
		"non-2xx-no-json": {502, `<html>`},
		"malformed":       {200, `{not json`},
		"empty choices":   {200, `{"choices":[]}`},
		"empty text":      {200, `{"choices":[{"message":{"content":"  "}}]}`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			_, err := NewOpenAIClient(srv.URL, fakeKey, "m", 0).Complete(context.Background(), "", []Message{{Role: "user", Content: "x"}}, 0)
			if err == nil {
				t.Fatal("expected error")
			}
			if strings.Contains(err.Error(), fakeKey) {
				t.Errorf("error leaks key: %v", err)
			}
		})
	}
}

func TestOpenAIClientTransportErrorHidesKeyAndURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()
	_, err := NewOpenAIClient(url, fakeKey, "m", 0).Complete(context.Background(), "", []Message{{Role: "user", Content: "x"}}, 0)
	if err == nil || strings.Contains(err.Error(), fakeKey) || strings.Contains(err.Error(), url) {
		t.Errorf("err = %v", err)
	}
}

func TestOpenAIClientRetriesWithMaxCompletionTokens(t *testing.T) {
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &m)
		bodies = append(bodies, m)
		if _, ok := m["max_tokens"]; ok {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":{"message":"Unsupported parameter: max_tokens. Use max_completion_tokens."}}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer srv.Close()
	out, err := NewOpenAIClient(srv.URL, fakeKey, "m", 0).Complete(context.Background(), "", []Message{{Role: "user", Content: "x"}}, 77)
	if err != nil || out != "ok" || len(bodies) != 2 {
		t.Fatalf("out=%q err=%v calls=%d", out, err, len(bodies))
	}
	if bodies[1]["max_completion_tokens"] != float64(77) || bodies[1]["max_tokens"] != nil {
		t.Errorf("retry body = %v", bodies[1])
	}
}

func TestMaxTokensUnsupportedMatch(t *testing.T) {
	yes := []string{"Unsupported parameter: 'max_tokens'", "MAX_TOKENS is not supported with this model", "use max_completion_tokens instead of max_tokens"}
	no := []string{"max_tokens must be positive", "invalid model", "", "unsupported parameter: temperature"}
	for _, d := range yes {
		if !maxTokensUnsupported(d) {
			t.Errorf("%q should retry", d)
		}
	}
	for _, d := range no {
		if maxTokensUnsupported(d) {
			t.Errorf("%q should not retry", d)
		}
	}
}

func TestWithHTTPClientIsUsed(t *testing.T) {
	var used bool
	hc := &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
		used = true
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"hi"}}]}`))}, nil
	})}
	c := NewOpenAIClient("https://x.example.com", "k", "m", 0).WithHTTPClient(hc)
	if out, err := c.Complete(context.Background(), "", []Message{{Role: "user", Content: "q"}}, 5); err != nil || out != "hi" || !used {
		t.Fatalf("%q %v used=%v", out, err, used)
	}
	if NewOpenAIClient("https://x.example.com", "k", "m", 0).WithHTTPClient(nil).http == nil {
		t.Fatal("nil client must be ignored")
	}
}

type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
