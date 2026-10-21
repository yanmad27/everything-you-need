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
	// Feed list must come from the file (cannot be env vars). search_queries is
	// intentionally empty now (topic filter off).
	if len(cfg.News.Feeds) == 0 {
		t.Errorf("feeds=%d, want the general feed list from file", len(cfg.News.Feeds))
	}
	// Non-secret file value preserved.
	if cfg.Telegram.ChannelID != "-1002923319232" {
		t.Errorf("channel_id: want file value, got %q", cfg.Telegram.ChannelID)
	}
}

// Zalo is off by default and picks up its secrets from env vars.
func TestDockerConfigZaloEnv(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "config.docker.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), src, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	cfg := LoadConfig()
	if cfg == nil || cfg.Zalo.Enabled || cfg.Zalo.WebhookPath != "/zalo-webhook" {
		t.Fatalf("zalo defaults: %+v", cfg)
	}

	t.Setenv("ZALO_ENABLED", "true")
	t.Setenv("ZALO_BOT_TOKEN", "zalo-tok")
	t.Setenv("ZALO_WEBHOOK_SECRET", "zalo-sec")
	cfg = LoadConfig()
	if !cfg.Zalo.Enabled || cfg.Zalo.BotToken != "zalo-tok" || cfg.Zalo.WebhookSecret != "zalo-sec" {
		t.Errorf("zalo env override: %+v", cfg.Zalo)
	}
}

// docker-compose passes every new variable even when Dokploy leaves it unset
// (substituted as ""); that must mean "defaults, Zalo disabled", not an error
// and not an empty string clobbering a default.
func TestDockerConfigNewEnvVarsEmpty(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "config.docker.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), src, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	for _, k := range []string{"ZALO_ENABLED", "ZALO_BOT_TOKEN", "ZALO_WEBHOOK_SECRET", "ZALO_WEBHOOK_PATH",
		"LLM_OPENAI_BASE_URL", "LLM_OPENAI_API_KEY", "LLM_OPENAI_MODEL"} {
		t.Setenv(k, "")
	}

	cfg := LoadConfig()
	if cfg == nil {
		t.Fatal("LoadConfig returned nil with empty Zalo/LLM env vars")
	}
	if cfg.Zalo.Enabled || cfg.Zalo.BotToken != "" || cfg.Zalo.WebhookSecret != "" {
		t.Errorf("zalo must be disabled/empty: %+v", cfg.Zalo)
	}
	if cfg.Zalo.WebhookPath != "/zalo-webhook" {
		t.Errorf("webhook_path clobbered: %q", cfg.Zalo.WebhookPath)
	}
	if cfg.LLMOpenAI.Model == "" || cfg.LLMOpenAI.BaseURL != "" || cfg.LLMOpenAI.APIKey != "" {
		t.Errorf("llm_openai: %+v", cfg.LLMOpenAI)
	}
}
