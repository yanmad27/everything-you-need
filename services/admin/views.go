package admin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
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
	"pct":  func(f float64) string { return strconv.FormatFloat(f*100, 'f', 0, 64) + "%" },
	"cost": func(f float64) string { return strconv.FormatFloat(f, 'f', 4, 64) },
	"enc":  url.QueryEscape,
}

type page struct {
	Title, Nav, Nonce, CSRF string
	CSS                     template.CSS
	Q                       url.Values // current filters
	Data                    any
}

func (h *Handler) render(w http.ResponseWriter, r *http.Request, status int, name string, p page) {
	p.Nonce, _ = r.Context().Value(nonceKey{}).(string)
	p.CSS = h.css
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
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
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
	return convo.StatsFilter{Platform: clip(q.Get("platform"), 20), ChatID: clip(q.Get("chat"), 100),
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
		base.Data = map[string]any{"Items": items, "Total": total, "Pager": pager(q, "/admin/chats", pg, chatsPerPage, total)}
		h.render(w, r, http.StatusOK, "chats.html", base)
	case "/admin/chat", "/admin/api/timeline":
		platform, chatID := clip(q.Get("platform"), 20), clip(q.Get("id"), 100)
		if platform == "" || chatID == "" {
			http.Error(w, "missing platform or id", http.StatusBadRequest)
			return
		}
		_, total, err := h.data.Timeline(ctx, platform, chatID, convo.Page{Limit: 1})
		if err != nil {
			fail(err)
			return
		}
		last := (total + timelinePage - 1) / timelinePage
		if last < 1 {
			last = 1
		}
		pg := last // newest page first
		if q.Get("page") != "" {
			pg = atoi(q.Get("page"), last)
		}
		items, total, err := h.data.Timeline(ctx, platform, chatID, convo.Page{Limit: timelinePage, Offset: (pg - 1) * timelinePage})
		if err != nil {
			fail(err)
			return
		}
		if api {
			writeJSON(w, map[string]any{"total": total, "page": pg, "items": items})
			return
		}
		base.Title, base.Nav = "Chi tiết hội thoại", "chats"
		base.Data = map[string]any{"Items": items, "Total": total, "Platform": platform, "ChatID": chatID,
			"Pager": pager(q, "/admin/chat", pg, timelinePage, total)}
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
		if needle := strings.ToLower(strings.TrimSpace(clip(q.Get("q"), 100))); needle != "" {
			kept := st[:0]
			for _, x := range st {
				if strings.Contains(strings.ToLower(x.Name), needle) || strings.Contains(strings.ToLower(x.ID), needle) {
					kept = append(kept, x)
				}
			}
			st = kept
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
	default:
		if api {
			writeJSONError(w, http.StatusNotFound, "not found")
			return
		}
		http.NotFound(w, r)
	}
}

type pagerData struct {
	Page, Pages      int
	Prev, Next       string
	HasPrev, HasNext bool
}

func pager(q url.Values, path string, pg, size, total int) pagerData {
	pages := (total + size - 1) / size
	if pages < 1 {
		pages = 1
	}
	link := func(n int) string {
		v := url.Values{}
		for k, vs := range q {
			v[k] = vs
		}
		v.Set("page", fmt.Sprint(n))
		return path + "?" + v.Encode()
	}
	p := pagerData{Page: pg, Pages: pages, HasPrev: pg > 1, HasNext: pg < pages}
	if p.HasPrev {
		p.Prev = link(pg - 1)
	}
	if p.HasNext {
		p.Next = link(pg + 1)
	}
	return p
}
