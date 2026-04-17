package reminder

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type stubParser struct {
	result ParseResult
	err    error
	calls  int
}

func (s *stubParser) Parse(_ context.Context, _ time.Time, _ string) (ParseResult, error) {
	s.calls++
	return s.result, s.err
}

type sentMessage struct {
	ChatID       string
	ReplyToMsgID int64
	Text         string
}

type stubSender struct {
	mu       sync.Mutex
	sent     []sentMessage
	failNext bool
}

func (s *stubSender) SendReply(_ context.Context, chatID string, replyTo int64, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failNext {
		s.failNext = false
		return errors.New("stub send failure")
	}
	s.sent = append(s.sent, sentMessage{chatID, replyTo, text})
	return nil
}

func (s *stubSender) SendMessage(_ context.Context, chatID string, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, sentMessage{ChatID: chatID, Text: text})
	return nil
}

func newTestService(t *testing.T, parser Parser, sender Sender) *Service {
	t.Helper()
	store := newTestStore(t)
	return NewService(store, parser, sender, func() time.Time {
		return time.Date(2026, 4, 17, 14, 0, 0, 0, time.UTC)
	})
}

func TestCreateFromText_Happy(t *testing.T) {
	ctx := context.Background()
	parser := &stubParser{result: ParseResult{
		OK:   true,
		When: time.Date(2026, 4, 17, 16, 0, 0, 0, time.UTC),
		Task: "ăn cơm",
	}}
	sender := &stubSender{}
	svc := newTestService(t, parser, sender)

	err := svc.HandleIncoming(ctx, IncomingMessage{
		ChatID:    "chat1",
		UserID:    "42",
		UserName:  "Doan",
		MessageID: 777,
		Text:      "2h nữa nhắc tao ăn cơm",
	})
	if err != nil {
		t.Fatalf("HandleIncoming: %v", err)
	}
	if parser.calls != 1 {
		t.Errorf("parser must be called once, got %d", parser.calls)
	}
	if len(sender.sent) != 1 || !strings.Contains(sender.sent[0].Text, "ăn cơm") {
		t.Errorf("expected ack containing task, got %+v", sender.sent)
	}
	list, err := svc.store.ListForChat(ctx, "chat1")
	if err != nil {
		t.Fatalf("ListForChat: %v", err)
	}
	if len(list) != 1 || list[0].Task != "ăn cơm" {
		t.Errorf("expected one stored reminder, got %+v", list)
	}
}

func TestCreateFromText_KeywordMissSkipsParser(t *testing.T) {
	ctx := context.Background()
	parser := &stubParser{}
	sender := &stubSender{}
	svc := newTestService(t, parser, sender)

	err := svc.HandleIncoming(ctx, IncomingMessage{
		ChatID: "chat1", UserID: "42", UserName: "Doan", MessageID: 1,
		Text: "giá vàng hôm nay",
	})
	if err != nil {
		t.Fatalf("HandleIncoming: %v", err)
	}
	if parser.calls != 0 {
		t.Errorf("parser must NOT be called for non-reminder text")
	}
	if len(sender.sent) != 0 {
		t.Errorf("no ack expected for unrelated message, got %+v", sender.sent)
	}
}

func TestCreateFromText_ParserNotOK(t *testing.T) {
	ctx := context.Background()
	parser := &stubParser{result: ParseResult{OK: false, Reason: "Thời điểm mơ hồ"}}
	sender := &stubSender{}
	svc := newTestService(t, parser, sender)

	err := svc.HandleIncoming(ctx, IncomingMessage{
		ChatID: "chat1", UserID: "42", UserName: "Doan", MessageID: 1,
		Text: "nhắc tao cái gì đó đi",
	})
	if err != nil {
		t.Fatalf("HandleIncoming: %v", err)
	}
	if len(sender.sent) != 1 || !strings.Contains(sender.sent[0].Text, "Thời điểm mơ hồ") {
		t.Errorf("expected reason in ack, got %+v", sender.sent)
	}
}

func TestHelp_RoutedFromSlashCommand(t *testing.T) {
	ctx := context.Background()
	parser := &stubParser{}
	sender := &stubSender{}
	svc := newTestService(t, parser, sender)

	err := svc.HandleIncoming(ctx, IncomingMessage{
		ChatID: "chat1", UserID: "42", UserName: "Doan", MessageID: 10,
		Text: "/help",
	})
	if err != nil {
		t.Fatalf("HandleIncoming: %v", err)
	}
	if parser.calls != 0 {
		t.Errorf("parser must NOT be called for /help")
	}
	if len(sender.sent) != 1 {
		t.Fatalf("expected 1 help reply, got %d", len(sender.sent))
	}
	body := sender.sent[0].Text
	if !strings.Contains(body, "nhắc tao") || !strings.Contains(body, "/reminders") || !strings.Contains(body, "/cancel") {
		t.Errorf("help text missing required sections: %q", body)
	}
}

