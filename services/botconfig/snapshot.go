// Package botconfig holds per-bot settings that the owner edits live in /admin.
// Precedence: env/config value = default; a stored row overrides it; deleting the
// row ("reset") restores the default. Secrets are AES-256-GCM encrypted with a key
// derived from BOTCONFIG_SECRET and never appear in logs, audit rows or String().
package botconfig

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"everything-you-need/m/services/config"
	"everything-you-need/m/services/convo"
)

const (
	PlatformZalo     = "zalo"
	PlatformTelegram = "telegram"
)

// Bot is a registry entry. Managed bots have editable keys.
type Bot struct {
	Platform string
	Managed  bool
}

// Bots lists the known bots. Telegram is listed but has no editable keys.
func Bots() []Bot {
	return []Bot{{PlatformZalo, true}, {PlatformTelegram, false}}
}

const redacted = "[redacted]"

// Secret is a secret string that prints as [redacted] everywhere; call Reveal
// to get the plaintext (internal callers only).
type Secret string

func (Secret) String() string               { return redacted }
func (Secret) GoString() string             { return redacted }
func (Secret) LogValue() slog.Value         { return slog.StringValue(redacted) }
func (Secret) MarshalJSON() ([]byte, error) { return []byte(`"` + redacted + `"`), nil }
func (Secret) MarshalText() ([]byte, error) { return []byte(redacted), nil }
func (s Secret) Reveal() string             { return string(s) }
func (s Secret) IsSet() bool                { return s != "" }

// Masked is what UIs may show of a secret.
type Masked struct {
	Set   bool   `json:"set"`
	Last4 string `json:"last4"`
}

func mask(s Secret) Masked {
	if s == "" {
		return Masked{}
	}
	l4 := ""
	if len(s) >= 12 { // never reveal a meaningful share of a short secret
		l4 = string(s[len(s)-4:])
	}
	return Masked{Set: true, Last4: l4}
}

// Defaults are the env/config-derived values, in the textual form of each key.
type Defaults map[string]string

// ZaloDefaults derives the zalo defaults from the loaded config; pricesSpec is
// the effective CONVO_PRICES-format string.
func ZaloDefaults(cfg *config.Config, pricesSpec string) Defaults {
	ret := cfg.Convo.RetentionDays
	if ret <= 0 {
		ret = 90
	}
	b := func(v bool) string { return fmt.Sprint(v) }
	return Defaults{
		KeyEnabled: b(cfg.Zalo.Enabled), KeyLLMBaseURL: cfg.LLMOpenAI.BaseURL, KeyLLMModel: cfg.LLMOpenAI.Model, KeyLLMAPIKey: cfg.LLMOpenAI.APIKey,
		KeyPrices: pricesSpec, KeySerperAPIKey: cfg.News.SerperAPIKey,
		KeyToolPrices: "true", KeyToolLunar: "true", KeyToolNews: "true", KeyToolWebSearch: "true", KeyToolFetch: "true",
		KeyToolReminders: "true", KeyToolDueDates: "true", KeyToolImages: "true",
		KeyFetchAllow: cfg.Chat.FetchAllowDomains, KeyPrivateChats: cfg.Zalo.PrivateToolChats,
		KeyRatePerMin: "6", KeyRateBurst: "3", KeyPersona: "", KeyRetentionDays: fmt.Sprint(ret),
	}
}

// Snapshot is an immutable view of one bot's effective settings. Capture one per
// request and read everything from it.
type Snapshot struct {
	platform string
	vals     map[string]any // secrets held as Secret
	override map[string]bool
	updated  map[string]convo.BotSettingRow
	// Problems lists keys whose stored override could not be used (e.g. secret
	// undecryptable); the default applies. Key names only.
	Problems []string
}

func (s *Snapshot) Platform() string { return s.platform }

func (s *Snapshot) Bool(key string) bool       { v, _ := s.vals[key].(bool); return v }
func (s *Snapshot) Int(key string) int         { v, _ := s.vals[key].(int); return v }
func (s *Snapshot) Str(key string) string      { v, _ := s.vals[key].(string); return v }
func (s *Snapshot) Secret(key string) Secret   { v, _ := s.vals[key].(Secret); return v }
func (s *Snapshot) Overridden(key string) bool { return s.override[key] }
func (s *Snapshot) List(key string) []string {
	v, _ := s.vals[key].([]string)
	return append([]string{}, v...)
}

// Prices parses the prices setting (invalid entries were rejected on save).
func (s *Snapshot) Prices() []convo.Price {
	p, _ := convo.ParsePrices(s.Str(KeyPrices))
	return p
}

