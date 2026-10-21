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

	"everything-you-need/m/services/chat"
	"everything-you-need/m/services/config"
	"everything-you-need/m/services/duedate"
	jobscheduler "everything-you-need/m/services/job-scheduler"
	"everything-you-need/m/services/news"
	pricetracker "everything-you-need/m/services/price-tracker"
	"everything-you-need/m/services/reminder"
	telebot "everything-you-need/m/services/tele-bot"
	zalobot "everything-you-need/m/services/zalo-bot"
)

func main() {
	cfg := config.ConfigMod.Resolve()

	log.Printf("=== %s ===\n", cfg.App.Name)
	log.Printf("Environment: %s\n", cfg.App.Environment)
	log.Printf("\n")

	priceService := pricetracker.PriceTrackerServiceMod.Resolve()
	teleService := telebot.TeleBotServiceMod.Resolve()
	zaloService := zalobot.ZaloBotServiceMod.Resolve()

	reminderService, reminderStore := buildReminderService(cfg, teleService, zaloService)
	if reminderStore != nil {
		defer reminderStore.Close()
	}

	newsService, newsStore := buildNewsService(cfg, teleService)
	if newsStore != nil {
		defer newsStore.Close()
	}

	dueDateService := buildDueDateService(cfg)
	var teleChat, zaloChat *chat.Service
	if teleService != nil {
		teleChat = buildChatService(cfg, teleService)
	}
	if zaloService != nil {
		zaloChat = buildChatService(cfg, zaloService)
	}

	scheduler := jobscheduler.NewJobScheduler()
	registerJobs(scheduler, priceService, teleService, reminderService, newsService, dueDateService, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	webhookServer := startWebhookServer(cfg, reminderService, teleService, teleChat, zaloService, zaloChat)

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

func buildReminderService(cfg *config.Config, teleService *telebot.TeleBotService, zaloService *zalobot.ZaloBotService) (*reminder.Service, *reminder.Store) {
	if !cfg.Reminder.Enabled {
		log.Printf("Reminder service disabled in config")
		return nil, nil
	}
	if teleService == nil && zaloService == nil {
		log.Printf("Reminder service disabled: neither telegram nor zalo service available")
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
	var defaultSender reminder.Sender // Telegram; stays a nil interface when unconfigured
	if teleService != nil {
		defaultSender = teleService
	}
	svc := reminder.NewService(store, parser, defaultSender, time.Now)
	if zaloService != nil {
		svc.WithSender(reminder.PlatformZalo, zaloService)
	}
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

// buildChatService builds one chat assistant bound to one platform's sender
// (history is kept per assistant, so platforms never share context).
func buildChatService(cfg *config.Config, sender chat.Sender) *chat.Service {
	if !cfg.Chat.Enabled {
		log.Printf("Chat service disabled in config")
		return nil
	}
	apiKey := cfg.Chat.APIKey
	if apiKey == "" {
		apiKey = cfg.Reminder.OpenAIAPIKey // reuse the reminder bot's LLM key
	}
	if apiKey == "" {
		apiKey = cfg.News.OpenAIAPIKey
	}
	if apiKey == "" {
		log.Printf("Chat service disabled: no LLM api key (chat/reminder/news api key empty)")
		return nil
	}
	log.Printf("Chat service initialized (model=%s)", cfg.Chat.Model)
	return chat.NewService(apiKey, cfg.Chat.Model, sender)
}

func startWebhookServer(cfg *config.Config, svc *reminder.Service,
	teleService *telebot.TeleBotService, teleChat *chat.Service,
	zaloService *zalobot.ZaloBotService, zaloChat *chat.Service) *http.Server {
	mux := http.NewServeMux()
	mounted := 0

	if teleService != nil && (svc != nil || teleChat != nil) {
		if cfg.Telegram.WebhookSecret == "" {
			log.Printf("Telegram webhook disabled: telegram.webhook_secret is empty")
		} else {
			path := cfg.Telegram.WebhookPath
			if path == "" {
				path = "/telegram-webhook"
			}
			dispatcher := func(ctx context.Context, update telebot.Update) {
				msg := update.AnyMessage()
				if msg == nil || msg.Text == "" {
					return
				}
				dispatchIncoming(ctx, svc, teleChat, toIncomingMessage(msg))
			}
			mux.Handle(path, telebot.NewWebhookHandler(cfg.Telegram.WebhookSecret, dispatcher))
			log.Printf("Telegram webhook mounted at %s", path)
			mounted++
		}
	}

	if zaloService != nil && (svc != nil || zaloChat != nil) {
		if cfg.Zalo.WebhookSecret == "" {
			log.Printf("Zalo webhook disabled: zalo.webhook_secret is empty")
		} else {
			path := cfg.Zalo.WebhookPath
			if path == "" {
				path = "/zalo-webhook"
			}
			dispatcher := func(ctx context.Context, msg zalobot.Message) {
				dispatchIncoming(ctx, svc, zaloChat, toZaloIncomingMessage(msg))
			}
			mux.Handle(path, zalobot.NewWebhookHandler(cfg.Zalo.WebhookSecret, dispatcher))
			log.Printf("Zalo webhook mounted at %s", path)
			mounted++
		}
	}

	if mounted == 0 {
		return nil
	}

	addr := cfg.Server.ListenAddr
	if addr == "" {
		addr = ":8080"
	}
	server := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}

	go func() {
		log.Printf("🌐 Webhook server listening on %s", addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("Webhook server error: %v", err)
		}
	}()

	return server
}

// dispatchIncoming routes a normalized message: reminders take priority; chat
// handles wake-word messages that aren't reminder commands.
func dispatchIncoming(ctx context.Context, svc *reminder.Service, chatSvc *chat.Service, incoming reminder.IncomingMessage) {
	if svc != nil && svc.Matches(incoming.Text) {
		if err := svc.HandleIncoming(ctx, incoming); err != nil {
			log.Printf("webhook dispatch error (%s): %v", incoming.Platform, err)
		}
		return
	}
	if chatSvc != nil {
		if _, ok := chat.SplitWake(incoming.Text); ok {
			if err := chatSvc.Handle(ctx, incoming.ChatID, incoming.MessageID, incoming.Text); err != nil {
				log.Printf("chat dispatch error (%s): %v", incoming.Platform, err)
			}
		}
	}
}

func toZaloIncomingMessage(msg zalobot.Message) reminder.IncomingMessage {
	return reminder.IncomingMessage{
		Platform: reminder.PlatformZalo,
		ChatID:   msg.Chat.ID,
		UserID:   msg.From.ID,
		UserName: msg.From.DisplayName,
		Text:     msg.Text,
	}
}

func toIncomingMessage(msg *telebot.Message) reminder.IncomingMessage {
	userName := ""
	userID := ""
	if msg.From != nil {
		userName = msg.From.DisplayName()
		userID = strconv.FormatInt(msg.From.ID, 10)
	}
	return reminder.IncomingMessage{
		Platform:  reminder.PlatformTelegram,
		ChatID:    strconv.FormatInt(msg.Chat.ID, 10),
		UserID:    userID,
		UserName:  userName,
		MessageID: msg.MessageID,
		Text:      msg.Text,
	}
}
