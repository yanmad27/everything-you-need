package config

import (
	"os"
	"path/filepath"
	"testing"
)

// Guards the Dokploy scheme: config.docker.yaml carries the non-secret config
// (incl. list values), and env vars override the blanked secrets at runtime.
func TestDockerConfigEnvOverride(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "config.docker.yaml"))
	if err != nil {
		t.Fatalf("read config.docker.yaml: %v", err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), src, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	t.Setenv("TELEGRAM_BOT_TOKEN", "tok-from-env")
	t.Setenv("NEWS_SERPER_API_KEY", "serper-from-env")
	t.Setenv("PRICE_SOURCES_BTMC_API_URL", "http://btmc.example/from-env")

	cfg := LoadConfig()
	if cfg == nil {
		t.Fatal("LoadConfig returned nil")
	}

	if cfg.Telegram.BotToken != "tok-from-env" {
		t.Errorf("bot_token: want env override, got %q", cfg.Telegram.BotToken)
	}
	if cfg.News.SerperAPIKey != "serper-from-env" {
		t.Errorf("serper_api_key: want env override, got %q", cfg.News.SerperAPIKey)
	}
	if cfg.PriceSources.BTMC.APIURL != "http://btmc.example/from-env" {
		t.Errorf("btmc api_url: want env override, got %q", cfg.PriceSources.BTMC.APIURL)
	}
	// Lists must come from the file (cannot be env vars).
	if len(cfg.News.Feeds) == 0 || len(cfg.News.SearchQueries) == 0 {
		t.Errorf("feeds=%d search_queries=%d, want both from file", len(cfg.News.Feeds), len(cfg.News.SearchQueries))
	}
	// Non-secret file value preserved.
	if cfg.Telegram.ChannelID != "-1002923319232" {
		t.Errorf("channel_id: want file value, got %q", cfg.Telegram.ChannelID)
	}
}
