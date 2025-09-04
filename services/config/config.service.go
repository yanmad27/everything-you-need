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

	viper.SetDefault("telegram.enabled", false)
	viper.SetDefault("telegram.bot_token", "")
	viper.SetDefault("telegram.channel_id", "")
	viper.SetDefault("telegram.timeout_seconds", 30)
}
