package chat

import (
	"context"
	"log"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Reactor is implemented by senders that can react to the user's message
// (Telegram). Zalo has no reaction API and does not implement it.
type Reactor interface {
	React(ctx context.Context, chatID string, msgID int64, emoji string) error
}

// reactionGap is the minimum time between two reactions in one chat.
const reactionGap = 3 * time.Second

var reactionTimeout = 6 * time.Second

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

// reactionRule is one keyword group. exact rules compare accented NFC tokens
// (the words collide with others once accents are folded: yêu/yếu, tuyệt/tuyết,
// buồn/buôn, mệt/mẹt); the others compare accent-folded tokens.
type reactionRule struct {
	words []string
	out   string
	exact bool
}

// rules are checked in order, so idioms ("buồn cười") precede their parts ("buồn").
var reactionRules = []reactionRule{
	{[]string{"chuc mung", "sinh nhat", "congrats", "congratulations"}, "🎉", false},
	{[]string{"cam on", "thanks", "thank you", "thank", "tks", "thx"}, "🙏", false},
	{[]string{"haha", "hahaha", "hihi", "hehe", "kkk", "kkkk", "lol", "ahihi"}, "🤣", false},
	{[]string{"buồn cười"}, "🤣", true},
	{[]string{"yêu", "thương quá"}, "❤", true},
	{[]string{"giỏi quá", "tuyệt", "xuất sắc", "hay quá", "đỉnh quá"}, "👏", true},
	{[]string{"buồn", "mệt", "chán quá", "thất vọng"}, "😢", true},
	{[]string{"troi oi", "that a", "that ha", "wow", "omg"}, "😱", false},
	{[]string{"khong the tin"}, "🤯", false},
}

// excludedPhrases are accented token sequences that must never trigger a rule.
var excludedPhrases = [][]string{{"yêu", "cầu"}} // "yêu cầu" = request, not love

var negators = map[string]bool{"không": true, "chẳng": true, "chả": true, "chưa": true, "đâu": true, "khong": true}

func foldWords(w string) []string { return strings.Fields(fold(w)) }

func hasSeq(toks []string, at int, seq []string) bool {
	if at+len(seq) > len(toks) {
		return false
	}
	for k, w := range seq {
		if toks[at+k] != w {
			return false
		}
	}
	return true
}

// negatedAt reports a negator within the two tokens before position i.
func negatedAt(acc []string, i int) bool {
	for k := i - 2; k < i; k++ {
		if k >= 0 && negators[acc[k]] {
			return true
		}
	}
	return false
}

// chooseReaction picks one allowed reaction emoji for the user's own text, or
// "" to skip. Pure and cheap: no LLM call.
func chooseReaction(userText string) string {
	text := norm.NFC.String(strings.ToLower(userText))
	for _, e := range emojiMap {
		for _, s := range e.in {
			if strings.Contains(text, s) {
				return e.out
			}
		}
	}
	// Negation never reaches across a clause boundary ("không buồn, cảm ơn").
	for _, clause := range strings.FieldsFunc(text, func(r rune) bool { return strings.ContainsRune(",.;:!?…\n", r) }) {
		if out := chooseFromClause(clause); out != "" {
			return out
		}
	}
	return ""
}

func chooseFromClause(text string) string {
	all := strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.Is(unicode.M, r)
	})
	// Drop excluded phrases in one left-to-right pass over token sequences.
	var acc []string
	for i := 0; i < len(all); {
		skipped := false
		for _, ex := range excludedPhrases {
			if hasSeq(all, i, ex) {
				i += len(ex)
				skipped = true
				break
			}
		}
		if !skipped {
			acc = append(acc, all[i])
			i++
		}
	}
	fol := make([]string, len(acc))
	for i, t := range acc {
		fol[i] = fold(t)
	}
	for _, r := range reactionRules {
		toks := fol
		if r.exact {
			toks = acc
		}
		for _, w := range r.words {
			seq := strings.Fields(w)
			if !r.exact {
				seq = foldWords(w)
			}
			for i := range toks {
				if hasSeq(toks, i, seq) && !negatedAt(acc, i) {
					return r.out
				}
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
	if emoji == "" || !s.reactAllowed(chatID) {
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

// reactAllowed enforces at most one reaction per chat per reactionGap, so a
// burst of messages cannot spawn unbounded goroutines. Entries are pruned.
func (s *Service) reactAllowed(chatID string) bool {
	now := time.Now()
	if s.reactNow != nil {
		now = s.reactNow()
	}
	s.reactMu.Lock()
	defer s.reactMu.Unlock()
	if last, ok := s.reactLast[chatID]; ok && now.Sub(last) < reactionGap {
		return false
	}
	if s.reactLast == nil {
		s.reactLast = make(map[string]time.Time)
	}
	if len(s.reactLast) >= 512 {
		for k, t := range s.reactLast {
			if now.Sub(t) >= reactionGap {
				delete(s.reactLast, k)
			}
		}
	}
	s.reactLast[chatID] = now
	return true
}
