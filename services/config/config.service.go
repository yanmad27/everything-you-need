package config

import (
	"log"
	"strings"

	"github.com/spf13/viper"
	"github.com/submodule-org/submodule.go/v2"
)

type Config struct {
	PriceSources PriceSourcesConfig `mapstructure:"price_sources"`
	Telegram     TelegramConfig     `mapstructure:"telegram"`
	App          AppConfig          `mapstructure:"app"`
	Reminder     ReminderConfig     `mapstructure:"reminder"`
	Server       ServerConfig       `mapstructure:"server"`
	News         NewsConfig         `mapstructure:"news"`
}

var ConfigMod = submodule.Make[*Config](LoadConfig)

func LoadConfig() *Config {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	viper.AddConfigPath("./config")
	viper.AddConfigPath("/etc/everything-you-need")

	setDefaults()

	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil
		}
		log.Println("No config file found, using defaults and environment variables")
	} else {
		log.Printf("Using config file: %s", viper.ConfigFileUsed())
	}

	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		return nil
	}

	return &config
}

func setDefaults() {
	viper.SetDefault("app.name", "Everything You Need")
	viper.SetDefault("app.environment", "development")
	viper.SetDefault("app.log_level", "info")

	viper.SetDefault("price_sources.doji.enabled", true)
	viper.SetDefault("price_sources.doji.api_key", "")

	viper.SetDefault("price_sources.vietgold.enabled", false)
	viper.SetDefault("price_sources.vietgold.api_url", "")

	viper.SetDefault("price_sources.coingecko.enabled", false)
	viper.SetDefault("price_sources.coingecko.api_url", "")

	viper.SetDefault("telegram.enabled", false)
	viper.SetDefault("telegram.bot_token", "")
	viper.SetDefault("telegram.channel_id", "")
	viper.SetDefault("telegram.timeout_seconds", 30)
	viper.SetDefault("telegram.webhook_secret", "")
	viper.SetDefault("telegram.webhook_path", "/telegram-webhook")

	viper.SetDefault("reminder.enabled", false)
	viper.SetDefault("reminder.db_path", "data/reminders.db")
	viper.SetDefault("reminder.llm_provider", "gemini")
	viper.SetDefault("reminder.gemini_api_key", "")
	viper.SetDefault("reminder.gemini_model", "gemini-2.5-flash")
	viper.SetDefault("reminder.retention_days", 30)
	viper.SetDefault("reminder.max_lookahead_days", 365)

	viper.SetDefault("server.listen_addr", ":8080")

	viper.SetDefault("news.enabled", true)
	viper.SetDefault("news.db_path", "data/news.db")
	viper.SetDefault("news.gemini_api_key", "")
	viper.SetDefault("news.gemini_model", "gemini-2.5-flash")
	viper.SetDefault("news.max_items", 7)
	viper.SetDefault("news.window_hours", 24)
	viper.SetDefault("news.dedup_days", 7)
	viper.SetDefault("news.feeds", []string{
		"https://news.ycombinator.com/rss",
		"https://techcrunch.com/feed/",
		"https://www.theverge.com/rss/index.xml",
		"http://feeds.arstechnica.com/arstechnica/index",
		"https://vnexpress.net/rss/so-hoa.rss",
		"https://vnexpress.net/rss/tin-moi-nhat.rss",
	})
}
