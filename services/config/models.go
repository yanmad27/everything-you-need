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
	Enabled   bool   `mapstructure:"enabled"`
	BotToken  string `mapstructure:"bot_token"`
	ChannelID string `mapstructure:"channel_id"`
}

type AppConfig struct {
	Name        string `mapstructure:"name"`
	Environment string `mapstructure:"environment"`
	LogLevel    string `mapstructure:"log_level"`
}

type UserManagementConfig struct {
	Enabled           bool   `mapstructure:"enabled"`
	HTTPPort          string `mapstructure:"http_port"`
	JWTSecret         string `mapstructure:"jwt_secret"`
	JWTExpirationHours int   `mapstructure:"jwt_expiration_hours"`
	DefaultAdminUsername string `mapstructure:"default_admin_username"`
	DefaultAdminEmail    string `mapstructure:"default_admin_email"`
	DefaultAdminPassword string `mapstructure:"default_admin_password"`
}
