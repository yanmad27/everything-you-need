package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"everything-you-need/m/services/llm"
	"everything-you-need/m/services/lunar"
	"everything-you-need/m/services/reminder"
)

// Consumer-side views of the services the tools read; main adapts the
// concrete services so tests can use fakes.

type PriceQuote struct {
	Type, Unit, Currency string
	Buy, Sell            float64
}

// PriceReader serves cached prices by display name (as listed by Sources).
type PriceReader interface {
	Sources() []string
	Get(ctx context.Context, source string) ([]PriceQuote, time.Time, error)
}

type WebHit struct{ Title, URL, Snippet, Source, Date string }

type WebSearch interface {
	Search(ctx context.Context, q string, n int) ([]WebHit, error)
	SearchNews(ctx context.Context, q string, n int) ([]WebHit, error)
}

type NewsReader interface {
	RecentPosted(ctx context.Context, limit int) ([]WebHit, error)
}

type DueEntry struct{ Title, Due, Bucket string }

type DueReader interface {
	Upcoming(ctx context.Context, days int) ([]DueEntry, error)
}

type ReminderInfo struct {
	ID     int64
	Task   string
	FireAt time.Time
}

// ReminderTools is scoped by the caller: platform and chat always come from
// the service, never from model arguments.
type ReminderTools interface {
	Create(ctx context.Context, platform, chatID, userID, userName, task string, at time.Time) (int64, error)
	List(ctx context.Context, platform, chatID string) ([]ReminderInfo, error)
	Cancel(ctx context.Context, platform, chatID string, id int64) error
}

// Tools wires the tool-calling path. Nil dependencies omit their tools.
type Tools struct {
	Platform  string // reminder scope, e.g. "zalo"
	Prices    PriceReader
	Search    WebSearch
	News      NewsReader
	Due       DueReader
	Reminders ReminderTools
	Fetch     func(ctx context.Context, url string) (string, error) // default: SSRF-safe reader
	Recorder  Recorder
	Model     string // LLM model name, recorded with usage
	Now       func() time.Time

	// DueChats lists the chat ids allowed to use due_dates (private data); empty disables it.
	DueChats []string
	// FetchAllowDomains: domains (exact or subdomain) the model may fetch on its
	// own; empty = defaultFetchAllow.
	FetchAllowDomains []string
	// SearchPerHour is the per-process web_search budget (default 200).
	SearchPerHour int

	// Limits; zero values use the defaults below.
	MaxRounds       int
	MaxCallsPerTurn int
	ToolTimeout     time.Duration
	LoopDeadline    time.Duration
	MaxResultChars  int
	RateBurst       int
	RatePerMinute   float64
}

const (
	defMaxRounds    = 4
	defMaxCalls     = 3
	defToolTimeout  = 15 * time.Second
	defLoopDeadline = 45 * time.Second
	defMaxResult    = 6000 // a list page (e.g. GitHub trending, 25 items) must fit
	defRateBurst    = 3
	defRatePerMin   = 6.0

	maxSearchesPerTurn = 3
	maxFetchesPerTurn  = 4
	defSearchPerHour   = 200
)

// ictZone is Vietnam time; a fixed zone avoids depending on tzdata.
var ictZone = time.FixedZone("ICT", 7*3600)

// userError is shown to the model verbatim; any other error becomes a generic
// failure so internals never leak into the conversation.
type userError string

func (e userError) Error() string { return string(e) }

var errTimeout = errors.New("tool timed out")

// sourceID maps a price source display name to a short stable id.
func sourceID(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch r {
		case 'đ':
			b.WriteRune('d')
			continue
		}
		if r < 128 {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				b.WriteRune(r)
			}
			continue
		}
		// strip Vietnamese diacritics via a small fold table
		if f, ok := foldVN[r]; ok {
			b.WriteRune(f)
		}
	}
	id := b.String()
	switch {
	case strings.Contains(id, "kimkhoa"):
		return "kimkhoa"
	case strings.Contains(id, "baotinminhchau"):
		return "btmc"
	}
	return id
}

var foldVN = func() map[rune]rune {
	m := map[rune]rune{}
	for base, set := range map[rune]string{
		'a': "àáảãạăằắẳẵặâầấẩẫậ", 'e': "èéẻẽẹêềếểễệ", 'i': "ìíỉĩị",
		'o': "òóỏõọôồốổỗộơờớởỡợ", 'u': "ùúủũụưừứửữự", 'y': "ỳýỷỹỵ",
	} {
		for _, r := range set {
			m[r] = base
		}
	}
	return m
}()

