package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"everything-you-need/m/services/convo"
)

var ict = time.FixedZone("ICT", 7*3600)

const (
	chatsPerPage = 25
	timelinePage = 100
)

var funcs = template.FuncMap{
	"ftime": func(t time.Time) string {
		if t.IsZero() {
			return "—"
		}
		return t.In(ict).Format("02/01/2006 15:04")
	},
	"kb": func(b int) string {
		k := (b + 1023) / 1024
		if k < 1 {
			k = 1
		}
		return strconv.Itoa(k) + " KB"
	},
	"pct":     func(f float64) string { return strconv.FormatFloat(f*100, 'f', 0, 64) + "%" },
	"cost":    func(f float64) string { return strconv.FormatFloat(f, 'f', 4, 64) },
	"money":   money,
	"estcost": estCost,
	"num":     num,
}

// money renders an optional cost: nil → "chưa cấu hình giá" (price not
// configured); partial costs are marked as a lower bound with "≥ … (một phần)".
// Decimals scale with magnitude: ≥0.01 → 2, below → 6 significant digits,
// below 0.000001 → "<0.000001".
func money(c *float64, partial bool) string {
	if c == nil {
		return "chưa cấu hình giá"
	}
	s := fmtMoney(*c)
	if partial {
		return "≥ " + s + " (một phần)"
	}
	return s
}

func fmtMoney(v float64) string {
	switch {
	case v <= 0:
		return "0"
	case v < 0.000001:
		return "<0.000001"
	case v >= 0.01:
		return strconv.FormatFloat(v, 'f', 2, 64)
	}
	dec := 5 - int(math.Floor(math.Log10(v)))
	s := strconv.FormatFloat(v, 'f', dec, 64)
	return strings.TrimRight(strings.TrimRight(s, "0"), ".")
}

// estCost renders convo.Stat's EstCost: with unpriced tokens and nothing priced
// it is "chưa cấu hình giá"; a priced zero is "0".
func estCost(v float64, partial bool) string {
	if partial && v == 0 {
		return money(nil, true)
	}
	return money(&v, partial)
}

// num groups digits by thousands: 1234567 → 1,234,567.
func num(n int) string {
	s := strconv.Itoa(n)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	if neg {
		s = "-" + s
	}
	return s
}

// page is the data every template receives. Active is the nav key (chats |
// users | chat-stats | tools | usage | password; "" on login) and Username the
// signed-in admin's name ("" on login); both are set for every page.
type page struct {
	Title, Nav, Nonce, CSRF string
	Active, Username        string
	CSS                     template.CSS
	Q                       url.Values // current filters
	Data                    any
}

var navActive = map[string]string{"chats": "chats", "users": "users", "chatstats": "chat-stats", "tools": "tools", "usage": "usage", "password": "password", "bots": "bots"}

