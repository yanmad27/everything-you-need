package reminder

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"everything-you-need/m/services/lunar"
)

// Sender is the subset of the Telegram bot used by the reminder service.
type Sender interface {
	SendReply(ctx context.Context, chatID string, replyToMsgID int64, text string) error
	SendMessage(ctx context.Context, chatID, text string) error
}

// IncomingMessage is the normalized inbound message from a chat platform.
type IncomingMessage struct {
	Platform  string // PlatformTelegram (default when empty) or PlatformZalo
	ChatID    string
	UserID    string
	UserName  string
	MessageID int64
	Text      string
}

// Service wires Store + Parser + Sender into the bot command handler.
type Service struct {
	store  *Store
	parser Parser
	sender Sender // default (Telegram) sender
	extra  map[string]Sender
	parsers map[string]Parser // per-platform overrides of parser
	now    func() time.Time
}

// WithSender registers the sender used for a non-default platform.
func (s *Service) WithSender(platform string, sender Sender) *Service {
	if s.extra == nil {
		s.extra = map[string]Sender{}
	}
	s.extra[platform] = sender
	return s
}

// WithParser sets a platform-specific parser (e.g. a different LLM backend).
func (s *Service) WithParser(platform string, p Parser) *Service {
	if s.parsers == nil {
		s.parsers = map[string]Parser{}
	}
	s.parsers[platform] = p
	return s
}

func (s *Service) parserFor(platform string) Parser {
	if p, ok := s.parsers[platform]; ok && p != nil {
		return p
	}
	return s.parser
}

// senderFor returns the sender for a platform, or nil if none is configured.
func (s *Service) senderFor(platform string) Sender {
	if platform == "" || platform == PlatformTelegram {
		return s.sender
	}
	return s.extra[platform]
}

// reply sends a reply to the incoming message on its own platform.
func (s *Service) reply(ctx context.Context, msg IncomingMessage, text string) error {
	snd := s.senderFor(msg.Platform)
	if snd == nil {
		return fmt.Errorf("no sender configured for platform %q", msg.Platform)
	}
	return snd.SendReply(ctx, msg.ChatID, msg.MessageID, text)
}

func NewService(store *Store, parser Parser, sender Sender, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{store: store, parser: parser, sender: sender, now: now}
}

// Matches reports whether the text is a reminder command/keyword this service
// would act on. Used to give reminders priority over the chat handler.
func (s *Service) Matches(text string) bool {
	t := strings.TrimSpace(text)
	return matchHelpCommand(t) || matchListCommand(t) || matchCancelCommand(t) ||
		matchLunarCommand(t) || MatchesReminderKeyword(t)
}

// HandleIncoming routes a message to the right internal handler.
func (s *Service) HandleIncoming(ctx context.Context, msg IncomingMessage) error {
	text := strings.TrimSpace(msg.Text)

	switch {
	case matchHelpCommand(text):
		return s.handleHelp(ctx, msg)
	case matchListCommand(text):
		return s.handleList(ctx, msg)
	case matchCancelCommand(text):
		return s.handleCancel(ctx, msg, text)
	case matchLunarCommand(text):
		return s.handleLunar(ctx, msg)
	case MatchesReminderKeyword(text):
		return s.handleCreate(ctx, msg)
	}
	return nil
}

const helpText = `🤖 *Bot Nhắc Nhở*

*Tạo nhắc:* gõ tự nhiên bằng tiếng Việt, chứa "nhắc tao/tôi/mình/ae/mọi người"
• ` + "`2h nữa nhắc tao ăn cơm`" + `
• ` + "`19h mai nhắc tôi họp với Huy`" + `
• ` + "`thứ 2 tới nhắc mình nộp báo cáo`" + `
• ` + "`lúc 20:30 nhắc ae đi đá bóng`" + `

*Lệnh:*
• /reminders — danh sách nhắc đang chờ
• /cancel <id> — hủy một nhắc (vd: ` + "`/cancel 3`" + `)
• ` + "`lịch âm`" + ` hoặc ` + "`âm lịch`" + ` — xem ngày âm lịch hôm nay
• /help — hiện hướng dẫn này

Nhắc sẽ bắn vào kênh đúng giờ, reply lại tin nhắn gốc của bạn.`

