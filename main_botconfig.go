package main

import (
	"context"
	"crypto/sha256"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"everything-you-need/m/services/botconfig"
	"everything-you-need/m/services/chat"
	"everything-you-need/m/services/config"
	"everything-you-need/m/services/convo"
	"everything-you-need/m/services/llm"
	"everything-you-need/m/services/news"
)

// botProvider is the live per-bot settings provider (nil until built). The
// convo store reads prices/retention through it at query time.
var botProvider atomic.Pointer[botconfig.Provider]

func formatPrices(ps []convo.Price) string {
	var b strings.Builder
	for i, p := range ps {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(p.Model + ":" + trimFloat(p.PromptPer1M) + ":" + trimFloat(p.CompletionPer1M))
	}
	return b.String()
}

func trimFloat(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// buildBotConfig creates the provider over the convo store; failure only
// disables live settings (env/config values keep applying).
func buildBotConfig(cfg *config.Config, store *convo.Store) *botconfig.Provider {
	if store == nil {
		return nil
	}
	prices, _ := convoPrices(cfg.Convo, os.Getenv("CONVO_PRICES"))
	defs := map[string]botconfig.Defaults{botconfig.PlatformZalo: botconfig.ZaloDefaults(cfg, formatPrices(prices))}
	p, err := botconfig.New(context.Background(), store, os.Getenv("BOTCONFIG_SECRET"), defs)
	if err != nil {
		log.Printf("Bot settings disabled: %v", err)
		return nil
	}
	botProvider.Store(p)
	log.Printf("Bot settings enabled (secrets_enabled=%t)", !p.SecretsDisabled())
	return p
}

// zaloLive turns the current botconfig snapshot into chat.Live and caches the
// outbound clients (keyed by their inputs, so a save never leaves a stale one).
type zaloLive struct {
	prov *botconfig.Provider

	mu         sync.Mutex
	llmKey     [32]byte
	llmClient  llm.ToolCompleter
	srchKey    [32]byte
	srchClient chat.WebSearch
}

func (z *zaloLive) llm(base, key, model string) llm.ToolCompleter {
	h := sha256.Sum256([]byte(base + "\x00" + key + "\x00" + model))
	z.mu.Lock()
	defer z.mu.Unlock()
	if z.llmClient == nil || z.llmKey != h {
		z.llmClient, z.llmKey = llm.NewOpenAIClient(base, key, model, 0), h
	}
	return z.llmClient
}

func (z *zaloLive) search(key string) chat.WebSearch {
	h := sha256.Sum256([]byte(key))
	z.mu.Lock()
	defer z.mu.Unlock()
	if z.srchClient == nil || z.srchKey != h {
		z.srchClient, z.srchKey = searchAdapter{news.NewWebSearcher(key)}, h
	}
	return z.srchClient
}

// bootCompleter is the OpenAI-compatible client from the stored settings, for a
// start where the env does not configure one; nil when base URL or key is unset.
func (z *zaloLive) bootCompleter() llm.Completer {
	s := z.prov.Snapshot(botconfig.PlatformZalo).Zalo()
	if s.LLMBaseURL == "" || s.LLMAPIKey.Reveal() == "" {
		return nil
	}
	return llm.NewOpenAIClient(s.LLMBaseURL, s.LLMAPIKey.Reveal(), s.LLMModel, 0)
}

// source captures one snapshot and maps it to the turn settings.
func (z *zaloLive) source() chat.Live {
	s := z.prov.Snapshot(botconfig.PlatformZalo).Zalo()
	lv := chat.Live{Set: true, Paused: !s.Enabled, Model: s.LLMModel, Persona: s.Persona, ImagesOff: !s.Tools.Images,
		FetchAllow: s.FetchAllowDomains, DueChats: s.PrivateToolChats, RatePerMin: float64(s.RateLimitPerMin), Burst: s.RateLimitBurst,
		DisabledTools: map[string]bool{}}
	for name, on := range map[string]bool{"get_prices": s.Tools.Prices, "lunar_date": s.Tools.Lunar, "news_recent": s.Tools.News, "web_search": s.Tools.WebSearch,
		"fetch_url": s.Tools.Fetch, "due_dates": s.Tools.DueDates, "reminder_create": s.Tools.Reminders, "reminder_list": s.Tools.Reminders, "reminder_cancel": s.Tools.Reminders} {
		if !on {
			lv.DisabledTools[name] = true
		}
	}
	if s.LLMBaseURL != "" && s.LLMAPIKey.Reveal() != "" {
		lv.Client = z.llm(s.LLMBaseURL, s.LLMAPIKey.Reveal(), s.LLMModel)
	}
	if k := s.SerperAPIKey.Reveal(); k != "" {
		lv.Search = z.search(k)
	}
	return lv
}
