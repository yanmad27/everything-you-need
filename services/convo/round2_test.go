package convo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"everything-you-need/m/services/chat"
)

func TestStartupPruneRunsAndCloseCancels(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.db")
	now := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	old, _ := Open(path, Options{NoBackground: true, Now: func() time.Time { return now.AddDate(0, 0, -200) }})
	old.RecordConvo(bg, in("old", "u", "n", "ancient"))
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	got := make(chan int64, 4)
	s, err := Open(path, Options{Now: func() time.Time { return now }, StartupPruneDelay: time.Millisecond,
		OnPrune: func(n int64, err error) {
			if err != nil {
				n = -1
			}
			got <- n
		}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case n := <-got:
		if n <= 0 {
			t.Errorf("startup prune deleted %d", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no startup prune")
	}
	if n := count(t, s, "messages"); n != 0 {
		t.Errorf("messages = %d", n)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if s.ctx.Err() == nil {
		t.Error("Close must cancel the prune context")
	}
	if _, err := s.Prune(s.ctx); err == nil {
		t.Error("prune with cancelled ctx should fail")
	}
}

func TestPruneTruncatesWAL(t *testing.T) {
	s, clk := open(t, func(o *Options) { o.RetentionDays = 1 })
	old := clk.now().AddDate(0, 0, -5)
	clk.set(old)
	for i := 0; i < 200; i++ {
		s.RecordConvo(bg, in("c", "u", "n", strings.Repeat("x", 500)))
	}
	flush(t, s)
	clk.set(old.AddDate(0, 0, 5))
	if n, err := s.Prune(bg); err != nil || n == 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	var path string
	_ = s.db.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&path)
	if fi, err := os.Stat(path + "-wal"); err == nil && fi.Size() != 0 {
		t.Errorf("wal size after prune = %d", fi.Size())
	}
}

func TestFailedBatchCountsAsDropped(t *testing.T) {
	s, _ := open(t, nil)
	if _, err := s.db.Exec(`DROP TABLE messages`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		s.RecordConvo(bg, in("c", "u", "n", "x"))
	}
	flush(t, s)
	if s.Dropped() != 3 {
		t.Errorf("dropped = %d, want 3", s.Dropped())
	}
}

func TestFilePermissions(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "new", "convo.db")
	s, err := Open(path, Options{NoBackground: true})
	if err != nil {
		t.Fatal(err)
	}
	s.RecordConvo(bg, in("c", "u", "n", "x"))
	_ = s.Close()
	mode := func(p string) os.FileMode {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		return fi.Mode().Perm()
	}
	if mode(path) != 0o600 || mode(filepath.Dir(path)) != 0o700 {
		t.Errorf("file %v dir %v", mode(path), mode(filepath.Dir(path)))
	}
	for _, suf := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(path + suf); err == nil && mode(path+suf) != 0o600 {
			t.Errorf("%s mode %v", suf, mode(path+suf))
		}
	}
	// an existing directory is left alone, a loose existing file is tightened.
	shared := filepath.Join(root, "shared")
	_ = os.Mkdir(shared, 0o755)
	_ = os.Chmod(shared, 0o755)
	p2 := filepath.Join(shared, "convo.db")
	_ = os.WriteFile(p2, nil, 0o644)
	s2, err := Open(p2, Options{NoBackground: true})
	if err != nil {
		t.Fatal(err)
	}
	_ = s2.Close()
	if mode(shared) != 0o755 || mode(p2) != 0o600 {
		t.Errorf("shared dir %v file %v", mode(shared), mode(p2))
	}
}

func TestPrivateToolResultsStoreSizeOnly(t *testing.T) {
	for _, name := range []string{"due_dates", "reminder_list", "news_recent", "fetch_url", "web_search"} {
		got := SummarizeResult(name, "ok", "<tool_result name=\"x\">\nsecret task 123\n</tool_result>")
		if !strings.HasPrefix(got, "ok (") || strings.Contains(got, "secret") {
			t.Errorf("%s: %q", name, got)
		}
	}
	if got := SummarizeResult("lunar_date", "ok", "Âm lịch 1/1"); got != "Âm lịch 1/1" {
		t.Errorf("lunar: %q", got)
	}
	long := `{"task":"` + strings.Repeat("ế", 1000) + `"}`
	if n := len([]rune(SummarizeArgs("reminder_create", long))); n != maxSummaryRunes {
		t.Errorf("args runes = %d", n)
	}
}

func TestStatsConsistency(t *testing.T) {
	s, clk := open(t, nil)
	seed(t, s, clk)
	// a rate-limit notice in chat A by u1's turn, and an inbound blocked tool call.
	s.RecordConvo(bg, chat.ConvoEvent{Platform: "zalo", ChatID: "A", Direction: "in", UserID: "u1", UserName: "An", Kind: "text", TurnID: "t9", ErrTag: "rate_limited"})
	s.RecordConvo(bg, chat.ConvoEvent{Platform: "zalo", ChatID: "A", Direction: "out", Kind: "system", TurnID: "t9", ErrTag: "rate_limited"})
	s.RecordToolCall(bg, chat.ToolEvent{Platform: "zalo", ChatID: "A", UserID: "u1", Name: "get_prices", Outcome: "blocked", Duration: time.Second})
	flush(t, s)

	us, _ := s.UserStats(bg, StatsFilter{})
	cs, _ := s.ChatStats(bg, StatsFilter{})
	var u1, ca Stat
	for _, u := range us {
		if u.ID == "u1" {
			u1 = u
		}
	}
	for _, c := range cs {
		if c.ID == "A" {
			ca = c
		}
	}
	// failed reply (t2, status=500) is attributed to u1 via its turn; notice is not an error.
	if u1.Errors != 1 || u1.Notices != 1 {
		t.Errorf("u1 errors=%d notices=%d", u1.Errors, u1.Notices)
	}
	if ca.Errors != 1 || ca.Notices != 1 {
		t.Errorf("chat A errors=%d notices=%d", ca.Errors, ca.Notices)
	}
	if ca.Name != "An" {
		t.Errorf("chat name = %q, want participant fallback", ca.Name)
	}
	// u1 errors == chat A errors because u1 is the only one failing in A and B has none for u1.
	// Q filters
	qc, _ := s.ChatStats(bg, StatsFilter{Q: "an"})
	if len(qc) != 2 { // "An" is a participant of A and B (u1 spoke in both)
		t.Errorf("chat q=an: %d", len(qc))
	}
	qc, _ = s.ChatStats(bg, StatsFilter{Q: "binh"})
	if len(qc) != 1 || qc[0].ID != "B" {
		t.Errorf("chat q=binh: %+v", qc)
	}
	qu, _ := s.UserStats(bg, StatsFilter{Q: "BIN"})
	if len(qu) != 1 || qu[0].ID != "u2" {
		t.Errorf("user q: %+v", qu)
	}

	rep, _ := s.ToolStats(bg, StatsFilter{ChatID: "A"})
	gp := rep.Tools[0]
	if gp.Calls != 2 || gp.OK != 1 || gp.Blocked != 1 || gp.SuccessRate != 1 || gp.P95MS != 100 {
		t.Errorf("get_prices = %+v", gp) // blocked excluded from rate and latency
	}
}