func (s *Service) handleHelp(ctx context.Context, msg IncomingMessage) error {
	return s.reply(ctx, msg, helpText)
}

func matchHelpCommand(text string) bool {
	lower := strings.ToLower(text)
	return lower == "/help" || strings.HasPrefix(lower, "/help ") ||
		strings.HasPrefix(lower, "/help@")
}

func (s *Service) handleCreate(ctx context.Context, msg IncomingMessage) error {
	result, err := s.parserFor(msg.Platform).Parse(ctx, s.now(), msg.Text)
	if err != nil {
		log.Printf("reminder parser error: %v", err)
		return s.reply(ctx, msg, "⚠️ Mình đang bận, thử lại sau 1 phút nhé.")
	}
	if !result.OK {
		return s.reply(ctx, msg, "❓ Mình không hiểu: "+result.Reason+". Thử lại nhé.")
	}

	reminder := Reminder{
		Platform:       msg.Platform,
		ChatID:         msg.ChatID,
		UserID:         msg.UserID,
		UserName:       msg.UserName,
		OriginalMsg:    msg.Text,
		OriginalMsgID:  msg.MessageID,
		Task:           result.Task,
		FireAt:         result.When,
	}
	id, err := s.store.Create(ctx, reminder)
	if err != nil {
		log.Printf("reminder create error: %v", err)
		return s.reply(ctx, msg, "⚠️ Không lưu được nhắc nhở, thử lại sau nhé.")
	}

	fireAtLocal := result.When.In(vietnamLocation())
	ack := fmt.Sprintf("✅ Đã đặt nhắc #%d: \"%s\" vào %s",
		id, result.Task, fireAtLocal.Format("15:04 02/01/2006"))
	return s.reply(ctx, msg, ack)
}

func (s *Service) handleList(ctx context.Context, msg IncomingMessage) error {
	list, err := s.store.ListForPlatformChat(ctx, platformOrDefault(msg.Platform), msg.ChatID)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		return s.reply(ctx, msg, "📭 Không có nhắc nhở nào đang chờ.")
	}

	var sb strings.Builder
	sb.WriteString("📋 Nhắc nhở đang chờ:\n")
	for _, r := range list {
		fireAtLocal := r.FireAt.In(vietnamLocation())
		fmt.Fprintf(&sb, "• #%d — %s — %s\n",
			r.ID, fireAtLocal.Format("15:04 02/01/2006"), r.Task)
	}
	sb.WriteString("\nHủy bằng: /cancel <id>")
	return s.reply(ctx, msg, sb.String())
}

func (s *Service) handleCancel(ctx context.Context, msg IncomingMessage, text string) error {
	id, err := extractCancelID(text)
	if err != nil {
		return s.reply(ctx, msg, "❓ Dùng: /cancel <id> hoặc: hủy nhắc <id>")
	}
	err = s.store.CancelForChat(ctx, platformOrDefault(msg.Platform), msg.ChatID, id)
	switch {
	case errors.Is(err, ErrNotFound):
		return s.reply(ctx, msg, fmt.Sprintf("❓ Không tìm thấy nhắc #%d.", id))
	case errors.Is(err, ErrAlreadyFired):
		return s.reply(ctx, msg, fmt.Sprintf("⏰ Nhắc #%d đã chạy rồi.", id))
	case err != nil:
		log.Printf("cancel reminder error: %v", err)
		return s.reply(ctx, msg, "⚠️ Không hủy được, thử lại sau nhé.")
	}
	return s.reply(ctx, msg, fmt.Sprintf("🗑 Đã hủy nhắc #%d.", id))
}

// PurgeOld deletes reminders fired/canceled before remindersCutoff and update rows before updatesCutoff.
func (s *Service) PurgeOld(ctx context.Context, remindersCutoff, updatesCutoff time.Time) (int64, int64, error) {
	return s.store.PurgeOld(ctx, remindersCutoff, updatesCutoff)
}

