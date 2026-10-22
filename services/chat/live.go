package chat

import (
	"context"
	"log"
	"strings"

	"everything-you-need/m/services/llm"
)

// Live is the per-turn view of settings that can change while the bot runs
// (Zalo, from the admin UI). One Live is captured per inbound message and used
// for the whole turn. The zero value (Set=false) changes nothing.
type Live struct {
	Set    bool
	Paused bool // answer nothing

	Client llm.ToolCompleter // nil = the service's static client
	Model  string            // recorded with usage; "" = static
	// Persona replaces the built-in system prompt base when non-empty.
	Persona string

	// DisabledTools holds tool names that must not be offered.
	DisabledTools map[string]bool
	ImagesOff     bool

	// Search overrides the web-search backend (built from the current key); nil = static.
	Search WebSearch
	// FetchAllow / DueChats replace the static lists when Set.
	FetchAllow []string
	DueChats   []string

	RatePerMin float64 // 0 = unchanged
	Burst      int
}

// LiveSource returns the current Live; it must be cheap and goroutine-safe.
type LiveSource func() Live

type liveKey struct{}

func liveFrom(ctx context.Context) Live {
	lv, _ := ctx.Value(liveKey{}).(Live)
	return lv
}

// WithLive makes the service read per-turn settings from src (Zalo only).
func (s *Service) WithLive(src LiveSource) *Service {
	s.liveSrc = src
	return s
}

// CaptureLive reads the settings once and stores them in the returned context;
// callers (the Zalo dispatcher) use it so the whole turn sees one snapshot. A
// service without a source (Telegram) returns the context unchanged.
func (s *Service) CaptureLive(ctx context.Context) (context.Context, Live) {
	if s == nil || s.liveSrc == nil {
		return ctx, Live{}
	}
	if _, ok := ctx.Value(liveKey{}).(Live); ok {
		return ctx, liveFrom(ctx)
	}
	lv := s.liveSrc()
	lv.Set = true
	if s.limiter != nil && lv.RatePerMin > 0 && lv.Burst > 0 {
		s.limiter.resize(lv.Burst, lv.RatePerMin)
	}
	return context.WithValue(ctx, liveKey{}, lv), lv
}

// NotePaused records (outcome=paused) an inbound message that is not answered.
func (s *Service) NotePaused(ctx context.Context, chatID, userID, userName, text string) {
	log.Printf("chat: paused chat=%s outcome=paused", maskChat(chatID))
	if s == nil || s.tools.Recorder == nil {
		return
	}
	ctx = newTurn(ctx)
	s.recordConvo(ctx, ConvoEvent{ChatID: chatID, UserID: userID, UserName: userName, Direction: "in", Kind: "text", Text: text, ErrTag: "paused"})
}

// toolEnabled reports whether the named tool may be offered this turn.
func (lv Live) toolEnabled(name string) bool { return !lv.DisabledTools[name] }

func (lv Live) fetchAllowOr(static []string) []string {
	if !lv.Set {
		return static
	}
	var out []string
	for _, d := range lv.FetchAllow {
		if d = strings.ToLower(strings.Trim(strings.TrimSpace(d), ".")); d != "" {
			out = append(out, d)
		}
	}
	if len(out) == 0 {
		return defaultFetchAllow
	}
	return out
}

func (lv Live) dueChat(static map[string]bool, chatID string) bool {
	if !lv.Set {
		return static[chatID]
	}
	for _, c := range lv.DueChats {
		if strings.TrimSpace(c) == chatID {
			return true
		}
	}
	return false
}

const msgImagesOff = "Pink đang tắt xử lý ảnh 🩷"
