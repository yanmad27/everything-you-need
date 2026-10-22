package convo

import (
	"context"
	"math"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"everything-you-need/m/services/chat"
)

var ictZ = time.FixedZone("ICT", 7*3600)

type tclock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *tclock) now() time.Time  { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *tclock) set(t time.Time) { c.mu.Lock(); c.t = t; c.mu.Unlock() }

func usageStore(t *testing.T, o Options) (*Store, *tclock) {
	t.Helper()
	c := &tclock{t: time.Date(2026, 10, 1, 12, 0, 0, 0, ictZ)}
	o.Now, o.NoBackground = c.now, true
	s, err := Open(filepath.Join(t.TempDir(), "u.db"), o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, c
}

func rec(t *testing.T, s *Store, c *tclock, at time.Time, chatID, user, model string, p, co int) {
	t.Helper()
	c.set(at)
	s.RecordMessage(context.Background(), chat.MessageEvent{Platform: "zalo", ChatID: chatID, UserID: user, TurnID: at.String(), Model: model,
		Rounds: 1, PromptTokens: p, CompletionTokens: co, Total: p + co})
	if err := s.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func fp(v float64) *float64 { return &v }

func near(a *float64, want float64) bool {
	return a != nil && *a-want < 1e-9 && want-*a < 1e-9
}

func TestUsageQueriesAndPricing(t *testing.T) {
	s, c := usageStore(t, Options{Prices: []Price{{Model: "m-a", PromptPer1M: 1, CompletionPer1M: 10}}, Currency: "VND"})
	ctx := context.Background()
	d1 := time.Date(2026, 10, 1, 0, 30, 0, 0, ictZ) // 00:30 ICT = 17:30 UTC previous day
	rec(t, s, c, d1, "c1", "u1", "m-a", 1_000_000, 100_000)
	rec(t, s, c, d1.Add(2*time.Hour), "c2", "u1", "m-a", 500_000, 0)
	rec(t, s, c, time.Date(2026, 10, 3, 23, 59, 0, 0, ictZ), "c1", "u2", "m-b", 1000, 2000) // unpriced model

	tot, err := s.UsageTotals(ctx, UsageFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if tot.Turns != 3 || tot.PromptTokens != 1_501_000 || tot.CompletionTokens != 102_000 || tot.TotalTokens != 1_603_000 {
		t.Fatalf("totals %+v", tot)
	}
	// m-a: 1.5M*1 + 0.1M*10 = 2.5; m-b unpriced → partial, lower bound.
	if !near(tot.Cost, 2.5) || !tot.PartialPricing {
		t.Fatalf("cost %v partial %v", tot.Cost, tot.PartialPricing)
	}
	if s.Currency() != "VND" {
		t.Fatal("currency")
	}

	days, _ := s.UsageByDay(ctx, UsageFilter{})
	if len(days) != 2 || days[0].Day != "2026-10-01" || days[0].Turns != 2 || days[1].Day != "2026-10-03" {
		t.Fatalf("ICT day bucketing: %+v", days)
	}
	if days[1].Cost != nil || !days[1].PartialPricing {
		t.Fatalf("unpriced day must have nil cost: %+v", days[1])
	}
	// From/To (To exclusive) zero-fills the series.
	from, to := time.Date(2026, 10, 1, 0, 0, 0, 0, ictZ), time.Date(2026, 10, 5, 0, 0, 0, 0, ictZ)
	days, _ = s.UsageByDay(ctx, UsageFilter{From: from, To: to})
	if len(days) != 4 || days[1].Day != "2026-10-02" || days[1].Turns != 0 || !near(days[1].Cost, 0) {
		t.Fatalf("zero fill: %+v", days)
	}
	days, _ = s.UsageByDay(ctx, UsageFilter{From: from, To: time.Date(2026, 10, 3, 0, 0, 0, 0, ictZ)})
	if len(days) != 2 {
		t.Fatalf("To exclusive: %+v", days)
	}

	chats, n, _ := s.UsageByChat(ctx, UsageFilter{}, Page{})
	if n != 2 || chats[0].ChatID != "c1" || chats[0].Turns != 2 || chats[0].TotalTokens != 1_103_000 || chats[1].ChatID != "c2" {
		t.Fatalf("by chat: %+v", chats)
	}
	if !chats[0].PartialPricing || !near(chats[0].Cost, 2.0) || !near(chats[1].Cost, 0.5) || chats[1].PartialPricing {
		t.Fatalf("chat pricing: %+v %+v", chats[0], chats[1])
	}
	page, n, _ := s.UsageByChat(ctx, UsageFilter{}, Page{Limit: 1, Offset: 1})
	if n != 2 || len(page) != 1 || page[0].ChatID != "c2" {
		t.Fatalf("paging: %+v", page)
	}
	users, n, _ := s.UsageByUser(ctx, UsageFilter{}, Page{})
	if n != 2 || users[0].UserID != "u1" || !near(users[0].Cost, 2.5) || users[1].Cost != nil {
		t.Fatalf("by user: %+v", users)
	}
	models, _ := s.UsageByModel(ctx, UsageFilter{})
	if len(models) != 2 || models[0].Model != "m-a" || !models[0].Priced || models[1].Model != "m-b" || models[1].Priced || models[1].Cost != nil {
		t.Fatalf("by model: %+v", models)
	}
	// platform filter
	none, _ := s.UsageTotals(ctx, UsageFilter{Platform: "telegram"})
	if none.Turns != 0 || none.Cost == nil || *none.Cost != 0 || none.PartialPricing {
		t.Fatalf("empty: %+v", none)
	}
}

func TestLegacyPer1KFallbackAndStats(t *testing.T) {
	s, c := usageStore(t, Options{PricePromptPer1K: 2, PriceCompletionPer1K: 4})
	rec(t, s, c, c.now(), "c1", "u1", "anything", 1000, 1000)
	tot, _ := s.UsageTotals(context.Background(), UsageFilter{})
	if !near(tot.Cost, 6) || tot.PartialPricing {
		t.Fatalf("legacy fallback: %+v", tot)
	}
	st, _ := s.UserStats(context.Background(), StatsFilter{})
	if len(st) != 1 || st[0].EstCost < 5.999 || st[0].EstCost > 6.001 {
		t.Fatalf("stats: %+v", st)
	}
}

func TestStatsUnpricedFlagged(t *testing.T) {
	s, c := usageStore(t, Options{})
	rec(t, s, c, c.now(), "c1", "u1", "x", 10, 10)
	st, _ := s.ChatStats(context.Background(), StatsFilter{})
	if len(st) != 1 || st[0].EstCost != 0 || !st[0].PartialPricing {
		t.Fatalf("%+v", st)
	}
}

func TestParsePricesPerEntry(t *testing.T) {
	got, bad := ParsePrices(" gpt-6-sol:1.25:10, claude-haiku-4-5:1:5 ,")
	if len(bad) != 0 || len(got) != 2 || got[0] != (Price{"gpt-6-sol", 1.25, 10}) || got[1] != (Price{"claude-haiku-4-5", 1, 5}) {
		t.Fatalf("%+v %v", got, bad)
	}
	if got, bad := ParsePrices(""); got != nil || bad != nil {
		t.Fatal("blank")
	}
	// model names may contain ':'; split from the right.
	got, bad = ParsePrices("ns:model:v2:2:20")
	if len(bad) != 0 || len(got) != 1 || got[0] != (Price{"ns:model:v2", 2, 20}) {
		t.Fatalf("%+v %v", got, bad)
	}
	// bad entries are skipped individually, good ones kept; nothing echoes the value.
	got, bad = ParsePrices("good:1:2,m:1,m:a:2,:1:2,m:-1:2,m:NaN:1,m:1:Inf,also-good:3:4,m:1:2e999")
	if len(got) != 2 || got[0].Model != "good" || got[1].Model != "also-good" || len(bad) != 7 {
		t.Fatalf("%+v %v", got, bad)
	}
	for _, b := range bad {
		if strings.Contains(b, "NaN") || strings.Contains(b, "Inf") {
			t.Fatalf("value echoed: %s", b)
		}
	}
	for _, p := range []Price{{"m", math.NaN(), 1}, {"m", 1, math.Inf(1)}, {"m", -1, 1}, {" ", 1, 1}} {
		if p.Valid() == nil {
			t.Errorf("%+v valid", p)
		}
	}
}

func TestZeroFillNonMidnightBounds(t *testing.T) {
	s, c := usageStore(t, Options{})
	rec(t, s, c, time.Date(2026, 10, 2, 10, 0, 0, 0, ictZ), "c1", "u1", "x", 1, 1)
	days, _ := s.UsageByDay(context.Background(), UsageFilter{From: time.Date(2026, 10, 1, 13, 0, 0, 0, ictZ), To: time.Date(2026, 10, 3, 5, 0, 0, 0, ictZ)})
	if len(days) != 3 || days[0].Day != "2026-10-01" || days[2].Day != "2026-10-03" {
		t.Fatalf("%+v", days)
	}
}

func TestAdminCredentialsAndAudit(t *testing.T) {
	s, _ := usageStore(t, Options{})
	ctx := context.Background()
	if _, ok, _ := s.AdminPasswordHash(ctx); ok {
		t.Fatal("unexpected row")
	}
	_ = s.SetAdminPasswordHash(ctx, "owner", "h1")
	_ = s.SetAdminPasswordHash(ctx, "owner", "h2")
	if h, ok, _ := s.AdminPasswordHash(ctx); !ok || h != "h2" {
		t.Fatalf("%q %v", h, ok)
	}
	if cleared, _ := s.ClearAdminPasswordHash(ctx); !cleared {
		t.Fatal("clear")
	}
	if _, ok, _ := s.AdminPasswordHash(ctx); ok {
		t.Fatal("still stored")
	}
	_ = s.RecordAdminAudit(ctx, "login_ok", "1.2.3.4")
	if a, _ := s.AdminAudit(ctx); len(a) != 1 || a[0].Event != "login_ok" || a[0].IP != "1.2.3.4" {
		t.Fatalf("%+v", a)
	}
}

func TestDefaultPriceOverridesAndLiveFns(t *testing.T) {
	ctx := context.Background()
	live := []Price{}
	days := 0
	s, c := usageStore(t, Options{RetentionDays: 90, PricesFn: func() []Price { return live }, RetentionFn: func() int { return days }})
	rec(t, s, c, c.now(), "c1", "u1", "gpt-6-sol", 1_000_000, 1_000_000)
	rec(t, s, c, c.now(), "c1", "u1", "other", 10, 10)
	tot, _ := s.UsageTotals(ctx, UsageFilter{})
	if !near(tot.Cost, 12) || !tot.PartialPricing { // built-in gpt-6-sol 2 + 10; "other" unpriced
		t.Fatalf("built-in default: %+v", tot)
	}
	// a configured price for the same model wins, and is read live
	live = []Price{{Model: "GPT-6-SOL", PromptPer1M: 1, CompletionPer1M: 1}}
	if tot, _ = s.UsageTotals(ctx, UsageFilter{}); !near(tot.Cost, 2) {
		t.Fatalf("override: %+v", tot)
	}
	eff := s.Prices()
	if len(eff) != 1 || eff[0].PromptPer1M != 1 {
		t.Fatalf("effective prices: %+v", eff)
	}
	live = nil
	if eff = s.Prices(); len(eff) != 1 || eff[0] != DefaultPrices[0] {
		t.Fatalf("defaults listed: %+v", eff)
	}
	// retention is read at each prune: the fn's value applies, 0 falls back to the static 90 days
	c.set(c.now().Add(100 * 24 * time.Hour))
	days = 365
	if n, _ := s.Prune(ctx); n != 0 {
		t.Fatalf("365 days must keep 100-day-old rows: pruned %d", n)
	}
	days = 0
	if n, _ := s.Prune(ctx); n == 0 {
		t.Fatal("static 90 days must prune 100-day-old rows")
	}
}

func TestParsePricesRejectsDuplicates(t *testing.T) {
	got, bad := ParsePrices("a:1:2,b:3:4,A:5:6")
	if len(got) != 2 || got[0].PromptPer1M != 1 || len(bad) != 1 || !strings.Contains(bad[0], "duplicate") {
		t.Fatalf("%+v %v", got, bad)
	}
}
