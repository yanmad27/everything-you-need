package chat

import (
	"context"
	"log"
	"strings"
	"time"
	"unicode"
)

// Reactor is implemented by senders that can react to the user's message
// (Telegram). Zalo has no reaction API and does not implement it.
type Reactor interface {
	React(ctx context.Context, chatID string, msgID int64, emoji string) error
}

const reactionTimeout = 6 * time.Second

// allowedReactions are the bare codepoints (no VS16) Telegram accepts that
// chooseReaction may return. The pink heart is deliberately absent.
var allowedReactions = map[string]bool{
	"❤": true, "👍": true, "🔥": true, "😁": true, "🤣": true, "😢": true, "😭": true,
	"🙏": true, "👏": true, "🤔": true, "😱": true, "🎉": true, "🤩": true, "👌": true,
	"😍": true, "💯": true, "🤗": true, "🫡": true, "👀": true, "✍": true, "🤝": true, "🤯": true,
}

// emojiMap maps user emoji onto an allowed equivalent.
var emojiMap = []struct {
	in  []string
	out string
}{
	{[]string{"🤣", "😂", "😆", "😹"}, "🤣"},
	{[]string{"❤", "💕", "💖", "💗", "🩷", "😘", "🥰", "💓"}, "❤"},
	{[]string{"😭"}, "😭"},
	{[]string{"😢", "🥲", "😞", "😔"}, "😢"},
	{[]string{"🎉", "🥳", "🎂"}, "🎉"},
	{[]string{"😱", "😮", "😲"}, "😱"},
	{[]string{"🤯"}, "🤯"},
	{[]string{"🙏"}, "🙏"},
	{[]string{"👏"}, "👏"},
	{[]string{"🔥"}, "🔥"},
	{[]string{"😍"}, "😍"},
	{[]string{"👍"}, "👍"},
}

// keyword rules, checked in order; words are accent-folded and matched on
// whole-word boundaries.
var reactionRules = []struct {
	words []string
	out   string
}{
	{[]string{"chuc mung", "sinh nhat", "congrats", "congratulations"}, "🎉"},
	{[]string{"cam on", "thanks", "thank you", "thank", "tks", "thx", "cảm ơn"}, "🙏"},
	{[]string{"haha", "hahaha", "hihi", "hehe", "kkk", "kkkk", "lol", "ahihi"}, "🤣"},
	{[]string{"yeu", "yeu qua", "thuong qua", "love you"}, "❤"},
	{[]string{"gioi qua", "tuyet", "tuyet voi", "xuat sac", "hay qua", "qua dinh", "dinh qua"}, "👏"},
	{[]string{"buon", "met", "chan qua", "that vong"}, "😢"},
	{[]string{"troi oi", "that a", "that ha", "wow", "omg"}, "😱"},
	{[]string{"khong the tin"}, "🤯"},
}

// chooseReaction picks one allowed reaction emoji for the user's own text, or
// "" to skip. Pure and cheap: no LLM call.
func chooseReaction(userText string) string {
	for _, e := range emojiMap {
		for _, s := range e.in {
			if strings.Contains(userText, s) {
				return e.out
			}
		}
	}
	folded := fold(strings.Map(func(r rune) rune {
		if unicode.IsPunct(r) {
			return ' '
		}
		return r
	}, userText))
	padded := " " + strings.Join(strings.Fields(folded), " ") + " "
	padded = strings.ReplaceAll(padded, " yeu cau ", " ") // "yêu cầu" = request, not love
	for _, r := range reactionRules {
		for _, w := range r.words {
			if strings.Contains(padded, " "+fold(w)+" ") {
				return r.out
			}
		}
	}
	return ""
}

// maybeReact fires a detached reaction for a Telegram turn that will be
// answered. It never blocks or alters the reply path.
func (s *Service) maybeReact(chatID string, msgID int64, prompt string) {
	if !s.reactions || msgID == 0 || s.toolClient != nil {
		return
	}
	r, ok := s.sender.(Reactor)
	if !ok {
		return
	}
	emoji := chooseReaction(prompt)
	if emoji == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), reactionTimeout)
		defer cancel()
		if err := r.React(ctx, chatID, msgID, emoji); err != nil {
			log.Printf("chat: reaction failed")
		}
	}()
}

// WithReactions turns Telegram reactions on or off (off by default).
func (s *Service) WithReactions(on bool) *Service {
	s.reactions = on
	return s
}
