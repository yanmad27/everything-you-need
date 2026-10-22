package convo

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// Read-only query API for the admin UI. All SQL is parameterised; the only
// interpolated fragments are fixed column names chosen in this file.

const (
	defPageLimit = 50
	maxPageLimit = 200
	topToolsN    = 5
	recentFailN  = 20
)

// Page selects a window of results. Limit defaults to 50 (max 200).
type Page struct {
	Limit, Offset int
	Desc          bool // newest first (Timeline only; ListChats is always newest activity first)
}

func (p Page) norm() (limit, offset int) {
	limit, offset = p.Limit, p.Offset
	if limit <= 0 {
		limit = defPageLimit
	}
	if limit > maxPageLimit {
		limit = maxPageLimit
	}
	if offset < 0 {
		offset = 0
	}
	return
}

// ChatFilter narrows ListChats. Zero values mean "any". From/To bound message
// activity (From inclusive, To exclusive); counts are then within that range.
type ChatFilter struct {
	Platform, ChatType string
	Q                  string // substring of chat id, title, a participant's display name or any message text
	From, To           time.Time
}

type ChatSummary struct {
	Platform, ChatID, ChatType, Title string
	DisplayName                       string // title, else (private chats) the last inbound sender's name
	FirstSeen, LastSeen               time.Time
	Messages, ToolCalls               int
	FirstActivity, LastActivity       time.Time // first/last message in range (zero when none)
}

func ms(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func fromMS(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.UnixMilli(v)
}

func likeEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(s) + "%"
}

// timeClause returns " AND col >= ? AND col < ?" fragments for the set bounds.
func timeClause(col string, from, to time.Time, args *[]any) string {
	var sb strings.Builder
	if !from.IsZero() {
		sb.WriteString(" AND " + col + " >= ?")
		*args = append(*args, ms(from))
	}
	if !to.IsZero() {
		sb.WriteString(" AND " + col + " < ?")
		*args = append(*args, ms(to))
	}
	return sb.String()
}

// chatNameExpr is the display name of chat alias c: its title, else (non-group
// chats) the last inbound sender's name.
const chatNameExpr = `CASE WHEN c.title != '' THEN c.title WHEN c.chat_type = 'GROUP' THEN '' ELSE COALESCE((SELECT u.display_name FROM messages x
			JOIN users u ON u.platform = x.platform AND u.user_id = x.user_id
			WHERE x.platform = c.platform AND x.chat_id = c.chat_id AND x.direction = 'in' AND u.display_name != ''
			ORDER BY x.created_at DESC, x.id DESC LIMIT 1), '') END`

// ListChats returns chats (newest activity first) with message/tool counts and
// the total number of matching chats.
func (s *Store) ListChats(ctx context.Context, f ChatFilter, p Page) ([]ChatSummary, int, error) {
	var args []any
	mw := "1=1" + timeClause("created_at", f.From, f.To, &args)
	tw := "1=1" + timeClause("created_at", f.From, f.To, &args)
	q := `FROM chats c
		LEFT JOIN (SELECT platform, chat_id, COUNT(*) n, MIN(created_at) f, MAX(created_at) l FROM messages WHERE ` + mw + ` GROUP BY platform, chat_id) m
			ON m.platform = c.platform AND m.chat_id = c.chat_id
		LEFT JOIN (SELECT platform, chat_id, COUNT(*) n FROM tool_calls WHERE ` + tw + ` GROUP BY platform, chat_id) t
			ON t.platform = c.platform AND t.chat_id = c.chat_id
		WHERE 1=1`
	if f.Platform != "" {
		q += ` AND c.platform = ?`
		args = append(args, f.Platform)
	}
	if f.ChatType != "" {
		q += ` AND c.chat_type = ?`
		args = append(args, strings.ToUpper(f.ChatType))
	}
	if !f.From.IsZero() || !f.To.IsZero() {
		q += ` AND COALESCE(m.n, 0) > 0`
	}
	if f.Q != "" {
		like := likeEscape(f.Q)
		q += ` AND (c.chat_id LIKE ? ESCAPE '\' OR c.title LIKE ? ESCAPE '\' OR EXISTS (
			SELECT 1 FROM messages x WHERE x.platform = c.platform AND x.chat_id = c.chat_id AND x.text LIKE ? ESCAPE '\')
			OR EXISTS (SELECT 1 FROM messages y JOIN users u ON u.platform = y.platform AND u.user_id = y.user_id
				WHERE y.platform = c.platform AND y.chat_id = c.chat_id AND u.display_name LIKE ? ESCAPE '\'))`
		args = append(args, like, like, like, like)
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) `+q, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("convo: count chats: %w", err)
	}
	limit, offset := p.norm()
	rows, err := s.db.QueryContext(ctx, `SELECT c.platform, c.chat_id, c.chat_type, c.title, c.first_seen, c.last_seen,
		`+chatNameExpr+`,
		COALESCE(m.n,0), COALESCE(t.n,0), COALESCE(m.f,0), COALESCE(m.l,0) `+q+
		` ORDER BY c.last_seen DESC, c.platform, c.chat_id LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("convo: list chats: %w", err)
	}
	defer rows.Close()
	var out []ChatSummary
	for rows.Next() {
		var c ChatSummary
		var fs, ls, fa, la int64
		if err := rows.Scan(&c.Platform, &c.ChatID, &c.ChatType, &c.Title, &fs, &ls, &c.DisplayName, &c.Messages, &c.ToolCalls, &fa, &la); err != nil {
			return nil, 0, err
		}
		c.FirstSeen, c.LastSeen, c.FirstActivity, c.LastActivity = fromMS(fs), fromMS(ls), fromMS(fa), fromMS(la)
		out = append(out, c)
	}
	return out, total, rows.Err()
}

