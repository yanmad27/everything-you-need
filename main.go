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
	jobscheduler "everything-you-need/m/services/job-scheduler"
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

	scheduler := jobscheduler.NewJobScheduler()
	registerJobs(scheduler, priceService, teleService, reminderService, cfg)

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
	if cfg.Reminder.GeminiAPIKey == "" {
		log.Printf("Reminder service disabled: reminder.gemini_api_key is empty")
		return nil, nil
	}

	store, err := reminder.OpenStore(cfg.Reminder.DBPath)
	if err != nil {
		log.Printf("Reminder store open failed: %v (reminder service disabled)", err)
		return nil, nil
	}

	parser := reminder.NewGeminiParser(cfg.Reminder.GeminiAPIKey, cfg.Reminder.GeminiModel, 0)
	svc := reminder.NewService(store, parser, teleService, time.Now)
	log.Printf("Reminder service initialized (db=%s, model=%s)", cfg.Reminder.DBPath, cfg.Reminder.GeminiModel)
	return svc, store
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
