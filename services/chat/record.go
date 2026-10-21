package chat

import (
	"context"
	"crypto/rand"
	"encoding/hex"
)

// ConvoEvent is one conversation message for an optional ConvoRecorder (the
// admin store). Text is the plain message/reply text; image bytes and URLs are
// never included, only mime and size.
type ConvoEvent struct {
	Platform, ChatID, ChatType string
	UserID, UserName           string
	TurnID                     string
	Direction                  string // in | out
	Kind                       string // text | image | system
	Text                       string
	ImageMime                  string
	ImageBytes                 int
	ErrTag                     string // fixed tag when the reply failed
}

// ConvoRecorder is implemented by recorders that also want messages. The
// Tools.Recorder is asserted against it, so plain Recorders keep working.
type ConvoRecorder interface {
	RecordConvo(ctx context.Context, e ConvoEvent)
}

// Meta carries optional per-message context from the platform layer.
type Meta struct {
	ChatType string // PRIVATE | GROUP
}

type ctxKey int

const (
	metaKey ctxKey = iota
	turnKey
)

// WithMeta attaches platform metadata (chat type) to ctx for recording.
func WithMeta(ctx context.Context, m Meta) context.Context {
	return context.WithValue(ctx, metaKey, m)
}

func metaFrom(ctx context.Context) Meta {
	m, _ := ctx.Value(metaKey).(Meta)
	return m
}

func turnIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(turnKey).(string)
	return id
}

// newTurn assigns a turn id grouping a user message, tool calls and the reply.
func newTurn(ctx context.Context) context.Context {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return context.WithValue(ctx, turnKey, hex.EncodeToString(b[:]))
}

// recordConvo forwards a message to the recorder if it supports messages.
// Recorder panics are swallowed.
func (s *Service) recordConvo(ctx context.Context, e ConvoEvent) {
	cr, ok := s.tools.Recorder.(ConvoRecorder)
	if !ok {
		return
	}
	e.Platform = s.tools.Platform
	e.TurnID = turnIDFrom(ctx)
	if e.ChatType == "" {
		e.ChatType = metaFrom(ctx).ChatType
	}
	s.record(func() { cr.RecordConvo(ctx, e) })
}