type toolImpl struct {
	def       llm.ToolDef
	run       func(ctx context.Context, t *turn, args json.RawMessage) (string, error)
	taints    bool // brings untrusted content into the turn
	sensitive bool // exposes or changes private data; disabled once the turn is tainted
}

func schema(s string) json.RawMessage { return json.RawMessage(s) }

// buildTools returns the enabled tools in a stable order.
func (s *Service) buildTools() []toolImpl {
	tl := s.tools
	var out []toolImpl
	if tl.Prices != nil {
		ids := map[string]string{}
		for _, n := range tl.Prices.Sources() {
			ids[sourceID(n)] = n
		}
		if len(ids) > 0 {
			keys := make([]string, 0, len(ids))
			for k := range ids {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			enum, _ := json.Marshal(keys)
			out = append(out, toolImpl{
				def: llm.ToolDef{Name: "get_prices", Description: "Giá vàng/bạc/coin mới nhất từ một nguồn: " + strings.Join(keys, ", ") + ".",
					Parameters: schema(`{"type":"object","properties":{"source":{"type":"string","enum":` + string(enum) + `}},"required":["source"]}`)},
				run: func(ctx context.Context, _ *turn, raw json.RawMessage) (string, error) {
					var a struct{ Source string }
					if err := json.Unmarshal(raw, &a); err != nil {
						return "", userError("invalid arguments")
					}
					name, ok := ids[a.Source]
					if !ok {
						return "", userError("unknown source; use one of " + strings.Join(keys, ", "))
					}
					qs, at, err := tl.Prices.Get(ctx, name)
					if err != nil {
						return "", err
					}
					var b strings.Builder
					fmt.Fprintf(&b, "Nguồn %s, cập nhật %s\n", name, at.In(ictZone).Format("02/01 15:04"))
					for _, q := range qs {
						fmt.Fprintf(&b, "- %s: mua %.0f, bán %.0f %s/%s\n", q.Type, q.Buy, q.Sell, q.Currency, q.Unit)
					}
					return b.String(), nil
				},
			})
		}
	}
	out = append(out, toolImpl{
		def: llm.ToolDef{Name: "lunar_date", Description: "Đổi ngày dương sang âm lịch. Bỏ trống date = hôm nay (giờ Việt Nam).",
			Parameters: schema(`{"type":"object","properties":{"date":{"type":"string","description":"YYYY-MM-DD"}}}`)},
		run: func(_ context.Context, t *turn, raw json.RawMessage) (string, error) {
			var a struct{ Date string }
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &a); err != nil {
					return "", userError("invalid arguments")
				}
			}
			d := t.now().In(ictZone)
			if a.Date != "" {
				p, err := time.ParseInLocation("2006-01-02", a.Date, ictZone)
				if err != nil {
					return "", userError("date must be YYYY-MM-DD")
				}
				d = p
			}
			ld, lm, ly, leap := lunar.SolarToLunar(d.Year(), int(d.Month()), d.Day(), lunar.VietnamTimeZone)
			return lunarText(d, ld, lm, ly, leap), nil
		},
	})
	if tl.News != nil {
		out = append(out, toolImpl{
			taints: true,
			def: llm.ToolDef{Name: "news_recent", Description: "Các tin tức đã đăng gần đây trên kênh tin của bot.",
				Parameters: schema(`{"type":"object","properties":{"limit":{"type":"integer","minimum":1,"maximum":5}}}`)},
			run: func(ctx context.Context, _ *turn, raw json.RawMessage) (string, error) {
				var a struct{ Limit int }
				_ = json.Unmarshal(raw, &a)
				hits, err := tl.News.RecentPosted(ctx, clamp(a.Limit, 1, 5, 5))
				if err != nil {
					return "", err
				}
				return formatHits(hits), nil
			},
		})
	}
	if tl.Search != nil || s.liveSrc != nil {
		out = append(out, toolImpl{
			taints: true,
			def: llm.ToolDef{Name: "web_search", Description: "Tìm kiếm web hoặc tin tức mới.",
				Parameters: schema(`{"type":"object","properties":{"query":{"type":"string"},"kind":{"type":"string","enum":["web","news"]},"n":{"type":"integer","minimum":1,"maximum":5}},"required":["query"]}`)},
			run: func(ctx context.Context, t *turn, raw json.RawMessage) (string, error) {
				var a struct {
					Query, Kind string
					N           int
				}
				if err := json.Unmarshal(raw, &a); err != nil || strings.TrimSpace(a.Query) == "" {
					return "", withReason("invalid_args", userError("invalid arguments: query required"))
				}
				if t.countSearch() > maxSearchesPerTurn || !s.searchBudget.take(tl.Now()) {
					return "", withReason("search_budget", userError("đã hết lượt tìm kiếm, hãy trả lời bằng thông tin đã có"))
				}
				n := clamp(a.N, 1, 5, 5)
				var hits []WebHit
				var err error
				ws := tl.Search
				if lv := liveFrom(ctx); lv.Search != nil {
					ws = lv.Search
				}
				if ws == nil {
					return "", withReason("not_configured", userError("web search is not configured"))
				}
				if a.Kind == "news" {
					hits, err = ws.SearchNews(ctx, a.Query, n)
				} else {
					hits, err = ws.Search(ctx, a.Query, n)
				}
				searchHealth.record(ws, err, tl.Now())
				if err != nil {
					return "", err
				}
				t.addURLs(hits)
				return formatHits(hits), nil
			},
		})
	}
	out = append(out, toolImpl{
		taints: true,
		def: llm.ToolDef{Name: "fetch_url", Description: "Đọc nội dung văn bản của một trang web http(s) công khai (có thể ghi thiếu https://, ví dụ github.com/trending).",
			Parameters: schema(`{"type":"object","properties":{"url":{"type":"string"}},"required":["url"]}`)},
		run: func(ctx context.Context, t *turn, raw json.RawMessage) (string, error) {
			var a struct{ URL string }
			if err := json.Unmarshal(raw, &a); err != nil || a.URL == "" {
				return "", withReason("invalid_args", userError("invalid arguments: url required"))
			}
			target, err := t.fetchTarget(preFrom(ctx), a.URL, liveFrom(ctx).fetchAllowOr(s.fetchAllow))
			if err != nil {
				return "", err
			}
			fetch := tl.Fetch
			if fetch == nil {
				fetch = fetchPageTextSafe
			}
			return fetch(ctx, target)
		},
	})
	if tl.Due != nil {
		out = append(out, toolImpl{
			sensitive: true,
			def: llm.ToolDef{Name: "due_dates", Description: "Công việc sắp đến hạn trong N ngày tới.",
				Parameters: schema(`{"type":"object","properties":{"days":{"type":"integer","minimum":0,"maximum":30}}}`)},
			run: func(ctx context.Context, _ *turn, raw json.RawMessage) (string, error) {
				var a struct{ Days *int }
				_ = json.Unmarshal(raw, &a)
				days := 7
				if a.Days != nil {
					days = clamp(*a.Days, 0, 30, 0)
				}
				items, err := tl.Due.Upcoming(ctx, days)
				if err != nil {
					return "", err
				}
				if len(items) == 0 {
					return "Không có việc nào sắp đến hạn.", nil
				}
				var b strings.Builder
				for _, it := range items {
					fmt.Fprintf(&b, "- %s (hạn %s, %s)\n", it.Title, it.Due, it.Bucket)
				}
				return b.String(), nil
			},
		})
	}
	if tl.Reminders != nil {
		out = append(out, s.reminderTools()...)
	}
	return out
}