// Zalo returns the typed zalo settings.
func (s *Snapshot) Zalo() ZaloSettings {
	return ZaloSettings{
		Enabled: s.Bool(KeyEnabled), LLMBaseURL: s.Str(KeyLLMBaseURL), LLMModel: s.Str(KeyLLMModel), LLMAPIKey: s.Secret(KeyLLMAPIKey),
		Prices: s.Prices(),
		Tools: ToolFlags{Prices: s.Bool(KeyToolPrices), Lunar: s.Bool(KeyToolLunar), News: s.Bool(KeyToolNews), WebSearch: s.Bool(KeyToolWebSearch),
			Fetch: s.Bool(KeyToolFetch), Reminders: s.Bool(KeyToolReminders), DueDates: s.Bool(KeyToolDueDates), Images: s.Bool(KeyToolImages)},
		SerperAPIKey: s.Secret(KeySerperAPIKey), FetchAllowDomains: s.List(KeyFetchAllow), PrivateToolChats: s.List(KeyPrivateChats),
		RateLimitPerMin: s.Int(KeyRatePerMin), RateLimitBurst: s.Int(KeyRateBurst), Persona: s.Str(KeyPersona), RetentionDays: s.Int(KeyRetentionDays),
	}
}

type ToolFlags struct{ Prices, Lunar, News, WebSearch, Fetch, Reminders, DueDates, Images bool }

// ZaloSettings is the typed effective zalo configuration. Secret fields print redacted.
type ZaloSettings struct {
	Enabled           bool
	LLMBaseURL        string
	LLMModel          string
	LLMAPIKey         Secret
	Prices            []convo.Price
	Tools             ToolFlags
	SerperAPIKey      Secret
	FetchAllowDomains []string
	PrivateToolChats  []string
	RateLimitPerMin   int
	RateLimitBurst    int
	Persona           string
	RetentionDays     int
}

// String is safe to log: secrets are redacted and the persona is summarised.
func (z ZaloSettings) String() string {
	return fmt.Sprintf("ZaloSettings{enabled=%t base_url=%q model=%q api_key=%s serper_key=%s tools=%+v allow=%d private_chats=%d rate=%d/%d burst persona_len=%d retention=%d}",
		z.Enabled, z.LLMBaseURL, z.LLMModel, z.LLMAPIKey, z.SerperAPIKey, z.Tools, len(z.FetchAllowDomains), len(z.PrivateToolChats),
		z.RateLimitPerMin, z.RateLimitBurst, len(z.Persona), z.RetentionDays)
}
func (z ZaloSettings) GoString() string { return z.String() }
func (z ZaloSettings) LogValue() slog.Value {
	return slog.GroupValue(slog.Bool("enabled", z.Enabled), slog.String("llm_base_url", z.LLMBaseURL), slog.String("llm_model", z.LLMModel),
		slog.String("llm_api_key", redacted), slog.String("serper_api_key", redacted))
}

func (s *Snapshot) String() string {
	var ks []string
	for k := range s.vals {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	var b strings.Builder
	b.WriteString("Snapshot{" + s.platform)
	for _, k := range ks {
		if sp, ok := specFor(s.platform, k); ok && sp.Secret {
			fmt.Fprintf(&b, " %s=%s", k, redacted)
		} else if sp.Kind == KindText {
			fmt.Fprintf(&b, " %s=(len %d)", k, len(s.Str(k)))
		} else {
			fmt.Fprintf(&b, " %s=%v", k, s.vals[k])
		}
	}
	return b.String() + "}"
}
func (s *Snapshot) GoString() string { return s.String() }
func (s *Snapshot) LogValue() slog.Value {
	return slog.StringValue(s.String())
}

// SettingView is one row of the settings page.
type SettingView struct {
	Key        string `json:"key"`
	Label      string `json:"label"`
	Kind       Kind   `json:"kind"`
	Secret     bool   `json:"secret"`
	Sensitive  bool   `json:"sensitive"`
	Overridden bool   `json:"overridden"`
	Value      any    `json:"value"`      // effective value; nil for secrets
	Default    any    `json:"default"`    // env/config default; nil for secrets
	Masked     Masked `json:"masked"`     // secrets only
	UpdatedAt  int64  `json:"updated_at"` // ms, overrides only
	UpdatedBy  string `json:"updated_by"`
}

// FormatValue renders a non-secret value in the textual form Save accepts.
func FormatValue(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case []string:
		return strings.Join(x, ", ")
	case bool:
		return fmt.Sprint(x)
	case int:
		return fmt.Sprint(x)
	case string:
		return x
	}
	return fmt.Sprint(v)
}

// View lists every key with its effective value, default and override state.
func (s *Snapshot) View(defaults Defaults) []SettingView {
	var out []SettingView
	for _, sp := range Specs(s.platform) {
		v := SettingView{Key: sp.Key, Label: sp.Label, Kind: sp.Kind, Secret: sp.Secret, Sensitive: sp.Sensitive, Overridden: s.override[sp.Key]}
		if sp.Secret {
			v.Masked = mask(s.Secret(sp.Key))
		} else {
			v.Value = s.vals[sp.Key]
			if d, err := parseInput(sp, defaults[sp.Key]); err == nil {
				v.Default = d
			}
		}
		if r, ok := s.updated[sp.Key]; ok {
			v.UpdatedAt, v.UpdatedBy = r.UpdatedAt, r.UpdatedBy
		}
		out = append(out, v)
	}
	return out
}
