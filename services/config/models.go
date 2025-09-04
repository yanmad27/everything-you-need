package config

type PriceSourcesConfig struct {
	Doji     DojiConfig     `mapstructure:"doji"`
	BTMC     BTMCConfig     `mapstructure:"btmc"`
}

type DojiConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	APIURL  string `mapstructure:"api_url"`
}

type BTMCConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	APIURL  string `mapstructure:"api_url"`
}

type TelegramConfig struct {
	Enabled   bool   `mapstructure:"enabled"`
	BotToken  string `mapstructure:"bot_token"`
	ChannelID string `mapstructure:"channel_id"`
}

type AppConfig struct {
	Name        string `mapstructure:"name"`
	Environment string `mapstructure:"environment"`
	LogLevel    string `mapstructure:"log_level"`
}