func (s *Service) reminderTools() []toolImpl {
	tl := s.tools
	return []toolImpl{
		{
			sensitive: true,
			def: llm.ToolDef{Name: "reminder_create", Description: "Tạo nhắc nhở cho chat này. Chỉ dùng khi người dùng yêu cầu rõ ràng trong tin nhắn của họ.",
				Parameters: schema(`{"type":"object","properties":{"task":{"type":"string"},"when":{"type":"string","description":"RFC3339 với múi giờ, ví dụ 2026-10-21T19:00:00+07:00"}},"required":["task","when"]}`)},
			run: func(ctx context.Context, t *turn, raw json.RawMessage) (string, error) {
				var a struct{ Task, When string }
				if err := json.Unmarshal(raw, &a); err != nil {
					return "", userError("invalid arguments")
				}
				if t.mutationCount() >= 1 {
					return "", userError("chỉ được tạo hoặc huỷ một nhắc nhở cho mỗi tin nhắn")
				}
				if !reminderIntent(t.userText) {
					return "", userError("the user did not ask for a reminder; ask them to confirm (e.g. 'nhắc tôi ...') before creating one")
				}
				at, err := time.Parse(time.RFC3339, a.When)
				if err != nil {
					return "", userError("when must be RFC3339 with offset, e.g. 2026-10-21T19:00:00+07:00")
				}
				// The write must not be lost or duplicated by a tool timeout.
				wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
				defer cancel()
				id, err := tl.Reminders.Create(wctx, tl.Platform, t.chatID, t.userID, t.userName, a.Task, at)
				if err != nil {
					return "", userError(reminderErrMsg(err))
				}
				t.addMutation()
				return fmt.Sprintf("Đã tạo nhắc nhở #%d lúc %s", id, at.In(ictZone).Format("02/01/2006 15:04")), nil
			},
		},
		{
			sensitive: true,
			def: llm.ToolDef{Name: "reminder_list", Description: "Liệt kê nhắc nhở đang chờ của chat này.",
				Parameters: schema(`{"type":"object","properties":{}}`)},
			run: func(ctx context.Context, t *turn, _ json.RawMessage) (string, error) {
				rs, err := tl.Reminders.List(ctx, tl.Platform, t.chatID)
				if err != nil {
					return "", err
				}
				if len(rs) == 0 {
					return "Không có nhắc nhở nào đang chờ.", nil
				}
				var b strings.Builder
				for _, r := range rs {
					fmt.Fprintf(&b, "- #%d %s (%s)\n", r.ID, r.Task, r.FireAt.In(ictZone).Format("02/01 15:04"))
				}
				return b.String(), nil
			},
		},
		{
			sensitive: true,
			def: llm.ToolDef{Name: "reminder_cancel", Description: "Huỷ một nhắc nhở của chat này theo id.",
				Parameters: schema(`{"type":"object","properties":{"id":{"type":"integer"}},"required":["id"]}`)},
			run: func(ctx context.Context, t *turn, raw json.RawMessage) (string, error) {
				var a struct{ ID int64 }
				if err := json.Unmarshal(raw, &a); err != nil || a.ID <= 0 {
					return "", userError("invalid arguments: id required")
				}
				if t.mutationCount() >= 1 {
					return "", userError("chỉ được tạo hoặc huỷ một nhắc nhở cho mỗi tin nhắn")
				}
				if !cancelIntentRe.MatchString(t.userText) {
					return "", userError("the user did not ask to cancel a reminder; ask them to confirm first")
				}
				wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
				defer cancel()
				if err := tl.Reminders.Cancel(wctx, tl.Platform, t.chatID, a.ID); err != nil {
					return "", userError("không huỷ được nhắc nhở này (kiểm tra lại số thứ tự)")
				}
				t.addMutation()
				return fmt.Sprintf("Đã huỷ nhắc nhở #%d", a.ID), nil
			},
		},
	}
}

