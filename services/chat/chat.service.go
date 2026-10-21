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
Vui vẻ nhưng đi thẳng vào vấn đề. Không bịa đặt; nếu không chắc thì nói thẳng.
Khi tin nhắn có kèm phần "[Nội dung trích từ trang ...]", hãy dùng chính nội dung đó để trả lời (đừng nói là không truy cập được link).`

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
	client    llm.Completer
	sender    Sender
	mu        sync.Mutex
	history   map[string][]llm.Message
	safeFetch bool // block private/loopback targets when reading pasted links

	// Tool-calling path (Zalo only); nil toolClient keeps the plain Complete path.
	toolClient   llm.ToolCompleter
	tools        Tools
	toolImpls    []toolImpl
	limiter      *limiter
	last         map[string]lastText
	searchBudget *searchBudget
	dueChats     map[string]bool
}

func NewService(token, model string, sender Sender) *Service {
	return NewServiceWithCompleter(llm.NewClient(token, model, 30*time.Second), sender)
}

// WithSafeFetch makes link reading refuse loopback/private/link-local targets
// (SSRF guard). Off by default so existing behaviour is unchanged.
func (s *Service) WithSafeFetch(on bool) *Service {
	s.safeFetch = on
	return s
}

// NewServiceWithCompleter builds the assistant on any LLM backend.
func NewServiceWithCompleter(client llm.Completer, sender Sender) *Service {
	return &Service{
		client:  client,
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
	return s.respond(ctx, chatID, replyToMsgID, prompt)
}

// HandleAddressed answers a message that is already known to be addressed to
// the bot (e.g. a Zalo group @mention/reply), so no wake word is required. A
// wake word, if present, is still stripped.
func (s *Service) HandleAddressed(ctx context.Context, chatID string, replyToMsgID int64, text string) error {
	return s.HandleAddressedAs(ctx, chatID, "", "", replyToMsgID, text)
}

// HandleAddressedAs is HandleAddressed with the sender's identity, used to
// attribute reminders created through tools.
func (s *Service) HandleAddressedAs(ctx context.Context, chatID, userID, userName string, replyToMsgID int64, text string) error {
	prompt, ok := SplitWake(text)
	if !ok {
		prompt = strings.Join(strings.Fields(text), " ")
	}
	return s.respondAs(ctx, chatID, userID, userName, replyToMsgID, prompt)
}

func (s *Service) respond(ctx context.Context, chatID string, replyToMsgID int64, prompt string) error {
	return s.respondAs(ctx, chatID, "", "", replyToMsgID, prompt)
}

func (s *Service) respondAs(ctx context.Context, chatID, userID, userName string, replyToMsgID int64, prompt string) error {
	if prompt == "" {
		return s.sender.SendReply(ctx, chatID, replyToMsgID, "Dạ, Pink đây! 🩷 Bạn cần gì nào?")
	}
	if IsClearCommand(prompt) {
		s.mu.Lock()
		delete(s.history, chatID)
		s.dropLast(chatID)
		s.mu.Unlock()
		return s.sender.SendReply(ctx, chatID, replyToMsgID, "🧹 Đã xoá ngữ cảnh. Mình bắt đầu lại từ đầu nhé!")
	}

	if s.toolClient != nil {
		return s.respondWithTools(ctx, chatID, userID, userName, replyToMsgID, prompt)
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
	msgs = append(msgs, llm.Message{Role: "user", Content: augmentWithURLs(ctx, prompt, s.safeFetch)})
	return s.client.Complete(ctx, systemPrompt, msgs, 2048)
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

// respondWithTools is the Zalo path: rate limit, bounded tool loop, markdown
// reply. History keeps only the text turns.
func (s *Service) respondWithTools(ctx context.Context, chatID, userID, userName string, replyToMsgID int64, prompt string) error {
	ctx = newTurn(ctx)
	in := ConvoEvent{ChatID: chatID, UserID: userID, UserName: userName, Direction: "in", Kind: "text", Text: prompt}
	if ok, notice := s.limiter.allow(chatID); !ok {
		// rate-limited: counted, but the text is not stored
		in.Text, in.ErrTag = "", "rate_limited"
		s.recordConvo(ctx, in)
		log.Printf("chat: rate limited chat=%s notice=%t", maskChat(chatID), notice)
		if notice {
			s.recordConvo(ctx, ConvoEvent{ChatID: chatID, Direction: "out", Kind: "system", Text: msgRateLimit, ErrTag: "rate_limited"})
			return s.sender.SendReply(ctx, chatID, replyToMsgID, msgRateLimit)
		}
		return nil
	}
	s.recordConvo(ctx, in)
	reply, err := s.completeTools(ctx, chatID, userID, userName, prompt)
	reply = strings.TrimSpace(reply)
	if err != nil || reply == "" {
		log.Printf("chat: completion error (tools): %s chat=%s", errTag(err), maskChat(chatID))
		s.recordConvo(ctx, ConvoEvent{ChatID: chatID, Direction: "out", Kind: "system", Text: msgBusy, ErrTag: failTag(err)})
		rc, cancel := replyCtx(ctx)
		defer cancel()
		return s.sender.SendReply(rc, chatID, replyToMsgID, msgBusy)
	}
	rc, cancel := replyCtx(ctx)
	defer cancel()
	if err := s.sendTools(rc, chatID, replyToMsgID, reply); err != nil {
		s.recordConvo(ctx, ConvoEvent{ChatID: chatID, Direction: "out", Kind: "text", Text: reply, ErrTag: "send_failed"})
		return err
	}
	s.recordConvo(ctx, ConvoEvent{ChatID: chatID, Direction: "out", Kind: "text", Text: reply})
	s.remember(chatID, prompt, reply)
	s.noteUserText(chatID, userID, prompt)
	return nil
}

const (
	msgRateLimit = "Pink đang nhận hơi nhiều tin nhắn, bạn chờ một chút rồi hỏi tiếp nhé 🩷"
	msgBusy      = "⚠️ Pink hơi bận, thử lại sau chút nhé."
)

// failTag is a fixed tag for a failed turn: errTag, or "empty_reply".
func failTag(err error) string {
	if err == nil {
		return "empty_reply"
	}
	return errTag(err)
}

func (s *Service) sendTools(ctx context.Context, chatID string, replyToMsgID int64, reply string) error {
	if ms, ok := s.sender.(MarkdownSender); ok {
		return ms.SendMarkdown(ctx, chatID, reply)
	}
	return s.sender.SendReply(ctx, chatID, replyToMsgID, reply)
}
