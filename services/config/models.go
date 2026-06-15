package config

type PriceSourcesConfig struct {
	Doji      DojiConfig      `mapstructure:"doji"`
	BTMC      BTMCConfig      `mapstructure:"btmc"`
	Mihong    MihongConfig    `mapstructure:"mihong"`
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

type AppConfig struct {
	Name        string `mapstructure:"name"`
	Environment string `mapstructure:"environment"`
	LogLevel    string `mapstructure:"log_level"`
}

type ReminderConfig struct {
	Enabled          bool   `mapstructure:"enabled"`
	DBPath           string `mapstructure:"db_path"`
	LLMProvider      string `mapstructure:"llm_provider"`
	GeminiAPIKey     string `mapstructure:"gemini_api_key"`
	GeminiModel      string `mapstructure:"gemini_model"`
	RetentionDays    int    `mapstructure:"retention_days"`
	MaxLookaheadDays int    `mapstructure:"max_lookahead_days"`
}

type ServerConfig struct {
	ListenAddr string `mapstructure:"listen_addr"`
}

type NewsConfig struct {
	Enabled      bool     `mapstructure:"enabled"`
	DBPath       string   `mapstructure:"db_path"`
	GeminiAPIKey string   `mapstructure:"gemini_api_key"`
	GeminiModel  string   `mapstructure:"gemini_model"`
	Feeds        []string `mapstructure:"feeds"`
	MaxItems     int      `mapstructure:"max_items"`
	WindowHours  int      `mapstructure:"window_hours"`
	DedupDays    int      `mapstructure:"dedup_days"`
}
