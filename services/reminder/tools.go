package reminder

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// ErrTooMany is returned by CreateFor when the chat already has the maximum
// number of pending reminders.
var ErrTooMany = errors.New("too many pending reminders")

const (
	maxPendingPerChat = 50
	maxTaskRunes      = 500
	maxFireAhead      = 366 * 24 * time.Hour
)

// createLocks serializes CreateFor's cap check + insert per Service.
var createLocks sync.Map // *Service -> *sync.Mutex

func (s *Service) createLock() *sync.Mutex {
	m, _ := createLocks.LoadOrStore(s, &sync.Mutex{})
	return m.(*sync.Mutex)
}

func normalizeTask(task string) string {
	return strings.Join(strings.Fields(task), " ")
}

// CreateFor stores a reminder directly (no LLM parsing, nothing sent). The
// time must be in the future, the task non-empty and at most 500 runes, and a
// chat may hold at most 50 pending reminders.
func (s *Service) CreateFor(ctx context.Context, platform, chatID, userID, userName, task string, fireAt time.Time) (int64, error) {
	task = normalizeTask(task)
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
	if platform != PlatformTelegram && platform != PlatformZalo {
		return 0, fmt.Errorf("reminder: unsupported platform %q", platform)
	}
	if fireAt.After(s.now().Add(maxFireAhead)) {
		return 0, errors.New("reminder: time more than 1 year ahead")
	}
	mu := s.createLock()
	mu.Lock()
	defer mu.Unlock()
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

// CancelFor cancels a reminder only when it belongs to exactly this platform
// and chat (for every platform, telegram included). "Not yours" and "does not
// exist" are both ErrNotFound.
func (s *Service) CancelFor(ctx context.Context, platform, chatID string, id int64) error {
	platform = platformOrDefault(platform)
	r, err := s.store.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if r.Platform != platform || r.ChatID != chatID {
		return ErrNotFound
	}
	return s.store.CancelForChat(ctx, platform, chatID, id)
}
