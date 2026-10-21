package main

import (
	"context"
	"log"
	"strings"
	"time"

	"everything-you-need/m/services/chat"
	"everything-you-need/m/services/config"
	"everything-you-need/m/services/duedate"
	"everything-you-need/m/services/llm"
	"everything-you-need/m/services/news"
	pricetracker "everything-you-need/m/services/price-tracker"
	"everything-you-need/m/services/reminder"
)

// Adapters from the concrete services to chat's consumer-side interfaces.

type priceAdapter struct {
	r *pricetracker.CachedPriceReader
}

func (a priceAdapter) Sources() []string { return a.r.Sources() }
func (a priceAdapter) Get(ctx context.Context, source string) ([]chat.PriceQuote, time.Time, error) {
	ps, at, err := a.r.Get(ctx, source)
	if err != nil {
		return nil, time.Time{}, err
	}
	out := make([]chat.PriceQuote, len(ps))
	for i, p := range ps {
		out[i] = chat.PriceQuote{Type: p.Type, Buy: p.BuyPrice, Sell: p.SellPrice, Unit: p.Unit, Currency: p.Currency}
	}
	return out, at, nil
}

func toHits(rs []news.WebResult) []chat.WebHit {
	out := make([]chat.WebHit, len(rs))
	for i, r := range rs {
		out[i] = chat.WebHit{Title: r.Title, URL: r.URL, Snippet: r.Snippet, Source: r.Source, Date: r.Date}
	}
	return out
}

type searchAdapter struct{ w *news.WebSearcher }

func (a searchAdapter) Search(ctx context.Context, q string, n int) ([]chat.WebHit, error) {
	rs, err := a.w.Search(ctx, q, n)
	return toHits(rs), err
}
func (a searchAdapter) SearchNews(ctx context.Context, q string, n int) ([]chat.WebHit, error) {
	rs, err := a.w.SearchNews(ctx, q, n)
	return toHits(rs), err
}

type newsAdapter struct{ s *news.Store }

func (a newsAdapter) RecentPosted(ctx context.Context, limit int) ([]chat.WebHit, error) {
	rs, err := a.s.RecentPosted(ctx, limit)
	return toHits(rs), err
}

type dueAdapter struct{ s *duedate.Service }

func (a dueAdapter) Upcoming(ctx context.Context, days int) ([]chat.DueEntry, error) {
	items, err := a.s.Upcoming(ctx, days)
	out := make([]chat.DueEntry, len(items))
	for i, it := range items {
		out[i] = chat.DueEntry{Title: it.Title, Due: it.Due, Bucket: it.Bucket}
	}
	return out, err
}

type reminderAdapter struct{ s *reminder.Service }

func (a reminderAdapter) Create(ctx context.Context, platform, chatID, userID, userName, task string, at time.Time) (int64, error) {
	return a.s.CreateFor(ctx, platform, chatID, userID, userName, task, at)
}
func (a reminderAdapter) List(ctx context.Context, platform, chatID string) ([]chat.ReminderInfo, error) {
	rs, err := a.s.ListFor(ctx, platform, chatID)
	out := make([]chat.ReminderInfo, len(rs))
	for i, r := range rs {
		out[i] = chat.ReminderInfo{ID: r.ID, Task: r.Task, FireAt: r.FireAt}
	}
	return out, err
}
func (a reminderAdapter) Cancel(ctx context.Context, platform, chatID string, id int64) error {
	return a.s.CancelFor(ctx, platform, chatID, id)
}

// buildZaloTools assembles the tool dependencies for the Zalo assistant. Any
// service that is not configured is left nil, which omits its tool.
func buildZaloTools(cfg *config.Config, prices *pricetracker.PriceTrackerService, newsStore *news.Store,
	due *duedate.Service, rem *reminder.Service) chat.Tools {
	tl := chat.Tools{Platform: reminder.PlatformZalo, DueChats: strings.Split(cfg.Zalo.PrivateToolChats, ",")}
	if prices != nil {
		tl.Prices = priceAdapter{pricetracker.NewCachedPriceReader(prices, 2*time.Minute)}
	}
	if cfg.News.SerperAPIKey != "" { // independent of the news digest being enabled
		tl.Search = searchAdapter{news.NewWebSearcher(cfg.News.SerperAPIKey)}
	}
	if newsStore != nil {
		tl.News = newsAdapter{newsStore}
	}
	if due != nil {
		tl.Due = dueAdapter{due}
	}
	if rem != nil {
		tl.Reminders = reminderAdapter{rem}
	}
	return tl
}

// enableZaloTools turns on the tool-calling path for the Zalo chat service
// when its LLM supports tools; otherwise Zalo keeps the plain path.
func enableZaloTools(svc *chat.Service, completer llm.Completer, tl chat.Tools) {
	if svc == nil || completer == nil {
		return
	}
	tc, ok := completer.(llm.ToolCompleter)
	if !ok {
		log.Printf("Zalo chat tools disabled: LLM client has no tool support")
		return
	}
	svc.WithTools(tc, tl)
	log.Printf("Zalo chat tools enabled (prices=%t search=%t news=%t due=%t reminders=%t)",
		tl.Prices != nil, tl.Search != nil, tl.News != nil, tl.Due != nil && len(tl.DueChats) > 0, tl.Reminders != nil)
}