var reminderIntentRe = regexp.MustCompile(`(?i)(nhắc|remind|hẹn giờ|báo thức|đặt lịch)`)

var cancelIntentRe = regexp.MustCompile(`(?i)(huỷ|hủy|xoá|xóa|bỏ|cancel|dừng nhắc)`)

// reminderErrMsg maps store/validation errors to fixed Vietnamese messages;
// raw error text never reaches the model or the user.
func reminderErrMsg(err error) string {
	e := err.Error()
	switch {
	case errors.Is(err, reminder.ErrTooMany):
		return "chat này đã có quá nhiều nhắc nhở đang chờ"
	case strings.Contains(e, "future"):
		return "thời gian nhắc phải ở tương lai"
	case strings.Contains(e, "1 year"):
		return "chỉ đặt nhắc nhở trong vòng 1 năm tới"
	case strings.Contains(e, "empty task"):
		return "nội dung nhắc nhở đang trống"
	case strings.Contains(e, "longer than"):
		return "nội dung nhắc nhở quá dài"
	}
	return "không tạo được nhắc nhở lúc này, thử lại sau"
}

// reminderIntent is the guard for reminder_create: the user's own text must
// carry reminder intent, whatever the model says.
func reminderIntent(text string) bool {
	return reminder.MatchesReminderKeyword(text) || reminderIntentRe.MatchString(text)
}

func clamp(v, lo, hi, def int) int {
	if v == 0 {
		return def
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func formatHits(hits []WebHit) string {
	if len(hits) == 0 {
		return "Không có kết quả."
	}
	var b strings.Builder
	for _, h := range hits {
		fmt.Fprintf(&b, "- %s\n  %s\n", h.Title, h.URL)
		if h.Snippet != "" {
			fmt.Fprintf(&b, "  %s\n", h.Snippet)
		}
		if h.Source != "" || h.Date != "" {
			fmt.Fprintf(&b, "  (%s %s)\n", h.Source, h.Date)
		}
	}
	return b.String()
}

var weekdaysVN = [...]string{"Chủ nhật", "Thứ hai", "Thứ ba", "Thứ tư", "Thứ năm", "Thứ sáu", "Thứ bảy"}

func lunarText(d time.Time, day, month, year, leap int) string {
	m := fmt.Sprintf("tháng %d", month)
	if leap == 1 {
		m += " (nhuận)"
	}
	return fmt.Sprintf("%s, %s dương lịch = ngày %d %s năm %d âm lịch", weekdaysVN[d.Weekday()], d.Format("02/01/2006"), day, m, year)
}