type Message struct {
	ID                                 int64
	Platform, ChatID, UserID, UserName string
	Direction, Kind, Text, ImageMime   string
	ImageBytes                         int
	CreatedAt                          time.Time
	TurnID, ErrorTag                   string
}

type ToolCall struct {
	ID                                     int64
	TurnID, Platform, ChatID, UserID, Name string
	ArgsSummary, ResultSummary, Outcome    string
	LatencyMS                              int64
	CreatedAt                              time.Time
}

// TimelineItem is one entry of a chat timeline: exactly one of Message/Tool is set.
type TimelineItem struct {
	Type    string // "message" | "tool_call"
	At      time.Time
	TurnID  string
	Message *Message
	Tool    *ToolCall
}

// Timeline merges a chat's messages and tool calls in time order (oldest first,
// or newest first with p.Desc) and returns the total item count.
func (s *Store) Timeline(ctx context.Context, platform, chatID string, p Page) ([]TimelineItem, int, error) {
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM messages WHERE platform=? AND chat_id=?) +
		(SELECT COUNT(*) FROM tool_calls WHERE platform=? AND chat_id=?)`, platform, chatID, platform, chatID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("convo: timeline count: %w", err)
	}
	dir := "ASC"
	if p.Desc {
		dir = "DESC"
	}
	limit, offset := p.norm()
	rows, err := s.db.QueryContext(ctx, `SELECT * FROM (
		SELECT 0 AS src, m.id AS id, m.created_at AS at, m.turn_id AS turn_id,
			m.direction AS a, m.kind AS b, m.text AS c, m.image_mime AS d, m.image_bytes AS n, m.error_tag AS e,
			COALESCE(m.user_id,'') AS uid, COALESCE(u.display_name,'') AS f
		FROM messages m LEFT JOIN users u ON u.platform = m.platform AND u.user_id = m.user_id
		WHERE m.platform = ? AND m.chat_id = ?
		UNION ALL
		SELECT 1, t.id, t.created_at, t.turn_id, t.name, t.outcome, t.args_summary, t.result_summary, t.latency_ms, '',
			t.user_id, ''
		FROM tool_calls t WHERE t.platform = ? AND t.chat_id = ?
	) ORDER BY at `+dir+`, src `+dir+`, id `+dir+` LIMIT ? OFFSET ?`, platform, chatID, platform, chatID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("convo: timeline: %w", err)
	}
	defer rows.Close()
	var out []TimelineItem
	for rows.Next() {
		var src int
		var id, at, n int64
		var turn, a, b, c, d, e, uid, f string
		if err := rows.Scan(&src, &id, &at, &turn, &a, &b, &c, &d, &n, &e, &uid, &f); err != nil {
			return nil, 0, err
		}
		it := TimelineItem{At: fromMS(at), TurnID: turn}
		if src == 0 {
			it.Type = "message"
			it.Message = &Message{ID: id, Platform: platform, ChatID: chatID, UserID: uid, UserName: f, Direction: a, Kind: b,
				Text: c, ImageMime: d, ImageBytes: int(n), CreatedAt: it.At, TurnID: turn, ErrorTag: e}
		} else {
			it.Type = "tool_call"
			it.Tool = &ToolCall{ID: id, TurnID: turn, Platform: platform, ChatID: chatID, UserID: uid, Name: a, Outcome: b,
				ArgsSummary: c, ResultSummary: d, LatencyMS: n, CreatedAt: it.At}
		}
		out = append(out, it)
	}
	return out, total, rows.Err()
}

// StatsFilter narrows UserStats/ChatStats/ToolStats. From inclusive, To exclusive.
type StatsFilter struct {
	Platform string
	ChatID   string // restrict to one chat (UserStats/ToolStats)
	// Q narrows UserStats to users whose name or id contains it, and ChatStats
	// to chats whose id, title or participant name contains it (case-insensitive).
	Q        string
	From, To time.Time
}

type ToolCount struct {
	Name  string
	Count int
}

// Stat is the per-user (UserStats) or per-chat (ChatStats) insight row.
type Stat struct {
	Platform string
	ID       string // user id or chat id
	Name     string // display name / chat title when known
	Messages int    // inbound messages
	// ActiveDays counts distinct Vietnam-time (UTC+7) days with inbound messages.
	ActiveDays       int
	LastSeen         time.Time
	RemindersCreated int // reminder_create calls with outcome ok
	ImagesSent       int // inbound image messages
	TopTools         []ToolCount
	// Errors = tool calls with outcome error|timeout + failed bot replies (error
	// tag set, notices excluded). Failed replies are attributed to the user of
	// the turn, so user and chat views agree.
	Errors           int
	Notices          int // rate-limit / image-too-large replies sent (not errors)
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	// EstCost is the cost of the priced part of the usage (per-model prices);
	// 0 when nothing is priced. PartialPricing means some tokens belong to a
	// model with no configured price, so EstCost is a lower bound.
	EstCost        float64
	PartialPricing bool
}

// UserStats returns per-user insights (ordered by messages, then last seen).
func (s *Store) UserStats(ctx context.Context, f StatsFilter) ([]Stat, error) {
	return s.stats(ctx, f, "user_id")
}

// ChatStats returns per-chat insights.
func (s *Store) ChatStats(ctx context.Context, f StatsFilter) ([]Stat, error) {
	return s.stats(ctx, f, "chat_id")
}

// noticeTags are fixed system notices, counted separately from errors.
const noticeTags = `('rate_limited','image_too_large')`

// stats groups by col, which is only ever the literal "user_id" or "chat_id".
func (s *Store) stats(ctx context.Context, f StatsFilter, col string) ([]Stat, error) {
	where := func(prefix string) (string, []any) {
		var a []any
		w := "1=1"
		if f.Platform != "" {
			w += " AND " + prefix + "platform = ?"
			a = append(a, f.Platform)
		}
		if f.ChatID != "" && col == "user_id" {
			w += " AND " + prefix + "chat_id = ?"
			a = append(a, f.ChatID)
		}
		w += timeClause(prefix+"created_at", f.From, f.To, &a)
		return w, a
	}
	type key struct{ platform, id string }
	m := map[key]*Stat{}
	get := func(platform, id string) *Stat {
		k := key{platform, id}
		st := m[k]
		if st == nil {
			st = &Stat{Platform: platform, ID: id}
			m[k] = st
		}
		return st
	}
	// messages. Bot replies carry no user, so for per-user stats a reply takes
	// the user of the inbound message in the same turn.
	grp := "chat_id"
	inner := "*"
	if col == "user_id" {
		grp = "eff_user"
		inner = `*, COALESCE(user_id, (SELECT i.user_id FROM messages i WHERE i.turn_id = messages.turn_id AND messages.turn_id != ''
			AND i.platform = messages.platform AND i.direction = 'in' LIMIT 1)) AS eff_user`
	}
	w, a := where("")
	rows, err := s.db.QueryContext(ctx, `SELECT platform, `+grp+`,
		SUM(direction='in'), SUM(direction='in' AND kind='image'),
		COUNT(DISTINCT CASE WHEN direction='in' THEN date(created_at/1000,'unixepoch','+7 hours') END),
		MAX(CASE WHEN direction='in' THEN created_at ELSE 0 END),
		SUM(direction='out' AND error_tag != '' AND error_tag NOT IN `+noticeTags+`),
		SUM(direction='out' AND error_tag IN `+noticeTags+`)
		FROM (SELECT `+inner+` FROM messages WHERE `+w+`) WHERE `+grp+` IS NOT NULL AND `+grp+` != ''
		GROUP BY platform, `+grp, a...)
	if err != nil {
		return nil, fmt.Errorf("convo: stats messages: %w", err)
	}
	for rows.Next() {
		var pf, id string
		var in, img, days, last, errs, notes int64
		if err := rows.Scan(&pf, &id, &in, &img, &days, &last, &errs, &notes); err != nil {
			rows.Close()
			return nil, err
		}
		st := get(pf, id)
		st.Messages, st.ImagesSent, st.ActiveDays, st.LastSeen, st.Errors, st.Notices = int(in), int(img), int(days), fromMS(last), int(errs), int(notes)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// tool calls: reminders, errors, per-tool counts.
	w, a = where("")
	rows, err = s.db.QueryContext(ctx, `SELECT platform, `+col+`, name, COUNT(*),
		SUM(name='reminder_create' AND outcome='ok'), SUM(outcome IN ('error','timeout'))
		FROM tool_calls WHERE `+w+` AND `+col+` != '' GROUP BY platform, `+col+`, name`, a...)
	if err != nil {
		return nil, fmt.Errorf("convo: stats tools: %w", err)
	}
	tools := map[key][]ToolCount{}
	for rows.Next() {
		var pf, id, name string
		var n, rem, errs int64
		if err := rows.Scan(&pf, &id, &name, &n, &rem, &errs); err != nil {
			rows.Close()
			return nil, err
		}
		st := get(pf, id)
		st.RemindersCreated += int(rem)
		st.Errors += int(errs)
		tools[key{pf, id}] = append(tools[key{pf, id}], ToolCount{name, int(n)})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// usage tokens (priced per model).
	w, a = where("")
	rows, err = s.db.QueryContext(ctx, `SELECT platform, `+col+`, model, SUM(prompt_tokens), SUM(completion_tokens), SUM(total_tokens)
		FROM usage WHERE `+w+` AND `+col+` != '' GROUP BY platform, `+col+`, model`, a...)
	if err != nil {
		return nil, fmt.Errorf("convo: stats usage: %w", err)
	}
	for rows.Next() {
		var pf, id, model string
		var p, c, t int64
		if err := rows.Scan(&pf, &id, &model, &p, &c, &t); err != nil {
			rows.Close()
			return nil, err
		}
		st := get(pf, id)
		st.PromptTokens += int(p)
		st.CompletionTokens += int(c)
		st.TotalTokens += int(t)
		if pr, ok := s.priceFor(model); ok {
			st.EstCost += float64(p)/1e6*pr.PromptPer1M + float64(c)/1e6*pr.CompletionPer1M
		} else if p+c+t > 0 {
			st.PartialPricing = true
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// names (chats use the same fallback as ListChats).
	nameQ := `SELECT platform, user_id, display_name FROM users`
	if col == "chat_id" {
		nameQ = `SELECT c.platform, c.chat_id, ` + chatNameExpr + ` FROM chats c`
	}
	rows, err = s.db.QueryContext(ctx, nameQ)
	if err != nil {
		return nil, fmt.Errorf("convo: stats names: %w", err)
	}
	for rows.Next() {
		var pf, id, nm string
		if err := rows.Scan(&pf, &id, &nm); err != nil {
			rows.Close()
			return nil, err
		}
		if st := m[key{pf, id}]; st != nil {
			st.Name = nm
		}
	}
	rows.Close()
	// Q filter.
	var qChats map[key]bool
	needle := strings.ToLower(strings.TrimSpace(f.Q))
	if needle != "" && col == "chat_id" {
		like := likeEscape(needle)
		rows, err = s.db.QueryContext(ctx, `SELECT c.platform, c.chat_id FROM chats c WHERE c.chat_id LIKE ? ESCAPE '\' OR c.title LIKE ? ESCAPE '\'
			OR EXISTS (SELECT 1 FROM messages y JOIN users u ON u.platform = y.platform AND u.user_id = y.user_id
				WHERE y.platform = c.platform AND y.chat_id = c.chat_id AND u.display_name LIKE ? ESCAPE '\')`, like, like, like)
		if err != nil {
			return nil, fmt.Errorf("convo: stats filter: %w", err)
		}
		qChats = map[key]bool{}
		for rows.Next() {
			var pf, id string
			if err := rows.Scan(&pf, &id); err != nil {
				rows.Close()
				return nil, err
			}
			qChats[key{pf, id}] = true
		}
		rows.Close()
	}
	out := make([]Stat, 0, len(m))
	for k, st := range m {
		if needle != "" {
			if col == "chat_id" && !qChats[k] {
				continue
			}
			if col == "user_id" && !strings.Contains(strings.ToLower(st.Name), needle) && !strings.Contains(strings.ToLower(st.ID), needle) {
				continue
			}
		}
		tc := tools[k]
		sort.Slice(tc, func(i, j int) bool {
			if tc[i].Count != tc[j].Count {
				return tc[i].Count > tc[j].Count
			}
			return tc[i].Name < tc[j].Name
		})
		if len(tc) > topToolsN {
			tc = tc[:topToolsN]
		}
		st.TopTools = tc
		out = append(out, *st)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Messages != out[j].Messages {
			return out[i].Messages > out[j].Messages
		}
		if !out[i].LastSeen.Equal(out[j].LastSeen) {
			return out[i].LastSeen.After(out[j].LastSeen)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

type ToolStat struct {
	Name     string
	Calls    int // all calls
	OK       int
	Errors   int // outcome error
	Timeouts int
	Blocked  int // refused by the exfiltration/fetch guards; not part of SuccessRate
	// SuccessRate = OK/(OK+Errors+Timeouts), 0..1 (0 when there are none).
	SuccessRate float64
	// P50MS/P95MS are nearest-rank latencies over ok calls only.
	P50MS, P95MS int64
}

type ToolFailure struct {
	Name, Outcome, ArgsSummary, ResultSummary string
	ChatID, TurnID                            string
	LatencyMS                                 int64
	At                                        time.Time
}

type ToolReport struct {
	Tools          []ToolStat    // most-called first
	RecentFailures []ToolFailure // last 20 non-ok calls, newest first
}

// ToolStats reports per-tool volume, success rate and latency percentiles
// (nearest-rank, computed over the ordered latencies SQL returns).
func (s *Store) ToolStats(ctx context.Context, f StatsFilter) (ToolReport, error) {
	var a []any
	w := "1=1"
	if f.Platform != "" {
		w += " AND platform = ?"
		a = append(a, f.Platform)
	}
	if f.ChatID != "" {
		w += " AND chat_id = ?"
		a = append(a, f.ChatID)
	}
	w += timeClause("created_at", f.From, f.To, &a)

	rows, err := s.db.QueryContext(ctx, `SELECT name, outcome, latency_ms FROM tool_calls WHERE `+w+` ORDER BY name, latency_ms`, a...)
	if err != nil {
		return ToolReport{}, fmt.Errorf("convo: tool stats: %w", err)
	}
	var rep ToolReport
	var cur *ToolStat
	var lat []int64
	closeCur := func() {
		if cur == nil {
			return
		}
		if d := cur.OK + cur.Errors + cur.Timeouts; d > 0 {
			cur.SuccessRate = float64(cur.OK) / float64(d)
		}
		cur.P50MS, cur.P95MS = percentile(lat, 0.50), percentile(lat, 0.95)
		rep.Tools = append(rep.Tools, *cur)
	}
	for rows.Next() {
		var name string
		var outcome string
		var l int64
		if err := rows.Scan(&name, &outcome, &l); err != nil {
			rows.Close()
			return ToolReport{}, err
		}
		if cur == nil || cur.Name != name {
			closeCur()
			cur, lat = &ToolStat{Name: name}, lat[:0]
		}
		cur.Calls++
		switch outcome {
		case "ok":
			cur.OK++
			lat = append(lat, l)
		case "error":
			cur.Errors++
		case "timeout":
			cur.Timeouts++
		case "blocked":
			cur.Blocked++
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return ToolReport{}, err
	}
	closeCur()
	sort.SliceStable(rep.Tools, func(i, j int) bool { return rep.Tools[i].Calls > rep.Tools[j].Calls })

	frows, err := s.db.QueryContext(ctx, `SELECT name, outcome, args_summary, result_summary, chat_id, turn_id, latency_ms, created_at
		FROM tool_calls WHERE `+w+` AND outcome != 'ok' ORDER BY created_at DESC, id DESC LIMIT ?`, append(a, recentFailN)...)
	if err != nil {
		return ToolReport{}, fmt.Errorf("convo: tool failures: %w", err)
	}
	defer frows.Close()
	for frows.Next() {
		var tf ToolFailure
		var at int64
		if err := frows.Scan(&tf.Name, &tf.Outcome, &tf.ArgsSummary, &tf.ResultSummary, &tf.ChatID, &tf.TurnID, &tf.LatencyMS, &at); err != nil {
			return ToolReport{}, err
		}
		tf.At = fromMS(at)
		rep.RecentFailures = append(rep.RecentFailures, tf)
	}
	return rep, frows.Err()
}

// percentile is nearest-rank over ascending-sorted values.
func percentile(sorted []int64, p float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	i := int(math.Ceil(p*float64(len(sorted)))) - 1
	if i < 0 {
		i = 0
	}
	return sorted[i]
}
