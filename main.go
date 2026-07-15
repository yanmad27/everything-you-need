package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"everything-you-need/m/services/config"
	"everything-you-need/m/services/duedate"
	jobscheduler "everything-you-need/m/services/job-scheduler"
	"everything-you-need/m/services/news"
	pricetracker "everything-you-need/m/services/price-tracker"
	"everything-you-need/m/services/reminder"
	telebot "everything-you-need/m/services/tele-bot"
)

func main() {
	cfg := config.ConfigMod.Resolve()

	log.Printf("=== %s ===\n", cfg.App.Name)
	log.Printf("Environment: %s\n", cfg.App.Environment)
	log.Printf("\n")

	priceService := pricetracker.PriceTrackerServiceMod.Resolve()
	teleService := telebot.TeleBotServiceMod.Resolve()

	reminderService, reminderStore := buildReminderService(cfg, teleService)
	if reminderStore != nil {
		defer reminderStore.Close()
	}

	newsService, newsStore := buildNewsService(cfg, teleService)
	if newsStore != nil {
		defer newsStore.Close()
	}

	dueDateService := buildDueDateService(cfg)

	scheduler := jobscheduler.NewJobScheduler()
	registerJobs(scheduler, priceService, teleService, reminderService, newsService, dueDateService, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	webhookServer := startWebhookServer(cfg, reminderService)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	scheduler.Start(ctx)

	log.Printf("\n🚀 Job scheduler started. Press Ctrl+C to stop.")

	<-sigChan
	log.Printf("\n📴 Shutting down...")

	if webhookServer != nil {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = webhookServer.Shutdown(shutdownCtx)
		shutdownCancel()
	}
	scheduler.Stop()
	cancel()

	log.Printf("✅ Shutdown complete")
}

func buildReminderService(cfg *config.Config, teleService *telebot.TeleBotService) (*reminder.Service, *reminder.Store) {
	if !cfg.Reminder.Enabled {
		log.Printf("Reminder service disabled in config")
		return nil, nil
	}
	if teleService == nil {
		log.Printf("Reminder service disabled: telegram service not available")
		return nil, nil
	}
	if cfg.Reminder.OpenAIAPIKey == "" {
		log.Printf("Reminder service disabled: reminder.openai_api_key is empty")
		return nil, nil
	}

	store, err := reminder.OpenStore(cfg.Reminder.DBPath)
	if err != nil {
		log.Printf("Reminder store open failed: %v (reminder service disabled)", err)
		return nil, nil
	}

	parser := reminder.NewOpenAIParser(cfg.Reminder.OpenAIAPIKey, cfg.Reminder.OpenAIModel, 0)
	svc := reminder.NewService(store, parser, teleService, time.Now)
	log.Printf("Reminder service initialized (db=%s, model=%s)", cfg.Reminder.DBPath, cfg.Reminder.OpenAIModel)
	return svc, store
}

func buildNewsService(cfg *config.Config, teleService *telebot.TeleBotService) (*news.Service, *news.Store) {
	if !cfg.News.Enabled {
		log.Printf("News service disabled in config")
		return nil, nil
	}
	if teleService == nil || cfg.Telegram.ChannelID == "" {
		log.Printf("News service disabled: telegram service or channel not available")
		return nil, nil
	}

	apiKey := cfg.News.OpenAIAPIKey
	if apiKey == "" {
		apiKey = cfg.Reminder.OpenAIAPIKey // reuse the reminder bot's key
	}
	if apiKey == "" {
		log.Printf("News service disabled: no openai api key (news.openai_api_key / reminder.openai_api_key empty)")
		return nil, nil
	}

	store, err := news.OpenStore(cfg.News.DBPath)
	if err != nil {
		log.Printf("News store open failed: %v (news service disabled)", err)
		return nil, nil
	}

	ranker := news.NewRanker(apiKey, cfg.News.OpenAIModel, 0)
	var searcher *news.Searcher
	if cfg.News.SerperAPIKey != "" && len(cfg.News.SearchQueries) > 0 {
		searcher = news.NewSearcher(cfg.News.SerperAPIKey, cfg.News.SearchQueries)
		log.Printf("News searcher initialized (serper, queries=%d)", len(cfg.News.SearchQueries))
	}
	svc := news.NewService(store, ranker, searcher, cfg.News.Feeds, cfg.News.MaxItems, cfg.News.WindowHours, cfg.News.DedupDays, time.Now)
	log.Printf("News service initialized (db=%s, feeds=%d, model=%s)", cfg.News.DBPath, len(cfg.News.Feeds), cfg.News.OpenAIModel)
	return svc, store
}

func buildDueDateService(cfg *config.Config) *duedate.Service {
	if !cfg.DueDate.Enabled {
		log.Printf("Due-date reminder service disabled in config")
		return nil
	}
	if cfg.DueDate.CSVURL == "" {
		log.Printf("Due-date reminder service disabled: duedate.csv_url is empty")
		return nil
	}
	log.Printf("Due-date reminder service initialized (state=%s)", cfg.DueDate.StatePath)
	return duedate.NewService(cfg.DueDate.CSVURL, cfg.DueDate.StatePath, time.Now)
}

func startWebhookServer(cfg *config.Config, svc *reminder.Service) *http.Server {
	if svc == nil {
		return nil
	}
	if cfg.Telegram.WebhookSecret == "" {
		log.Printf("Webhook server disabled: telegram.webhook_secret is empty")
		return nil
	}

	path := cfg.Telegram.WebhookPath
	if path == "" {
		path = "/telegram-webhook"
	}
	addr := cfg.Server.ListenAddr
	if addr == "" {
		addr = ":8080"
	}

	dispatcher := func(ctx context.Context, update telebot.Update) {
		msg := update.AnyMessage()
		if msg == nil || msg.Text == "" {
			return
		}
		incoming := toIncomingMessage(msg)
		if err := svc.HandleIncoming(ctx, incoming); err != nil {
			log.Printf("webhook dispatch error: %v", err)
		}
	}

	mux := http.NewServeMux()
	mux.Handle(path, telebot.NewWebhookHandler(cfg.Telegram.WebhookSecret, dispatcher))

	server := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}

	go func() {
		log.Printf("🌐 Webhook server listening on %s%s", addr, path)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("Webhook server error: %v", err)
		}
	}()

	return server
}

func toIncomingMessage(msg *telebot.Message) reminder.IncomingMessage {
	userName := ""
	userID := ""
	if msg.From != nil {
		userName = msg.From.DisplayName()
		userID = strconv.FormatInt(msg.From.ID, 10)
	}
	return reminder.IncomingMessage{
		ChatID:    strconv.FormatInt(msg.Chat.ID, 10),
		UserID:    userID,
		UserName:  userName,
		MessageID: msg.MessageID,
		Text:      msg.Text,
	}
}
