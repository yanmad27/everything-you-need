package convo

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"everything-you-need/m/services/chat"
)

var ictTZ = time.FixedZone("ICT", 7*3600)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time  { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) set(t time.Time) { c.mu.Lock(); c.t = t; c.mu.Unlock() }

func open(t *testing.T, mod func(*Options)) (*Store, *clock) {
	t.Helper()
	clk := &clock{t: time.Date(2026, 10, 21, 9, 0, 0, 0, ictTZ)}
	opt := Options{Now: clk.now, NoBackground: true, PricePromptPer1K: 1, PriceCompletionPer1K: 2}
	if mod != nil {
		mod(&opt)
	}
	s, err := Open(filepath.Join(t.TempDir(), "sub", "convo.db"), opt)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, clk
}

func flush(t *testing.T, s *Store) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Flush(ctx); err != nil {
		t.Fatal(err)
	}
}

var bg = context.Background()

func in(chatID, user, name, text string) chat.ConvoEvent {
	return chat.ConvoEvent{Platform: "zalo", ChatID: chatID, ChatType: "PRIVATE", UserID: user, UserName: name, TurnID: "t", Direction: "in", Kind: "text", Text: text}
}

func count(t *testing.T, s *Store, table string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestMigrationsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.db")
	for i := 0; i < 3; i++ {
		s, err := Open(path, Options{NoBackground: true})
		if err != nil {
			t.Fatal(err)
		}
		s.RecordConvo(bg, in("c1", "u1", "An", "hi"))
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
	s, _ := Open(path, Options{NoBackground: true})
	defer s.Close()
	if n := count(t, s, "messages"); n != 3 {
		t.Errorf("messages = %d, want 3 (data kept across reopen)", n)
	}
	var mode string
	_ = s.db.QueryRow("PRAGMA journal_mode").Scan(&mode)
	if mode != "wal" {
		t.Errorf("journal_mode = %q", mode)
	}
}

func TestWriterBatchesAndFlushesOnClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.db")
	s, err := Open(path, Options{NoBackground: true, BatchSize: 10, FlushEvery: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 25; i++ {
		s.RecordConvo(bg, in("c1", "u1", "An", fmt.Sprint("m", i)))
	}
	if err := s.Close(); err != nil { // 25 = 2 full batches + remainder flushed on close
		t.Fatal(err)
	}
	s2, _ := Open(path, Options{NoBackground: true})
	defer s2.Close()
	if n := count(t, s2, "messages"); n != 25 {
		t.Errorf("messages after close = %d, want 25", n)
	}
	// Recording after Close is a no-op, never a panic.
	s.RecordConvo(bg, in("c1", "u1", "An", "late"))
	s.RecordToolCall(bg, chat.ToolEvent{Name: "x", Outcome: "ok"})
}

func TestEnqueueNeverBlocksAndDropsWhenFull(t *testing.T) {
	s, _ := open(t, func(o *Options) { o.QueueSize = 3 })
	// Stall the writer: hold SQLite's write lock so its batch blocks on busy.
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO chats(platform,chat_id,first_seen,last_seen) VALUES('x','x',0,0)`); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		for i := 0; i < 50; i++ {
			s.RecordConvo(bg, in("c1", "u1", "An", "m"))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RecordConvo blocked on a full queue")
	}
	_ = tx.Rollback()
	if s.Dropped() == 0 {
		t.Error("expected drops with a stuck writer and a 3-slot queue")
	}
	flush(t, s)
	if got := int64(count(t, s, "messages")) + s.Dropped(); got != 50 {
		t.Errorf("stored+dropped = %d, want 50", got)
	}
}

func TestPrune(t *testing.T) {
	s, clk := open(t, func(o *Options) { o.RetentionDays = 30 })
	old := clk.now().AddDate(0, 0, -40)
	clk.set(old)
	s.RecordConvo(bg, in("old", "u-old", "Old", "ancient"))
	s.RecordToolCall(bg, chat.ToolEvent{Platform: "zalo", ChatID: "old", Name: "lunar_date", Outcome: "ok"})
	s.RecordMessage(bg, chat.MessageEvent{Platform: "zalo", ChatID: "old", Total: 5})
	clk.set(old.AddDate(0, 0, 35)) // 5 days ago
	s.RecordConvo(bg, in("new", "u-new", "New", "recent"))
	flush(t, s)
	clk.set(old.AddDate(0, 0, 40))
	n, err := s.Prune(bg)
	if err != nil || n == 0 {
		t.Fatalf("prune n=%d err=%v", n, err)
	}
	for tbl, want := range map[string]int{"messages": 1, "tool_calls": 0, "usage": 0, "chats": 1, "users": 1} {
		if got := count(t, s, tbl); got != want {
			t.Errorf("%s = %d, want %d", tbl, got, want)
		}
	}
}

func TestNoSecretsOrURLsStored(t *testing.T) {
	s, _ := open(t, nil)
	s.RecordToolCall(bg, chat.ToolEvent{Platform: "zalo", ChatID: "c", Name: "fetch_url", Outcome: "ok",
		Args:   `{"url":"https://user:pw@Example.COM:8443/private/path?token=SECRET#frag"}`,
		Result: "<tool_result>page text with https://secret.example/x?k=1</tool_result>"})
	s.RecordToolCall(bg, chat.ToolEvent{Platform: "zalo", ChatID: "c", Name: "web_search", Outcome: "ok",
		Args: `{"query":"  giá   vàng "}`, Result: "hits https://a.example/p"})
	flush(t, s)
	rows, err := s.db.Query(`SELECT name, args_summary, result_summary FROM tool_calls ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got [][3]string
	for rows.Next() {
		var r [3]string
		_ = rows.Scan(&r[0], &r[1], &r[2])
		got = append(got, r)
	}
	if got[0][1] != "example.com" || got[1][1] != "giá vàng" {
		t.Errorf("args summaries = %q / %q", got[0][1], got[1][1])
	}
	for _, r := range got {
		for _, v := range r {
			for _, bad := range []string{"SECRET", "pw", "/private", "http", "token", "8443"} {
				if strings.Contains(v, bad) {
					t.Errorf("%q leaked in %q", bad, v)
				}
			}
		}
	}
	if SummarizeArgs("fetch_url", `{"url":"::bad"}`) != "(invalid url)" || SummarizeArgs("x", "not json") != "(invalid args)" {
		t.Error("bad input handling")
	}
}

func TestTruncation(t *testing.T) {
	s, _ := open(t, nil)
	s.RecordConvo(bg, in("c", "u", "n", strings.Repeat("é", 9000)))
	s.RecordToolCall(bg, chat.ToolEvent{Platform: "zalo", ChatID: "c", Name: "reminder_create", Outcome: "ok",
		Args: `{"task":"` + strings.Repeat("a", 1000) + `"}`, Result: "<tool_result name=\"reminder_create\">\n" + strings.Repeat("b", 1000) + "\n</tool_result>"})
	flush(t, s)
	var n int
	_ = s.db.QueryRow(`SELECT length(text) FROM messages`).Scan(&n)
	if n != maxTextRunes {
		t.Errorf("text len = %d", n)
	}
	var a, r string
	_ = s.db.QueryRow(`SELECT args_summary, result_summary FROM tool_calls`).Scan(&a, &r)
	if len([]rune(a)) != maxSummaryRunes || len(r) != maxSummaryRunes || strings.Contains(r, "tool_result") {
		t.Errorf("args=%d result=%d %.40q", len(a), len(r), r)
	}
}

// fixture: two chats, two users, spread over days.
func seed(t *testing.T, s *Store, clk *clock) {
	t.Helper()
	base := time.Date(2026, 10, 1, 9, 0, 0, 0, ictTZ)
	at := func(d int, h int) { clk.set(base.AddDate(0, 0, d).Add(time.Duration(h) * time.Hour)) }
	ev := func(chatID, typ, user, name, dir, kind, text, turn string) chat.ConvoEvent {
		return chat.ConvoEvent{Platform: "zalo", ChatID: chatID, ChatType: typ, UserID: user, UserName: name, Direction: dir, Kind: kind, Text: text, TurnID: turn}
	}
	at(0, 0)
	s.RecordConvo(bg, ev("A", "PRIVATE", "u1", "An", "in", "text", "xin chào pink", "t1"))
	at(0, 1)
	s.RecordToolCall(bg, chat.ToolEvent{Platform: "zalo", ChatID: "A", UserID: "u1", TurnID: "t1", Name: "get_prices", Outcome: "ok", Duration: 100 * time.Millisecond, Args: `{"source":"doji"}`, Result: "gia"})
	at(0, 2)
	s.RecordMessage(bg, chat.MessageEvent{Platform: "zalo", ChatID: "A", UserID: "u1", TurnID: "t1", Model: "m", Rounds: 2, PromptTokens: 1000, CompletionTokens: 500, Total: 1500})
	s.RecordConvo(bg, ev("A", "PRIVATE", "u1", "", "out", "text", "chào An", "t1"))
	at(1, 0)
	i := ev("A", "PRIVATE", "u1", "An", "in", "image", "ảnh gì", "t2")
	i.ImageMime, i.ImageBytes = "image/png", 2048
	s.RecordConvo(bg, i)
	s.RecordConvo(bg, chat.ConvoEvent{Platform: "zalo", ChatID: "A", Direction: "out", Kind: "system", Text: "⚠️", TurnID: "t2", ErrTag: "status=500"})
	at(2, 0)
	s.RecordConvo(bg, ev("B", "GROUP", "u2", "Binh", "in", "text", "nhắc tôi 100% mai", "t3"))
	s.RecordConvo(bg, ev("B", "GROUP", "u1", "An", "in", "text", "ok", "t4"))
	for k, lat := range []int{10, 20, 30, 40, 50, 60, 70, 80, 90, 100} {
		oc := "ok"
		if k == 3 {
			oc = "error"
		}
		if k == 9 {
			oc = "timeout"
		}
		s.RecordToolCall(bg, chat.ToolEvent{Platform: "zalo", ChatID: "B", UserID: "u2", TurnID: "t3", Name: "reminder_create", Outcome: oc, Duration: time.Duration(lat) * time.Millisecond, Args: fmt.Sprintf(`{"n":%d}`, k)})
		clk.set(clk.now().Add(time.Second))
	}
	s.RecordMessage(bg, chat.MessageEvent{Platform: "zalo", ChatID: "B", UserID: "u2", TurnID: "t3", Rounds: 1, PromptTokens: 2000, CompletionTokens: 0, Total: 2000})
	flush(t, s)
}

func TestListChats(t *testing.T) {
	s, clk := open(t, nil)
	seed(t, s, clk)
	all, total, err := s.ListChats(bg, ChatFilter{}, Page{})
	if err != nil || total != 2 || len(all) != 2 {
		t.Fatalf("all=%+v total=%d err=%v", all, total, err)
	}
	if all[0].ChatID != "B" || all[0].Messages != 2 || all[0].ToolCalls != 10 || all[0].ChatType != "GROUP" {
		t.Errorf("B = %+v", all[0])
	}
	a := all[1]
	if a.Messages != 4 || a.ToolCalls != 1 || a.FirstActivity.After(a.LastActivity) || !a.FirstSeen.Equal(a.FirstActivity) {
		t.Errorf("A = %+v", a)
	}
	check := func(name string, f ChatFilter, p Page, wantIDs string, wantTotal int) {
		t.Helper()
		got, tot, err := s.ListChats(bg, f, p)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var ids []string
		for _, c := range got {
			ids = append(ids, c.ChatID)
		}
		if strings.Join(ids, ",") != wantIDs || tot != wantTotal {
			t.Errorf("%s: ids=%v total=%d, want %s/%d", name, ids, tot, wantIDs, wantTotal)
		}
	}
	check("platform", ChatFilter{Platform: "zalo"}, Page{}, "B,A", 2)
	check("other platform", ChatFilter{Platform: "telegram"}, Page{}, "", 0)
	check("type", ChatFilter{ChatType: "group"}, Page{}, "B", 1)
	check("q text", ChatFilter{Q: "pink"}, Page{}, "A", 1)
	check("q id", ChatFilter{Q: "B"}, Page{}, "B", 1)
	check("q wildcard is literal", ChatFilter{Q: "%"}, Page{}, "B", 1) // only "100%" matches
	check("q underscore literal", ChatFilter{Q: "_"}, Page{}, "", 0)
	base := time.Date(2026, 10, 1, 0, 0, 0, 0, ictTZ)
	check("range day0", ChatFilter{From: base, To: base.AddDate(0, 0, 1)}, Page{}, "A", 1)
	check("range day2", ChatFilter{From: base.AddDate(0, 0, 2)}, Page{}, "B", 1)
	check("page1", ChatFilter{}, Page{Limit: 1}, "B", 2)
	check("page2", ChatFilter{}, Page{Limit: 1, Offset: 1}, "A", 2)
	check("page beyond", ChatFilter{}, Page{Limit: 1, Offset: 5}, "", 2)
	// counts respect the range
	got, _, _ := s.ListChats(bg, ChatFilter{From: base, To: base.AddDate(0, 0, 1)}, Page{})
	if got[0].Messages != 2 {
		t.Errorf("ranged messages = %d, want 2", got[0].Messages)
	}
}

func TestTimeline(t *testing.T) {
	s, clk := open(t, nil)
	seed(t, s, clk)
	items, total, err := s.Timeline(bg, "zalo", "A", Page{})
	if err != nil || total != 5 || len(items) != 5 {
		t.Fatalf("items=%d total=%d err=%v", len(items), total, err)
	}
	var seq []string
	for _, it := range items {
		if it.Type == "message" {
			seq = append(seq, it.Message.Direction+":"+it.Message.Kind)
		} else {
			seq = append(seq, "tool:"+it.Tool.Name)
		}
	}
	want := "in:text,tool:get_prices,out:text,in:image,out:system"
	if got := strings.Join(seq, ","); got != want {
		t.Errorf("seq=%s, want %s", got, want)
	}
	if items[0].Message.UserName != "An" || items[0].Message.UserID != "u1" || items[2].Message.UserID != "" {
		t.Errorf("users: %+v %+v", items[0].Message, items[2].Message)
	}
	img := items[3].Message
	if img.Kind != "image" || img.ImageMime != "image/png" || img.ImageBytes != 2048 {
		t.Errorf("image = %+v", img)
	}
	if items[4].Message.ErrorTag != "status=500" || items[1].Tool.ArgsSummary != `{"source":"doji"}` || items[1].TurnID != "t1" {
		t.Errorf("tool/err: %+v %+v", items[4].Message, items[1].Tool)
	}
	for i := 1; i < len(items); i++ {
		if items[i].At.Before(items[i-1].At) {
			t.Error("not ascending")
		}
	}
	desc, _, _ := s.Timeline(bg, "zalo", "A", Page{Desc: true, Limit: 2})
	if len(desc) != 2 || desc[0].Message.Kind != "system" {
		t.Errorf("desc = %+v", desc)
	}
	p2, _, _ := s.Timeline(bg, "zalo", "A", Page{Limit: 2, Offset: 4})
	if len(p2) != 1 {
		t.Errorf("page2 = %d", len(p2))
	}
	if other, tot, _ := s.Timeline(bg, "telegram", "A", Page{}); len(other) != 0 || tot != 0 {
		t.Error("platform scoping")
	}
}

func TestUserAndChatStats(t *testing.T) {
	s, clk := open(t, nil)
	seed(t, s, clk)
	us, err := s.UserStats(bg, StatsFilter{Platform: "zalo"})
	if err != nil || len(us) != 2 {
		t.Fatalf("us=%+v err=%v", us, err)
	}
	var u1, u2 Stat
	for _, u := range us {
		if u.ID == "u1" {
			u1 = u
		} else {
			u2 = u
		}
	}
	// u1: inbound A text, A image, B "ok" = 3 messages on 3 distinct days, 1 image.
	if u1.Messages != 3 || u1.ActiveDays != 3 || u1.ImagesSent != 1 || u1.Name != "An" || u1.RemindersCreated != 0 {
		t.Errorf("u1 = %+v", u1)
	}
	if u1.PromptTokens != 1000 || u1.CompletionTokens != 500 || u1.TotalTokens != 1500 || math.Abs(u1.EstCost-2.0) > 1e-9 {
		t.Errorf("u1 usage = %+v", u1)
	}
	if len(u1.TopTools) != 1 || u1.TopTools[0] != (ToolCount{"get_prices", 1}) {
		t.Errorf("u1 tools = %+v", u1.TopTools)
	}
	// u2: 8 reminder_create ok (10 minus error, timeout), 2 errors, 1 message.
	if u2.Messages != 1 || u2.RemindersCreated != 8 || u2.Errors != 2 || u2.TotalTokens != 2000 || math.Abs(u2.EstCost-2.0) > 1e-9 {
		t.Errorf("u2 = %+v", u2)
	}
	if us[0].ID != "u1" {
		t.Errorf("order: %v first", us[0].ID)
	}
	one, _ := s.UserStats(bg, StatsFilter{Platform: "zalo", ChatID: "B"})
	if len(one) != 2 {
		t.Errorf("chat-scoped users = %+v", one)
	}
	for _, u := range one {
		if u.ID == "u1" && (u.Messages != 1 || u.ImagesSent != 0) {
			t.Errorf("scoped u1 = %+v", u)
		}
	}
	base := time.Date(2026, 10, 1, 0, 0, 0, 0, ictTZ)
	ranged, _ := s.UserStats(bg, StatsFilter{From: base, To: base.AddDate(0, 0, 1)})
	if len(ranged) != 1 || ranged[0].ID != "u1" || ranged[0].Messages != 1 {
		t.Errorf("ranged = %+v", ranged)
	}

	cs, err := s.ChatStats(bg, StatsFilter{})
	if err != nil || len(cs) != 2 {
		t.Fatalf("cs=%+v err=%v", cs, err)
	}
	var ca Stat
	for _, c := range cs {
		if c.ID == "A" {
			ca = c
		}
	}
	// chat A also counts the failed system reply as an error.
	if ca.Messages != 2 || ca.ActiveDays != 2 || ca.ImagesSent != 1 || ca.Errors != 1 || ca.TotalTokens != 1500 {
		t.Errorf("chat A = %+v", ca)
	}
}

func TestToolStats(t *testing.T) {
	s, clk := open(t, nil)
	seed(t, s, clk)
	rep, err := s.ToolStats(bg, StatsFilter{Platform: "zalo"})
	if err != nil || len(rep.Tools) != 2 {
		t.Fatalf("rep=%+v err=%v", rep, err)
	}
	rc := rep.Tools[0]
	if rc.Name != "reminder_create" || rc.Calls != 10 || rc.OK != 8 || math.Abs(rc.SuccessRate-0.8) > 1e-9 || rc.P50MS != 50 || rc.P95MS != 100 {
		t.Errorf("reminder_create = %+v", rc)
	}
	gp := rep.Tools[1]
	if gp.Name != "get_prices" || gp.Calls != 1 || gp.SuccessRate != 1 || gp.P50MS != 100 || gp.P95MS != 100 {
		t.Errorf("get_prices = %+v", gp)
	}
	if len(rep.RecentFailures) != 2 || rep.RecentFailures[0].Outcome != "timeout" || rep.RecentFailures[1].Outcome != "error" {
		t.Errorf("failures = %+v", rep.RecentFailures)
	}
	scoped, _ := s.ToolStats(bg, StatsFilter{ChatID: "A"})
	if len(scoped.Tools) != 1 || scoped.Tools[0].Name != "get_prices" || len(scoped.RecentFailures) != 0 {
		t.Errorf("scoped = %+v", scoped)
	}
}

func TestRecentFailuresCappedAt20(t *testing.T) {
	s, _ := open(t, nil)
	for i := 0; i < 30; i++ {
		s.RecordToolCall(bg, chat.ToolEvent{Platform: "zalo", ChatID: "c", Name: "fetch_url", Outcome: "error"})
	}
	flush(t, s)
	rep, _ := s.ToolStats(bg, StatsFilter{})
	if len(rep.RecentFailures) != 20 || rep.Tools[0].SuccessRate != 0 {
		t.Errorf("failures=%d rate=%v", len(rep.RecentFailures), rep.Tools[0].SuccessRate)
	}
}

func TestPercentile(t *testing.T) {
	v := []int64{1, 2, 3, 4}
	if percentile(v, 0.5) != 2 || percentile(v, 0.95) != 4 || percentile(nil, 0.5) != 0 || percentile([]int64{7}, 0.95) != 7 {
		t.Error("nearest-rank percentile")
	}
}
