package chat

import (
	"context"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"

	"everything-you-need/m/services/llm"
)

const systemPrompt = `Bạn là Trợ lý Pink 🩷, một trợ lý ảo thân thiện, dễ thương và hữu ích.
Trả lời ngắn gọn, tự nhiên bằng tiếng Việt (trừ khi người dùng yêu cầu ngôn ngữ khác).
Vui vẻ nhưng đi thẳng vào vấn đề. Không bịa đặt; nếu không chắc thì nói thẳng.`

// maxHistory caps how many past messages (user+assistant) are kept per chat.
const maxHistory = 10

// wakeRe matches a standalone wake word ("pink" / "trợ lý" / "troly") anywhere in
// the message (start, middle, or end) — bounded by separators so "pinky" or
// "pinkish" won't trigger.
var wakeRe = regexp.MustCompile(`(?i)(^|[\s,.:;!?…-]+)(pink|trợ\s*l[ýí]|tro\s*ly|troly)($|[\s,.:;!?…-]+)`)

// SplitWake reports whether text is addressed to the bot (wake word anywhere) and
// returns the message with the wake word removed and whitespace collapsed.
func SplitWake(text string) (rest string, ok bool) {
	if !wakeRe.MatchString(text) {
		return "", false
	}
	stripped := wakeRe.ReplaceAllString(text, " ")
	return strings.Join(strings.Fields(stripped), " "), true
}

// clearRe matches a whole-message request to reset the conversation (anchored so
// "quên mật khẩu thì sao" stays a normal question).
var clearRe = regexp.MustCompile(`(?i)^(/?clear( context)?|/?reset( context)?|quên đi|quên hết( ngữ cảnh)?|x(óa|oá|oa) (ngữ cảnh|lịch sử)|new chat|làm mới|bắt đầu lại)$`)

// IsClearCommand reports whether the (wake-word-stripped) prompt asks to reset.
func IsClearCommand(prompt string) bool {
	return clearRe.MatchString(strings.TrimSpace(prompt))
}

// Sender delivers a reply back to the chat.
type Sender interface {
	SendReply(ctx context.Context, chatID string, replyToMessageID int64, text string) error
}

// Service answers wake-word messages via the LLM, keeping short per-chat history
// in memory (reset on restart).
type Service struct {
	client  *llm.Client
	sender  Sender
	mu      sync.Mutex
	history map[string][]llm.Message
}

func NewService(token, model string, sender Sender) *Service {
	return &Service{
		client:  llm.NewClient(token, model, 30*time.Second),
		sender:  sender,
		history: make(map[string][]llm.Message),
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
	if IsClearCommand(prompt) {
		s.mu.Lock()
		delete(s.history, chatID)
		s.mu.Unlock()
		return s.sender.SendReply(ctx, chatID, replyToMsgID, "🧹 Đã xoá ngữ cảnh. Mình bắt đầu lại từ đầu nhé!")
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
	s.mu.Lock()
	msgs := append([]llm.Message{}, s.history[chatID]...)
	s.mu.Unlock()
	msgs = append(msgs, llm.Message{Role: "user", Content: prompt})
	return s.client.Complete(ctx, systemPrompt, msgs, 1024)
}

func (s *Service) remember(chatID, prompt, reply string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := append(s.history[chatID],
		llm.Message{Role: "user", Content: prompt},
		llm.Message{Role: "assistant", Content: reply})
	if len(h) > maxHistory {
		h = h[len(h)-maxHistory:]
	}
	s.history[chatID] = h
}
