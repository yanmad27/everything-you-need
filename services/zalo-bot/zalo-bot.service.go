// Package zalobot sends and receives Zalo Bot Platform messages.
//
// API reference: https://bot.zaloplatforms.com/docs/call-api/
package zalobot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"everything-you-need/m/services/config"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/submodule-org/submodule.go/v2"
)

const (
	defaultBaseURL = "https://bot-api.zaloplatforms.com"

	// maxTextLen is Zalo's sendMessage text limit (1..2000 characters).
	maxTextLen = 2000
)

type ZaloBotService struct {
	botToken string
	baseURL  string
	client   *http.Client
}

type sendMessageRequest struct {
	ChatID string `json:"chat_id"`
	Text   string `json:"text"`
}

type apiResponse struct {
	Ok          bool   `json:"ok"`
	Description string `json:"description,omitempty"`
	ErrorCode   int    `json:"error_code,omitempty"`
}

func NewZaloBotService(config *config.Config) *ZaloBotService {
	if !config.Zalo.Enabled {
		log.Printf("Zalo bot service is disabled")
		return nil
	}
	if config.Zalo.BotToken == "" {
		log.Printf("Zalo bot service is disabled: bot_token is missing")
		return nil
	}

	log.Printf("Zalo bot service initialized")
	return NewClient(config.Zalo.BotToken, defaultBaseURL)
}

// NewClient builds a service against an explicit API base URL (used by tests).
func NewClient(botToken, baseURL string) *ZaloBotService {
	return &ZaloBotService{
		botToken: botToken,
		baseURL:  strings.TrimRight(baseURL, "/"),
		client:   &http.Client{Timeout: 30 * time.Second},
	}
}

var ZaloBotServiceMod = submodule.Make[*ZaloBotService](NewZaloBotService, config.ConfigMod)

// SendMessage satisfies the reminder.Sender interface.
func (z *ZaloBotService) SendMessage(ctx context.Context, chatID, text string) error {
	return z.send(ctx, chatID, text)
}

// SendReply satisfies the reminder.Sender and chat.Sender interfaces. Zalo has
// no reply-to parameter, so the original message id is ignored and the text is
// sent as a plain message to the same chat.
func (z *ZaloBotService) SendReply(ctx context.Context, chatID string, _ int64, text string) error {
	return z.send(ctx, chatID, text)
}

func (z *ZaloBotService) send(ctx context.Context, chatID, text string) error {
	for _, chunk := range splitText(text, maxTextLen) {
		if err := z.call(ctx, "sendMessage", sendMessageRequest{ChatID: chatID, Text: chunk}); err != nil {
			return err
		}
	}
	return nil
}

// call POSTs a JSON payload to a bot API method. The token lives in the URL
// path, so no error or log line produced here may contain the URL.
func (z *ZaloBotService) call(ctx context.Context, method string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	endpoint := fmt.Sprintf("%s/bot%s/%s", z.baseURL, z.botToken, method)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %s", z.redact(err.Error()))
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := z.client.Do(req)
	if err != nil {
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err // drop the URL (it embeds the token)
		}
		return fmt.Errorf("failed to send %s request: %s", method, z.redact(err.Error()))
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("failed to read response: %s", z.redact(err.Error()))
	}

	var out apiResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return fmt.Errorf("zalo API %s: unexpected response (HTTP %d)", method, resp.StatusCode)
	}
	if !out.Ok {
		return fmt.Errorf("zalo API %s error %d: %s", method, out.ErrorCode, z.redact(out.Description))
	}
	return nil
}

func (z *ZaloBotService) redact(s string) string {
	if z.botToken == "" {
		return s
	}
	return strings.ReplaceAll(s, z.botToken, "<redacted>")
}

// splitText cuts text into pieces of at most limit runes, preferring to break
// at a newline or space in the second half of the piece.
func splitText(text string, limit int) []string {
	runes := []rune(text)
	if len(runes) <= limit {
		return []string{text}
	}
	var out []string
	for len(runes) > limit {
		cut := limit
		for i := limit - 1; i > limit/2; i-- {
			if runes[i] == '\n' {
				cut = i + 1
				break
			}
		}
		if cut == limit {
			for i := limit - 1; i > limit/2; i-- {
				if runes[i] == ' ' {
					cut = i + 1
					break
				}
			}
		}
		out = append(out, string(runes[:cut]))
		runes = runes[cut:]
	}
	if len(runes) > 0 {
		out = append(out, string(runes))
	}
	return out
}
