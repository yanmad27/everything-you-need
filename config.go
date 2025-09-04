package main

import (
	"fmt"
	"log"
	"strings"

	"github.com/spf13/viper"
)

// Config holds all configuration for the application
type Config struct {
	// Price sources configuration
	PriceSources PriceSourcesConfig `mapstructure:"price_sources"`
	
	// Telegram configuration
	Telegram TelegramConfig `mapstructure:"telegram"`
	
	// Application settings
	App AppConfig `mapstructure:"app"`
}

// PriceSourcesConfig holds configuration for all price sources
type PriceSourcesConfig struct {
	Doji DojiConfig `mapstructure:"doji"`
}

// DojiConfig holds Doji-specific configuration
type DojiConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	APIKey  string `mapstructure:"api_key"`
}

// TelegramConfig holds Telegram-specific configuration
type TelegramConfig struct {
	Enabled   bool   `mapstructure:"enabled"`
	BotToken  string `mapstructure:"bot_token"`
	ChannelID string `mapstructure:"channel_id"`
}

// AppConfig holds general application configuration
type AppConfig struct {
	Name        string `mapstructure:"name"`
	Environment string `mapstructure:"environment"`
	LogLevel    string `mapstructure:"log_level"`
}

// LoadConfig loads configuration from file, environment variables, and defaults
func LoadConfig() (*Config, error) {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	viper.AddConfigPath("./config")
	viper.AddConfigPath("/etc/everything-you-need")
	
	// Set default values
	setDefaults()
	
	// Enable reading from environment variables
	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	
	// Read config file
	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("failed to read config file: %w", err)
		}
		log.Println("No config file found, using defaults and environment variables")
	} else {
		log.Printf("Using config file: %s", viper.ConfigFileUsed())
	}
	
	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}
	
	return &config, nil
}

// setDefaults sets default configuration values
func setDefaults() {
	// App defaults
	viper.SetDefault("app.name", "Everything You Need")
	viper.SetDefault("app.environment", "development")
	viper.SetDefault("app.log_level", "info")
	
	// Price sources defaults
	viper.SetDefault("price_sources.doji.enabled", true)
	viper.SetDefault("price_sources.doji.api_key", "")
	
	// Telegram defaults
	viper.SetDefault("telegram.enabled", false)
	viper.SetDefault("telegram.bot_token", "")
	viper.SetDefault("telegram.channel_id", "")
}