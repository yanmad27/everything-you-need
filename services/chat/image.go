package chat

import (
	"context"
	"encoding/base64"
	"errors"
	"log"
	"regexp"
	"strings"
	"time"

	"everything-you-need/m/services/llm"
	"everything-you-need/m/services/netguard"
)

const (
	maxImageB64     = 7 << 20 // base64 bytes sent to the model
	imageMaxRounds  = 2
	recentQuestionW = 2 * time.Minute
)

// ImageFetcher downloads the message's image (SSRF-safe, size/type checked).
type ImageFetcher func(ctx context.Context) (data []byte, mime string, err error)

var questionRe = regexp.MustCompile(`(?i)(\?|^(hãy|giúp|cho|xem|đọc|dịch|tóm|tính|so sánh|kiểm tra|check|read|translate|what|how)\b|là gì|bao nhiêu|có phải|như thế nào|thế nào|được không|không\s*$)`)

type lastText struct {
	text string
	at   time.Time
}

func lastKey(chatID, userID string) string { return chatID + "\x00" + userID }

// noteUserText remembers a user's latest text (keyed by chat+user) for the
// image "recent question" lookup, pruning entries older than 10 minutes.
func (s *Service) noteUserText(chatID, userID, text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.last == nil {
		s.last = map[string]lastText{}
	}
	now := s.tools.Now()
	for k, v := range s.last {
		if now.Sub(v.at) > 10*time.Minute {
			delete(s.last, k)
		}
	}
	s.last[lastKey(chatID, userID)] = lastText{text, now}
}

// dropLast forgets every user's remembered text for a chat; s.mu must be held.
func (s *Service) dropLast(chatID string) {
	for k := range s.last {
		if strings.HasPrefix(k, chatID+"\x00") {
			delete(s.last, k)
		}
	}
}

func (s *Service) recentQuestion(chatID, userID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.last[lastKey(chatID, userID)]
	if !ok || s.tools.Now().Sub(l.at) > recentQuestionW || !questionRe.MatchString(l.text) {
		return ""
	}
	return l.text
}

const describePrompt = "Người dùng gửi một bức ảnh không kèm lời nhắn. Hãy mô tả ngắn gọn ảnh và hỏi họ cần Pink giúp gì với ảnh này."

func imageFetchReason(err error) string {
	switch {
	case errors.Is(err, netguard.ErrBlocked):
		return "blocked"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	}
	return "failed"
}

// HandleImage answers an image message with a vision-capable tool loop.
// Neither the image URL nor the bytes are logged or stored.
func (s *Service) HandleImage(ctx context.Context, chatID, userID, userName string, replyToMsgID int64, caption string, fetch ImageFetcher) error {
	if s.toolClient == nil {
		return s.sender.SendReply(ctx, chatID, replyToMsgID, "Pink chưa xem được ảnh ở đây, bạn mô tả bằng chữ giúp Pink nhé 🩷")
	}
	if ok, notice := s.limiter.allow(chatID); !ok {
		if notice {
			return s.sender.SendReply(ctx, chatID, replyToMsgID, "Pink đang nhận hơi nhiều tin nhắn, bạn chờ một chút rồi hỏi tiếp nhé 🩷")
		}
		return nil
	}
	caption = strings.Join(strings.Fields(caption), " ")
	data, mime, err := fetch(ctx)
	if err != nil {
		log.Printf("chat image: kind=image bytes=0 mime= outcome=fetch_error reason=%s chat=%s", imageFetchReason(err), maskChat(chatID))
		return s.sender.SendReply(ctx, chatID, replyToMsgID, "Pink không tải được ảnh, bạn gửi lại giúp nhé")
	}
	if base64.StdEncoding.EncodedLen(len(data)) > maxImageB64 {
		log.Printf("chat image: kind=image bytes=%d mime=%s outcome=too_large chat=%s", len(data), mime, maskChat(chatID))
		return s.sender.SendReply(ctx, chatID, replyToMsgID, "Ảnh này lớn quá, Pink chưa đọc được. Bạn gửi ảnh nhỏ hơn giúp nhé")
	}
	log.Printf("chat image: kind=image bytes=%d mime=%s outcome=ok chat=%s", len(data), mime, maskChat(chatID))

	text := caption
	if text == "" {
		if text = s.recentQuestion(chatID, userID); text == "" {
			text = describePrompt
		}
	}
	user := llm.ChatMessage{Role: "user", Parts: []llm.ContentPart{
		{Type: "text", Text: text},
		{Type: "image_url", ImageURL: "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)},
	}}
	data = nil
	reply, err := s.completeToolsMsg(ctx, chatID, userID, userName, caption, user, imageMaxRounds)
	reply = strings.TrimSpace(reply)
	if err != nil || reply == "" {
		log.Printf("chat: completion error (image): %s chat=%s", errTag(err), maskChat(chatID))
		rc, cancel := replyCtx(ctx)
		defer cancel()
		return s.sender.SendReply(rc, chatID, replyToMsgID, "⚠️ Pink hơi bận, thử lại sau chút nhé.")
	}
	rc, cancel := replyCtx(ctx)
	defer cancel()
	if err := s.sendTools(rc, chatID, replyToMsgID, reply); err != nil {
		return err
	}
	s.remember(chatID, strings.TrimSpace("[ảnh] "+caption), reply)
	return nil
}