func TestList_RoutedFromSlashCommand(t *testing.T) {
	ctx := context.Background()
	parser := &stubParser{}
	sender := &stubSender{}
	svc := newTestService(t, parser, sender)

	fireAt := time.Date(2026, 4, 18, 14, 0, 0, 0, time.UTC)
	_, _ = svc.store.Create(ctx, Reminder{
		ChatID: "chat1", UserID: "42", UserName: "Doan",
		OriginalMsg: "x", OriginalMsgID: 1, Task: "họp", FireAt: fireAt,
	})

	err := svc.HandleIncoming(ctx, IncomingMessage{
		ChatID: "chat1", UserID: "42", UserName: "Doan", MessageID: 50,
		Text: "/reminders",
	})
	if err != nil {
		t.Fatalf("HandleIncoming: %v", err)
	}
	if len(sender.sent) != 1 || !strings.Contains(sender.sent[0].Text, "họp") {
		t.Errorf("expected list reply with task, got %+v", sender.sent)
	}
}

func TestCancel_RoutedFromSlashCommand(t *testing.T) {
	ctx := context.Background()
	parser := &stubParser{}
	sender := &stubSender{}
	svc := newTestService(t, parser, sender)

	id, _ := svc.store.Create(ctx, Reminder{
		ChatID: "chat1", UserID: "42", UserName: "Doan",
		OriginalMsg: "x", OriginalMsgID: 1, Task: "họp",
		FireAt: time.Date(2026, 4, 18, 14, 0, 0, 0, time.UTC),
	})

	err := svc.HandleIncoming(ctx, IncomingMessage{
		ChatID: "chat1", UserID: "42", UserName: "Doan", MessageID: 51,
		Text: "/cancel " + itoa(id),
	})
	if err != nil {
		t.Fatalf("HandleIncoming: %v", err)
	}
	got, _ := svc.store.GetByID(ctx, id)
	if got.CanceledAt == nil {
		t.Errorf("reminder should be canceled, got %+v", got)
	}
}

func TestSweep_FiresDueReminders(t *testing.T) {
	ctx := context.Background()
	sender := &stubSender{}
	svc := newTestService(t, &stubParser{}, sender)

	now := time.Date(2026, 4, 17, 14, 0, 0, 0, time.UTC)
	id, _ := svc.store.Create(ctx, Reminder{
		ChatID: "chat1", UserID: "42", UserName: "Doan",
		OriginalMsg: "x", OriginalMsgID: 999, Task: "ăn cơm",
		FireAt: now.Add(-1 * time.Minute),
	})

	if err := svc.Sweep(ctx, now); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(sender.sent) != 1 {
		t.Fatalf("expected 1 send, got %d (%+v)", len(sender.sent), sender.sent)
	}
	if sender.sent[0].ReplyToMsgID != 999 {
		t.Errorf("ReplyToMsgID: got %d want 999", sender.sent[0].ReplyToMsgID)
	}
	got, _ := svc.store.GetByID(ctx, id)
	if got.FiredAt == nil {
		t.Errorf("FiredAt should be set after sweep")
	}
}

func TestSweep_FailureIncrementsAttempts(t *testing.T) {
	ctx := context.Background()
	sender := &stubSender{failNext: true}
	svc := newTestService(t, &stubParser{}, sender)

	now := time.Date(2026, 4, 17, 14, 0, 0, 0, time.UTC)
	id, _ := svc.store.Create(ctx, Reminder{
		ChatID: "chat1", UserID: "42", UserName: "Doan",
		OriginalMsg: "x", OriginalMsgID: 1, Task: "ăn cơm",
		FireAt: now.Add(-1 * time.Minute),
	})

	_ = svc.Sweep(ctx, now)
	got, _ := svc.store.GetByID(ctx, id)
	if got.FiredAt != nil {
		t.Errorf("FiredAt must remain nil on send failure")
	}
	if got.Attempts != 1 {
		t.Errorf("Attempts: got %d want 1", got.Attempts)
	}
}

func itoa(n int64) string {
	var b [20]byte
	i := len(b)
	if n == 0 {
		return "0"
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
