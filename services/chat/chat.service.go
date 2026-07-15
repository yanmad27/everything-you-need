package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

const geminiChatURL = "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions"

const systemPrompt = `Bạn là Trợ lý Pink 🩷, một trợ lý ảo thân thiện, dễ thương và hữu ích.
Trả lời ngắn gọn, tự nhiên bằng tiếng Việt (trừ khi người dùng yêu cầu ngôn ngữ khác).
Vui vẻ nhưng đi thẳng vào vấn đề. Không bịa đặt; nếu không chắc thì nói thẳng.`

// maxHistory caps how many past messages (user+assistant) are kept per chat.
const maxHistory = 10

// wakeRe matches a leading wake word ("pink" / "trợ lý" / "troly") followed by a
// separator or end of string, so "pinky" won't trigger.
var wakeRe = regexp.MustCompile(`(?i)^\s*(pink|trợ\s*l[ýí]|tro\s*ly|troly)(\s+|[,.:;!?…-]+|$)`)

// SplitWake reports whether text is addressed to the bot and returns the message
// with the wake word stripped.
func SplitWake(text string) (rest string, ok bool) {
	loc := wakeRe.FindStringIndex(text)
	if loc == nil {
		return "", false
	}
	return strings.TrimSpace(text[loc[1]:]), true
}

// Sender delivers a reply back to the chat.
type Sender interface {
	SendReply(ctx context.Context, chatID string, replyToMessageID int64, text string) error
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Service answers wake-word messages via Gemini, keeping short per-chat history
// in memory (reset on restart).
type Service struct {
	apiKey  string
	model   string
	sender  Sender
	client  *http.Client
	mu      sync.Mutex
	history map[string][]message
}

func NewService(apiKey, model string, sender Sender) *Service {
	if model == "" {
		model = "gemini-flash-latest"
	}
	return &Service{
		apiKey:  apiKey,
		model:   model,
		sender:  sender,
		client:  &http.Client{Timeout: 30 * time.Second},
		history: make(map[string][]message),
	}
}

// Handle answers one wake-word message: strips the wake word, calls the model
// with recent history, replies, and records the exchange.
func (s *Service) Handle(ctx context.Context, chatID string, replyToMsgID int64, text string) error {
	prompt, ok := SplitWake(text)
	if !ok {
		return nil
	}
	if prompt == "" {
		return s.sender.SendReply(ctx, chatID, replyToMsgID, "Dạ, Pink đây! 🩷 Bạn cần gì nào?")
	}

	reply, err := s.complete(ctx, chatID, prompt)
	if err != nil {
		log.Printf("chat: completion error: %v", err)
		return s.sender.SendReply(ctx, chatID, replyToMsgID, "⚠️ Pink hơi bận, thử lại sau chút nhé.")
	}

	if err := s.sender.SendReply(ctx, chatID, replyToMsgID, reply); err != nil {
		return err
	}
	s.remember(chatID, prompt, reply)
	return nil
}

func (s *Service) complete(ctx context.Context, chatID, prompt string) (string, error) {
	msgs := []message{{Role: "system", Content: systemPrompt}}
	s.mu.Lock()
	msgs = append(msgs, s.history[chatID]...)
	s.mu.Unlock()
	msgs = append(msgs, message{Role: "user", Content: prompt})

	body, err := json.Marshal(map[string]any{"model": s.model, "messages": msgs})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", geminiChatURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.apiKey)

	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gemini status %d: %s", resp.StatusCode, string(raw))
	}

	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("decode gemini response: %w", err)
	}
	if len(out.Choices) == 0 || strings.TrimSpace(out.Choices[0].Message.Content) == "" {
		return "", fmt.Errorf("gemini returned no content")
	}
	return strings.TrimSpace(out.Choices[0].Message.Content), nil
}

func (s *Service) remember(chatID, prompt, reply string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := append(s.history[chatID], message{Role: "user", Content: prompt}, message{Role: "assistant", Content: reply})
	if len(h) > maxHistory {
		h = h[len(h)-maxHistory:]
	}
	s.history[chatID] = h
}
