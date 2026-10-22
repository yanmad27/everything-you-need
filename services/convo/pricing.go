package convo

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Price is the rate for one model, per 1,000,000 tokens, prompt and completion
// priced separately (in Options.Currency).
type Price struct {
	Model           string  `mapstructure:"model" json:"model"`
	PromptPer1M     float64 `mapstructure:"prompt_per_1m" json:"prompt_per_1m"`
	CompletionPer1M float64 `mapstructure:"completion_per_1m" json:"completion_per_1m"`
}

// Valid reports why a price is unusable (blank model, negative, NaN or infinite rate).
func (p Price) Valid() error {
	if strings.TrimSpace(p.Model) == "" {
		return fmt.Errorf("empty model")
	}
	for _, v := range []float64{p.PromptPer1M, p.CompletionPer1M} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			return fmt.Errorf("invalid rate")
		}
	}
	return nil
}

// ParsePrices parses the env form "model:prompt:completion,model:prompt:completion"
// (rates per 1M tokens), e.g. "gpt-6-sol:1.25:10,claude-haiku-4-5:1:5". Entries
// are independent: good ones are returned, each bad one adds a description to
// bad (entry number and reason only). The split is from the right, so model
// names may contain ':'.
func ParsePrices(spec string) (prices []Price, bad []string) {
	for i, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		j := strings.LastIndexByte(part, ':')
		k := -1
		if j > 0 {
			k = strings.LastIndexByte(part[:j], ':')
		}
		if k < 1 {
			bad = append(bad, fmt.Sprintf("entry %d: want model:prompt:completion", i+1))
			continue
		}
		p, err1 := strconv.ParseFloat(strings.TrimSpace(part[k+1:j]), 64)
		c, err2 := strconv.ParseFloat(strings.TrimSpace(part[j+1:]), 64)
		pr := Price{Model: strings.TrimSpace(part[:k]), PromptPer1M: p, CompletionPer1M: c}
		if err1 != nil || err2 != nil {
			bad = append(bad, fmt.Sprintf("entry %d: rate is not a number", i+1))
		} else if err := pr.Valid(); err != nil {
			bad = append(bad, fmt.Sprintf("entry %d: %v", i+1, err))
		} else {
			prices = append(prices, pr)
		}
	}
	return prices, bad
}

// DefaultPrices are built-in rates (USD per 1M tokens) used for a model unless
// CONVO_PRICES / the admin UI sets one. gpt-6-sol: OpenAI's pricing page
// (https://developers.openai.com/api/docs/pricing), standard short-context tier,
// input $2 / output $10. Cached input ($0.20) is not modelled: cached tokens are
// billed at the input price here, so the estimate is slightly high.
var DefaultPrices = []Price{{Model: "gpt-6-sol", PromptPer1M: 2, CompletionPer1M: 10}}

func (s *Store) configuredPrices() []Price {
	if s.opt.PricesFn != nil {
		return s.opt.PricesFn()
	}
	return s.opt.Prices
}

func (s *Store) retentionDays() int {
	if s.opt.RetentionFn != nil {
		if d := s.opt.RetentionFn(); d > 0 {
			return d
		}
	}
	return s.opt.RetentionDays
}

// priceFor returns the rate for model: a configured exact (case-insensitive)
// match, else a built-in default, else the legacy per-1K fallback when set,
// else ok=false.
func (s *Store) priceFor(model string) (Price, bool) {
	if model != "" {
		for _, p := range s.configuredPrices() {
			if strings.EqualFold(p.Model, model) {
				return p, true
			}
		}
		for _, p := range DefaultPrices {
			if strings.EqualFold(p.Model, model) {
				return p, true
			}
		}
	}
	if s.opt.PricePromptPer1K > 0 || s.opt.PriceCompletionPer1K > 0 {
		return Price{Model: model, PromptPer1M: s.opt.PricePromptPer1K * 1000, CompletionPer1M: s.opt.PriceCompletionPer1K * 1000}, true
	}
	return Price{}, false
}

// Currency is the label for every cost the store reports.
func (s *Store) Currency() string { return s.opt.Currency }

// Prices returns the effective per-model rates: configured ones plus built-in
// defaults not overridden (without the legacy fallback).
func (s *Store) Prices() []Price {
	out := append([]Price{}, s.configuredPrices()...)
	for _, d := range DefaultPrices {
		found := false
		for _, p := range out {
			if strings.EqualFold(p.Model, d.Model) {
				found = true
			}
		}
		if !found {
			out = append(out, d)
		}
	}
	return out
}

// UsageSum aggregates token usage. Cost covers only the priced part of the
// usage and is nil when none of it could be priced; PartialPricing is true when
// some tokens belong to a model with no configured price (so Cost is a lower
// bound, or absent). Cost is never 0 merely because a price is missing.
type UsageSum struct {
	Turns            int      `json:"turns"`
	PromptTokens     int      `json:"prompt_tokens"`
	CompletionTokens int      `json:"completion_tokens"`
	TotalTokens      int      `json:"total_tokens"`
	Cost             *float64 `json:"cost"`
	PartialPricing   bool     `json:"partial_pricing"`

	cost   float64
	priced bool
}

func (u *UsageSum) add(price Price, ok bool, turns, p, c, t int) {
	u.Turns += turns
	u.PromptTokens += p
	u.CompletionTokens += c
	u.TotalTokens += t
	if p+c+t == 0 {
		return
	}
	if !ok {
		u.PartialPricing = true
		return
	}
	u.cost += float64(p)/1e6*price.PromptPer1M + float64(c)/1e6*price.CompletionPer1M
	u.priced = true
}

func (u *UsageSum) finish() {
	if u.priced || (u.TotalTokens+u.PromptTokens+u.CompletionTokens == 0 && !u.PartialPricing) {
		c := u.cost
		u.Cost = &c
	}
}
