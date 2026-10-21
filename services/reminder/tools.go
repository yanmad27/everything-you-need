package reminder

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// ErrTooMany is returned by CreateFor when the chat already has the maximum
// number of pending reminders.
var ErrTooMany = errors.New("too many pending reminders")

const (
	maxPendingPerChat = 50
	maxTaskRunes      = 500
)

// CreateFor stores a reminder directly (no LLM parsing, nothing sent). The
// time must be in the future, the task non-empty and at most 500 runes, and a
// chat may hold at most 50 pending reminders.
func (s *Service) CreateFor(ctx context.Context, platform, chatID, userID, userName, task string, fireAt time.Time) (int64, error) {
	task = strings.TrimSpace(task)
	if task == "" {
		return 0, errors.New("reminder: empty task")
	}
	if utf8.RuneCountInString(task) > maxTaskRunes {
		return 0, fmt.Errorf("reminder: task longer than %d characters", maxTaskRunes)
	}
	if chatID == "" {
		return 0, errors.New("reminder: empty chat id")
	}
	if !fireAt.After(s.now()) {
		return 0, errors.New("reminder: time must be in the future")
	}
	platform = platformOrDefault(platform)
	pending, err := s.store.ListForPlatformChat(ctx, platform, chatID)
	if err != nil {
		return 0, err
	}
	if len(pending) >= maxPendingPerChat {
		return 0, ErrTooMany
	}
	return s.store.Create(ctx, Reminder{
		Platform: platform,
		ChatID:   chatID,
		UserID:   userID,
		UserName: userName,
		Task:     task,
		FireAt:   fireAt.UTC(),
	})
}

// ListFor lists pending reminders for one platform + chat.
func (s *Service) ListFor(ctx context.Context, platform, chatID string) ([]Reminder, error) {
	return s.store.ListForPlatformChat(ctx, platformOrDefault(platform), chatID)
}

// CancelFor cancels a reminder with the same scoping as Store.CancelForChat.
func (s *Service) CancelFor(ctx context.Context, platform, chatID string, id int64) error {
	return s.store.CancelForChat(ctx, platformOrDefault(platform), chatID, id)
}