// Sweep fires all due reminders. Intended to be called once per minute from the scheduler.
func (s *Service) Sweep(ctx context.Context, now time.Time) error {
	pending, err := s.store.ListPending(ctx, now, 50)
	if err != nil {
		return fmt.Errorf("list pending: %w", err)
	}
	warned := map[string]bool{}
	for _, r := range pending {
		text := formatFireMessage(r, now)
		snd := s.senderFor(r.Platform)
		if snd == nil {
			// Platform not configured right now: leave the reminder pending
			// (no attempt counted) so it fires once that platform is back.
			if !warned[r.Platform] {
				warned[r.Platform] = true
				log.Printf("reminder sweep: no sender configured for platform %q, leaving its reminders pending", r.Platform)
			}
			continue
		}
		sendErr := snd.SendReply(ctx, r.ChatID, r.OriginalMsgID, text)
		if sendErr != nil {
			log.Printf("reminder #%d send failed (attempts=%d): %v", r.ID, r.Attempts+1, sendErr)
			if incErr := s.store.IncrementAttempts(ctx, r.ID); incErr != nil {
				log.Printf("increment attempts #%d: %v", r.ID, incErr)
			}
			continue
		}
		if fErr := s.store.MarkFired(ctx, r.ID, now); fErr != nil {
			log.Printf("mark fired #%d: %v", r.ID, fErr)
		}
	}
	return nil
}

func platformOrDefault(p string) string {
	if p == "" {
		return PlatformTelegram
	}
	return p
}

func formatFireMessage(r Reminder, now time.Time) string {
	mention := r.UserName
	if mention == "" {
		mention = "bạn"
	}
	late := ""
	if delay := now.Sub(r.FireAt); delay > 5*time.Minute {
		late = fmt.Sprintf("(trễ %d phút) ", int(delay.Minutes()))
	}
	return fmt.Sprintf("⏰ %sNhắc @%s: %s", late, mention, r.Task)
}

var lunarCommandAliases = map[string]struct{}{
	"lich am": {}, "am lich": {},
	"lịch âm": {}, "âm lịch": {},
	"/lichit": {}, "/amlich": {},
}

func matchLunarCommand(text string) bool {
	normalized := strings.ToLower(strings.TrimSpace(text))
	_, ok := lunarCommandAliases[normalized]
	return ok
}

func (s *Service) handleLunar(ctx context.Context, msg IncomingMessage) error {
	today := s.now().In(vietnamLocation())
	lunarDay, lunarMonth, lunarYear, leap := lunar.SolarToLunar(
		today.Year(), int(today.Month()), today.Day(), lunar.VietnamTimeZone,
	)
	leapSuffix := ""
	if leap == 1 {
		leapSuffix = " (nhuận)"
	}
	text := fmt.Sprintf("🌙 *Lịch Âm*\n📅 Dương lịch: %s\n🗓 Âm lịch: %d/%d/%d%s",
		today.Format("02/01/2006"), lunarDay, lunarMonth, lunarYear, leapSuffix)
	return s.reply(ctx, msg, text)
}

func matchListCommand(text string) bool {
	lower := strings.ToLower(text)
	return strings.HasPrefix(lower, "/reminders") ||
		strings.HasPrefix(lower, "danh sách nhắc")
}

func matchCancelCommand(text string) bool {
	lower := strings.ToLower(text)
	return strings.HasPrefix(lower, "/cancel ") ||
		strings.HasPrefix(lower, "hủy nhắc ")
}

func extractCancelID(text string) (int64, error) {
	fields := strings.Fields(text)
	if len(fields) < 2 {
		return 0, fmt.Errorf("missing id")
	}
	// "hủy nhắc <id>" → id is fields[2], "/cancel <id>" → id is fields[1]
	candidate := fields[len(fields)-1]
	return strconv.ParseInt(candidate, 10, 64)
}

func vietnamLocation() *time.Location {
	loc, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		return time.FixedZone("UTC+7", 7*60*60)
	}
	return loc
}