func (h *Handler) render(w http.ResponseWriter, r *http.Request, status int, name string, p page) {
	p.Nonce, _ = r.Context().Value(nonceKey{}).(string)
	p.CSS = h.css
	if p.Active == "" {
		p.Active = navActive[p.Nav]
	}
	if p.CSRF != "" { // signed in
		p.Username = h.username
	}
	var buf bytes.Buffer
	if err := h.tpl.ExecuteTemplate(&buf, name, p); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

func (h *Handler) renderLogin(w http.ResponseWriter, r *http.Request, status int, failed bool) {
	h.render(w, r, status, "login.html", page{Title: "Đăng nhập", CSRF: h.loginCSRF(), Data: failed})
}

func writeJSON(w http.ResponseWriter, v any) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(v); err != nil {
		log.Printf("admin: json encode failed: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "encode failed")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(buf.Bytes())
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// parseDay reads YYYY-MM-DD in Vietnam time; plusDay makes the bound exclusive.
func parseDay(s string, plusDay bool) time.Time {
	t, err := time.ParseInLocation("2006-01-02", s, ict)
	if err != nil {
		return time.Time{}
	}
	if plusDay {
		t = t.AddDate(0, 0, 1)
	}
	return t
}

func atoi(s string, def int) int {
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return def
	}
	return n
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func statsFilter(q url.Values) convo.StatsFilter {
	return convo.StatsFilter{Platform: clip(q.Get("platform"), 20), ChatID: clip(q.Get("chat"), 100), Q: clip(q.Get("q"), 100),
		From: parseDay(q.Get("from"), false), To: parseDay(q.Get("to"), true)}
}

func (h *Handler) route(w http.ResponseWriter, r *http.Request, path string, s *session) {
	q := r.URL.Query()
	ctx := r.Context()
	api := strings.HasPrefix(path, "/admin/api/")
	fail := func(err error) {
		if api {
			writeJSONError(w, http.StatusInternalServerError, "query failed")
			return
		}
		http.Error(w, "query failed", http.StatusInternalServerError)
	}
	base := page{CSRF: s.csrf, Q: q}

	switch path {
	case "/admin", "/admin/chats", "/admin/api/chats":
		f := convo.ChatFilter{Platform: clip(q.Get("platform"), 20), ChatType: clip(q.Get("type"), 10), Q: clip(q.Get("q"), 100),
			From: parseDay(q.Get("from"), false), To: parseDay(q.Get("to"), true)}
		pg := atoi(q.Get("page"), 1)
		items, total, err := h.data.ListChats(ctx, f, convo.Page{Limit: chatsPerPage, Offset: (pg - 1) * chatsPerPage})
		if err != nil {
			fail(err)
			return
		}
		if api {
			writeJSON(w, map[string]any{"total": total, "page": pg, "items": items})
			return
		}
		base.Title, base.Nav = "Hội thoại", "chats"
		base.Data = map[string]any{"Items": items, "Total": total, "Pager": pager(q, "/admin/chats", pg, chatsPerPage, total, "← Trước", "Sau →")}
		h.render(w, r, http.StatusOK, "chats.html", base)
	case "/admin/chat", "/admin/api/timeline":
		platform, chatID := clip(q.Get("platform"), 20), clip(q.Get("id"), 100)
		if platform == "" || chatID == "" {
			http.Error(w, "missing platform or id", http.StatusBadRequest)
			return
		}
		// Page 1 is the newest window; "older" pages follow. One query per view.
		pg := atoi(q.Get("page"), 1)
		items, total, err := h.data.Timeline(ctx, platform, chatID, convo.Page{Limit: timelinePage, Offset: (pg - 1) * timelinePage, Desc: true})
		if err != nil {
			fail(err)
			return
		}
		for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 { // show chronologically within the page
			items[i], items[j] = items[j], items[i]
		}
		if api {
			writeJSON(w, map[string]any{"total": total, "page": pg, "items": items})
			return
		}
		base.Title, base.Nav = "Chi tiết hội thoại", "chats"
		base.Data = map[string]any{"Items": items, "Total": total, "Platform": platform, "ChatID": chatID,
			"Pager": pager(q, "/admin/chat", pg, timelinePage, total, "← Mới hơn", "Cũ hơn →")}
		h.render(w, r, http.StatusOK, "chat.html", base)
	case "/admin/users", "/admin/api/users", "/admin/chat-stats", "/admin/api/chat-stats":
		byChat := strings.Contains(path, "chat-stats")
		var st []convo.Stat
		var err error
		if byChat {
			st, err = h.data.ChatStats(ctx, statsFilter(q))
		} else {
			st, err = h.data.UserStats(ctx, statsFilter(q))
		}
		if err != nil {
			fail(err)
			return
		}
		if api {
			writeJSON(w, map[string]any{"items": st})
			return
		}
		base.Title, base.Nav = "Thống kê người dùng", "users"
		if byChat {
			base.Title, base.Nav = "Thống kê theo chat", "chatstats"
		}
		base.Data = map[string]any{"Items": st, "ByChat": byChat}
		h.render(w, r, http.StatusOK, "stats.html", base)
	case "/admin/tools", "/admin/api/tools":
		rep, err := h.data.ToolStats(ctx, statsFilter(q))
		if err != nil {
			fail(err)
			return
		}
		if api {
			writeJSON(w, rep)
			return
		}
		base.Title, base.Nav = "Công cụ", "tools"
		base.Data = rep
		h.render(w, r, http.StatusOK, "tools.html", base)
	case "/admin/usage", "/admin/api/usage":
		if h.usage == nil {
			if api {
				writeJSONError(w, http.StatusNotFound, "not found")
				return
			}
			http.NotFound(w, r)
			return
		}
		v, err := h.usageView(ctx, q)
		if err != nil {
			fail(err)
			return
		}
		if api {
			writeJSON(w, v)
			return
		}
		base.Title, base.Nav = "Usage & cost", "usage"
		base.Data = v
		h.render(w, r, http.StatusOK, "usage.html", base)
	default:
		if api {
			writeJSONError(w, http.StatusNotFound, "not found")
			return
		}
		http.NotFound(w, r)
	}
}

type pagerData struct {
	Page, Pages          int
	Prev, Next           string
	HasPrev, HasNext     bool
	PrevLabel, NextLabel string
}

func pager(q url.Values, path string, pg, size, total int, prevLabel, nextLabel string) pagerData {
	return pagerKey(q, path, "page", pg, size, total, prevLabel, nextLabel)
}

func pagerKey(q url.Values, path, key string, pg, size, total int, prevLabel, nextLabel string) pagerData {
	pages := (total + size - 1) / size
	if pages < 1 {
		pages = 1
	}
	link := func(n int) string {
		v := url.Values{}
		for k, vs := range q {
			v[k] = vs
		}
		v.Set(key, fmt.Sprint(n))
		return path + "?" + v.Encode()
	}
	p := pagerData{PrevLabel: prevLabel, NextLabel: nextLabel, Page: pg, Pages: pages, HasPrev: pg > 1, HasNext: pg < pages}
	if p.HasPrev {
		p.Prev = link(pg - 1)
	}
	if p.HasNext {
		p.Next = link(pg + 1)
	}
	return p
}

const usagePerPage = 25

// UsageView is the Data of usage.html and the body of GET /admin/api/usage.
// Costs are *float64: nil means "price not configured" (never 0); see
// convo.UsageSum for PartialPricing.
type UsageView struct {
	Currency string        `json:"currency"`
	Platform string        `json:"platform"` // filter echo ("" = all)
	From     string        `json:"from"`     // YYYY-MM-DD echo ("" = open)
	To       string        `json:"to"`       // inclusive end day echo
	Prices   []convo.Price `json:"prices"`

	Totals convo.UsageSum     `json:"totals"`
	Days   []convo.UsageDay   `json:"days"` // ascending; zero-filled when From and To are set
	Models []convo.UsageModel `json:"models"`

	Chats      []convo.UsageChat `json:"chats"` // this page, most tokens first
	ChatsTotal int               `json:"chats_total"`
	ChatsPage  int               `json:"chats_page"`
	ChatsPager pagerData         `json:"-"`
	Users      []convo.UsageUser `json:"users"`
	UsersTotal int               `json:"users_total"`
	UsersPage  int               `json:"users_page"`
	UsersPager pagerData         `json:"-"`

	// Chart helpers: the largest daily value, to scale bars.
	MaxDayTokens int     `json:"max_day_tokens"`
	MaxDayCost   float64 `json:"max_day_cost"`
}

func (h *Handler) usageView(ctx context.Context, q url.Values) (UsageView, error) {
	f := convo.UsageFilter{Platform: clip(q.Get("platform"), 20), From: parseDay(q.Get("from"), false), To: parseDay(q.Get("to"), true)}
	v := UsageView{Currency: h.usage.Currency(), Platform: f.Platform, Prices: h.usage.Prices(),
		ChatsPage: atoi(q.Get("chat_page"), 1), UsersPage: atoi(q.Get("user_page"), 1)}
	if !f.From.IsZero() {
		v.From = q.Get("from")
	}
	if !f.To.IsZero() {
		v.To = q.Get("to")
	}
	var err error
	if v.Totals, err = h.usage.UsageTotals(ctx, f); err != nil {
		return v, err
	}
	if v.Days, err = h.usage.UsageByDay(ctx, f); err != nil {
		return v, err
	}
	if v.Models, err = h.usage.UsageByModel(ctx, f); err != nil {
		return v, err
	}
	if v.Chats, v.ChatsTotal, err = h.usage.UsageByChat(ctx, f, convo.Page{Limit: usagePerPage, Offset: (v.ChatsPage - 1) * usagePerPage}); err != nil {
		return v, err
	}
	if last := lastPage(v.ChatsTotal, usagePerPage); v.ChatsPage > last { // clamp out-of-range pages
		v.ChatsPage = last
		if v.Chats, _, err = h.usage.UsageByChat(ctx, f, convo.Page{Limit: usagePerPage, Offset: (last - 1) * usagePerPage}); err != nil {
			return v, err
		}
	}
	if v.Users, v.UsersTotal, err = h.usage.UsageByUser(ctx, f, convo.Page{Limit: usagePerPage, Offset: (v.UsersPage - 1) * usagePerPage}); err != nil {
		return v, err
	}
	if last := lastPage(v.UsersTotal, usagePerPage); v.UsersPage > last {
		v.UsersPage = last
		if v.Users, _, err = h.usage.UsageByUser(ctx, f, convo.Page{Limit: usagePerPage, Offset: (last - 1) * usagePerPage}); err != nil {
			return v, err
		}
	}
	for _, d := range v.Days {
		if d.TotalTokens > v.MaxDayTokens {
			v.MaxDayTokens = d.TotalTokens
		}
		if d.Cost != nil && *d.Cost > v.MaxDayCost {
			v.MaxDayCost = *d.Cost
		}
	}
	v.ChatsPager = pagerKey(q, "/admin/usage", "chat_page", v.ChatsPage, usagePerPage, v.ChatsTotal, "← Trước", "Sau →")
	v.UsersPager = pagerKey(q, "/admin/usage", "user_page", v.UsersPage, usagePerPage, v.UsersTotal, "← Trước", "Sau →")
	return v, nil
}

func lastPage(total, size int) int {
	if n := (total + size - 1) / size; n > 1 {
		return n
	}
	return 1
}
