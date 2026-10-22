package config

type PriceSourcesConfig struct {
	Doji      DojiConfig      `mapstructure:"doji"`
	BTMC      BTMCConfig      `mapstructure:"btmc"`
	Mihong    MihongConfig    `mapstructure:"mihong"`
	KimKhoa   KimKhoaConfig   `mapstructure:"kimkhoa"`
	CoinGecko CoinGeckoConfig `mapstructure:"coingecko"`
}

type DojiConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	APIURL  string `mapstructure:"api_url"`
}

type BTMCConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	APIURL  string `mapstructure:"api_url"`
}

type MihongConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	BaseURL string `mapstructure:"base_url"`
	APIURL  string `mapstructure:"api_url"`
}

type KimKhoaConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	URL     string `mapstructure:"url"`
}

type CoinGeckoConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	APIURL  string `mapstructure:"api_url"`
}

type TelegramConfig struct {
	Enabled       bool   `mapstructure:"enabled"`
	BotToken      string `mapstructure:"bot_token"`
	ChannelID     string `mapstructure:"channel_id"`
	WebhookSecret string `mapstructure:"webhook_secret"`
	WebhookPath   string `mapstructure:"webhook_path"`
}

type ZaloConfig struct {
	Enabled       bool   `mapstructure:"enabled"`
	BotToken      string `mapstructure:"bot_token"`
	WebhookSecret string `mapstructure:"webhook_secret"`
	WebhookPath   string `mapstructure:"webhook_path"`
	// PrivateToolChats: comma-separated chat ids allowed to use the due_dates
	// tool (private data). Empty disables it.
	PrivateToolChats string `mapstructure:"private_tool_chats"`
}

// LLMOpenAIConfig points the Zalo bot's AI work (chat + reminder parsing) at an
// OpenAI-compatible /v1/chat/completions endpoint. Unused unless both
// base_url and api_key are set.
type LLMOpenAIConfig struct {
	BaseURL string `mapstructure:"base_url"`
	APIKey  string `mapstructure:"api_key"`
	Model   string `mapstructure:"model"`
}

type AppConfig struct {
	Name        string `mapstructure:"name"`
	Environment string `mapstructure:"environment"`
	LogLevel    string `mapstructure:"log_level"`
}

type ReminderConfig struct {
	Enabled          bool   `mapstructure:"enabled"`
	DBPath           string `mapstructure:"db_path"`
	OpenAIAPIKey     string `mapstructure:"openai_api_key"`
	OpenAIModel      string `mapstructure:"openai_model"`
	RetentionDays    int    `mapstructure:"retention_days"`
	MaxLookaheadDays int    `mapstructure:"max_lookahead_days"`
}

type ServerConfig struct {
	ListenAddr string `mapstructure:"listen_addr"`
}

type DueDateConfig struct {
	Enabled   bool   `mapstructure:"enabled"`
	CSVURL    string `mapstructure:"csv_url"`
	StatePath string `mapstructure:"state_path"`
}

// AdminConfig enables the read-only admin UI at /admin. Both Username and
// PasswordHash (bcrypt, cost >= 10) must be set; there is no default password.
type AdminConfig struct {
	Username        string `mapstructure:"username"`
	PasswordHash    string `mapstructure:"password_hash"`
	PasswordHashB64 string `mapstructure:"password_hash_b64"`
	SessionSecret   string `mapstructure:"session_secret"`
	// PasswordReset (ADMIN_PASSWORD_RESET=true) clears the UI-changed password at
	// startup so the env hash applies again.
	PasswordReset bool `mapstructure:"password_reset"`
}

// ConvoConfig controls the Zalo conversation store used by the admin UI.
type ConvoConfig struct {
	Enabled              bool    `mapstructure:"enabled"`
	DBPath               string  `mapstructure:"db_path"`
	RetentionDays        int     `mapstructure:"retention_days"`
	PricePromptPer1K     float64 `mapstructure:"price_prompt_per_1k"`
	PriceCompletionPer1K float64 `mapstructure:"price_completion_per_1k"`
	// Prices are per-model rates per 1M tokens (also CONVO_PRICES, see docs/admin.md).
	Prices   []ModelPrice `mapstructure:"prices"`
	Currency string       `mapstructure:"currency"`
}

// ModelPrice is one model's rate per 1,000,000 tokens.
type ModelPrice struct {
	Model           string  `mapstructure:"model"`
	PromptPer1M     float64 `mapstructure:"prompt_per_1m"`
	CompletionPer1M float64 `mapstructure:"completion_per_1m"`
}

type ChatConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	APIKey  string `mapstructure:"api_key"`
	Model   string `mapstructure:"model"`
}

type NewsConfig struct {
	Enabled       bool     `mapstructure:"enabled"`
	DBPath        string   `mapstructure:"db_path"`
	OpenAIAPIKey  string   `mapstructure:"openai_api_key"`
	OpenAIModel   string   `mapstructure:"openai_model"`
	Feeds         []string `mapstructure:"feeds"`
	SearchQueries []string `mapstructure:"search_queries"`
	SerperAPIKey  string   `mapstructure:"serper_api_key"`
	MaxItems      int      `mapstructure:"max_items"`
	WindowHours   int      `mapstructure:"window_hours"`
	DedupDays     int      `mapstructure:"dedup_days"`
}
