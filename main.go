package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"everything-you-need/m/services/admin"
	"everything-you-need/m/services/chat"
	"everything-you-need/m/services/config"
	"everything-you-need/m/services/convo"
	"everything-you-need/m/services/duedate"
	jobscheduler "everything-you-need/m/services/job-scheduler"
	"everything-you-need/m/services/llm"
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

	zaloCompleter := buildZaloCompleter(cfg)
	reminderService, reminderStore := buildReminderService(cfg, teleService, zaloService, zaloCompleter)
	if reminderStore != nil {
		defer reminderStore.Close()
	}

	newsService, newsStore := buildNewsService(cfg, teleService)
	if newsStore != nil {
		defer newsStore.Close()
	}

	dueDateService := buildDueDateService(cfg)
	var convoStore *convo.Store
	var teleChat, zaloChat *chat.Service
	if teleService != nil {
		teleChat = buildChatService(cfg, teleService, nil, false)
	}
	if zaloService != nil {
		zaloChat = buildChatService(cfg, zaloService, zaloCompleter, true)
		var zaloRem *reminder.Service
		if _, zOn := reminderPlatforms(cfg, teleService != nil, true, zaloCompleter != nil); zOn {
			zaloRem = reminderService
		}
		zaloTools := buildZaloTools(cfg, priceService, newsStore, dueDateService, zaloRem)
		if convoStore = buildConvoStore(cfg); convoStore != nil {
			defer convoStore.Close()
			zaloTools.Recorder = convoStore
			zaloTools.Model = zaloModel(cfg, zaloCompleter)
		}
		enableZaloTools(zaloChat, zaloCompleter, zaloTools)
	}

	scheduler := jobscheduler.NewJobScheduler()
	registerJobs(scheduler, priceService, teleService, reminderService, newsService, dueDateService, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	teleOn, zaloOn := reminderPlatforms(cfg, teleService != nil, zaloService != nil, zaloCompleter != nil)
	var teleSvc, zaloSvc *reminder.Service
	if teleOn {
		teleSvc = reminderService
	}
	if zaloOn {
		zaloSvc = reminderService
	}
	webhookServer := startWebhookServer(cfg, teleSvc, teleService, teleChat, zaloSvc, zaloService, zaloChat, adminMount(cfg, convoStore))

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

func buildReminderService(cfg *config.Config, teleService *telebot.TeleBotService, zaloService *zalobot.ZaloBotService, zaloLLM llm.Completer) (*reminder.Service, *reminder.Store) {
	if !cfg.Reminder.Enabled {
		log.Printf("Reminder service disabled in config")
		return nil, nil
	}
	teleOn, zaloOn := reminderPlatforms(cfg, teleService != nil, zaloService != nil, zaloLLM != nil)
	if !teleOn && !zaloOn {
		log.Printf("Reminder service disabled: no platform has a bot and an LLM key (telegram needs reminder.openai_api_key; zalo needs llm_openai or reminder.openai_api_key)")
		return nil, nil
	}

	store, err := reminder.OpenStore(cfg.Reminder.DBPath)
	if err != nil {
		log.Printf("Reminder store open failed: %v (reminder service disabled)", err)
		return nil, nil
	}

	var teleSender, zaloSender reminder.Sender // stay nil interfaces when unconfigured
	if teleOn {                                // no Telegram sender => Telegram reminders stay off, exactly as before
		teleSender = teleService
	}
	if zaloOn {
		zaloSender = zaloService
	}
	svc := newReminderService(cfg, store, teleSender, zaloSender, zaloLLM)
	if teleOn {
		log.Printf("Reminder service: telegram parser model=%s", cfg.Reminder.OpenAIModel)
	}
	if zaloOn {
		if zaloLLM != nil {
			log.Printf("Reminder service: zalo parser uses OpenAI-compatible provider (model=%s)", cfg.LLMOpenAI.Model)
		} else {
			log.Printf("Reminder service: zalo parser falls back to Anthropic (model=%s)", cfg.Reminder.OpenAIModel)
		}
	}
	log.Printf("Reminder service initialized (db=%s)", cfg.Reminder.DBPath)
	return svc, store
}

// reminderPlatforms says which platforms get reminders. Telegram keeps its
// historical requirement (reminder.openai_api_key); Zalo needs either its own
// OpenAI-compatible provider or that same Anthropic key as fallback.
func reminderPlatforms(cfg *config.Config, haveTele, haveZalo, haveZaloLLM bool) (teleOn, zaloOn bool) {
	hasKey := cfg.Reminder.OpenAIAPIKey != ""
	return haveTele && hasKey, haveZalo && (haveZaloLLM || hasKey)
}

// newReminderService wires the Telegram path (Anthropic parser, default) and the
// optional Zalo path (own sender; own parser when an OpenAI-compatible provider is set).
func newReminderService(cfg *config.Config, store *reminder.Store, tele, zalo reminder.Sender, zaloLLM llm.Completer) *reminder.Service {
	parser := reminder.NewOpenAIParser(cfg.Reminder.OpenAIAPIKey, cfg.Reminder.OpenAIModel, 0)
	svc := reminder.NewService(store, parser, tele, time.Now)
	if zalo != nil {
		svc.WithSender(reminder.PlatformZalo, zalo)
		if zaloLLM != nil {
			svc.WithParser(reminder.PlatformZalo, reminder.NewLLMParser(zaloLLM))
		}
	}
	return svc
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

// buildZaloCompleter returns the OpenAI-compatible LLM used for Zalo, or nil
// when it is not configured (Zalo then falls back to the Anthropic client).
func buildZaloCompleter(cfg *config.Config) llm.Completer {
	c := cfg.LLMOpenAI
	if c.BaseURL == "" || c.APIKey == "" {
		return nil
	}
	log.Printf("Zalo LLM: OpenAI-compatible provider (model=%s)", c.Model)
	return llm.NewOpenAIClient(c.BaseURL, c.APIKey, c.Model, 0)
}

// buildChatService builds one chat assistant bound to one platform's sender
// (history is kept per assistant, so platforms never share context).
func buildChatService(cfg *config.Config, sender chat.Sender, completer llm.Completer, safeFetch bool) *chat.Service {
	if !cfg.Chat.Enabled {
		log.Printf("Chat service disabled in config")
		return nil
	}
	if completer != nil {
		log.Printf("Chat service initialized (OpenAI-compatible provider)")
		return chat.NewServiceWithCompleter(completer, sender).WithSafeFetch(safeFetch)
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
	return chat.NewService(apiKey, cfg.Chat.Model, sender).WithSafeFetch(safeFetch)
}

// minZaloWebhookSecret is the shortest secret_token Zalo's setWebhook accepts.
const (
	minZaloWebhookSecret = 8
	maxZaloWebhookSecret = 256
)

func startWebhookServer(cfg *config.Config, teleSvc *reminder.Service,
	teleService *telebot.TeleBotService, teleChat *chat.Service,
	zaloSvc *reminder.Service, zaloService *zalobot.ZaloBotService, zaloChat *chat.Service,
	extra ...func(*http.ServeMux) int) *http.Server {
	mux := http.NewServeMux()
	mounted := 0
	for _, m := range extra {
		if m != nil {
			mounted += m(mux)
		}
	}

	if teleService != nil && (teleSvc != nil || teleChat != nil) {
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
				dispatchIncoming(ctx, teleSvc, teleChat, toIncomingMessage(msg), false)
			}
			mux.Handle(path, telebot.NewWebhookHandler(cfg.Telegram.WebhookSecret, dispatcher))
			log.Printf("Telegram webhook mounted at %s", path)
			mounted++
		}
	}

	if zaloService != nil && (zaloSvc != nil || zaloChat != nil) {
		if n := len(cfg.Zalo.WebhookSecret); n < minZaloWebhookSecret || n > maxZaloWebhookSecret {
			log.Printf("Zalo webhook disabled: zalo.webhook_secret must be %d-%d characters (got %d)", minZaloWebhookSecret, maxZaloWebhookSecret, n)
		} else {
			path := cfg.Zalo.WebhookPath
			if path == "" {
				path = "/zalo-webhook"
			}
			var botNames []string
			nameCtx, cancelName := context.WithTimeout(context.Background(), 10*time.Second)
			if names, err := zaloService.BotNames(nameCtx); err != nil {
				log.Printf("Zalo getMe failed (%v); group @mentions fall back to stripping one leading token", err)
			} else {
				botNames = names
			}
			cancelName()
			dispatcher := func(ctx context.Context, msg zalobot.Message) {
				dispatchZaloWith(ctx, zaloSvc, zaloChat, botNames, msg, zaloService.FetchImage)
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
// handles wake-word messages that aren't reminder commands. addressed means the
// platform only delivers messages aimed at the bot (Zalo group @mention/reply),
// so the chat assistant answers without requiring a wake word.
func dispatchIncoming(ctx context.Context, svc *reminder.Service, chatSvc *chat.Service, incoming reminder.IncomingMessage, addressed bool) string {
	if svc != nil && svc.Matches(incoming.Text) {
		if err := svc.HandleIncoming(ctx, incoming); err != nil {
			log.Printf("webhook dispatch error (%s): %v", incoming.Platform, err)
		}
		return "reminder"
	}
	if chatSvc != nil && addressed {
		if err := chatSvc.HandleAddressedAs(ctx, incoming.ChatID, incoming.UserID, incoming.UserName, incoming.MessageID, incoming.Text); err != nil {
			log.Printf("chat dispatch error (%s): %v", incoming.Platform, err)
		}
		return "chat"
	}
	if chatSvc != nil {
		if _, ok := chat.SplitWake(incoming.Text); ok {
			if err := chatSvc.Handle(ctx, incoming.ChatID, incoming.MessageID, incoming.Text); err != nil {
				log.Printf("chat dispatch error (%s): %v", incoming.Platform, err)
			}
			return "chat"
		}
	}
	return "ignored"
}

// dispatchZalo normalizes a Zalo message and routes it. Every Zalo message is
// treated as addressed to the bot: in 1:1 chats by definition, and in groups
// because Zalo only delivers messages that @mention the bot or reply to it (the
// leading mention is stripped before reminder matching and chat). So the chat
// assistant answers without a wake word; reminder commands keep priority.
// Telegram is unaffected (it uses dispatchIncoming with addressed=false).
func dispatchZalo(ctx context.Context, svc *reminder.Service, chatSvc *chat.Service, botNames []string, msg zalobot.Message) {
	dispatchZaloWith(ctx, svc, chatSvc, botNames, msg, nil)
}

// dispatchZaloWith also routes image messages to chat (never to the reminder
// regex service). fetchImage downloads msg.Photo; nil ignores images.
func dispatchZaloWith(ctx context.Context, svc *reminder.Service, chatSvc *chat.Service, botNames []string, msg zalobot.Message, fetchImage func(ctx context.Context, url string) ([]byte, string, error)) {
	ctx = chat.WithMeta(ctx, chat.Meta{ChatType: msg.Chat.ChatType})
	if msg.Kind == "image" {
		dispatchZaloImage(ctx, chatSvc, botNames, msg, fetchImage)
		return
	}
	group := strings.EqualFold(msg.Chat.ChatType, "GROUP")
	if group {
		msg.Text = zalobot.StripLeadingMention(msg.Text, botNames)
		if msg.Text == "" {
			log.Printf("zalo dispatch: result=ignored reason=group_mention_only chat=%s", zalobot.MaskID(msg.Chat.ID))
			return
		}
	}
	result := dispatchIncoming(ctx, svc, chatSvc, toZaloIncomingMessage(msg), true)
	log.Printf("zalo dispatch: result=%s chat_type=%q chat=%s", result, zalobot.SafeField(msg.Chat.ChatType, 40), zalobot.MaskID(msg.Chat.ID))
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

func dispatchZaloImage(ctx context.Context, chatSvc *chat.Service, botNames []string, msg zalobot.Message, fetchImage func(ctx context.Context, url string) ([]byte, string, error)) {
	if chatSvc == nil || fetchImage == nil || msg.Photo == "" {
		log.Printf("zalo dispatch: result=ignored reason=image_unavailable chat=%s", zalobot.MaskID(msg.Chat.ID))
		return
	}
	caption := msg.Caption
	if strings.EqualFold(msg.Chat.ChatType, "GROUP") {
		caption = zalobot.StripLeadingMention(caption, botNames) // empty caption stays addressed
	}
	photo := msg.Photo
	err := chatSvc.HandleImage(ctx, msg.Chat.ID, msg.From.ID, msg.From.DisplayName, 0, caption,
		func(c context.Context) ([]byte, string, error) { return fetchImage(c, photo) })
	if err != nil {
		log.Printf("chat dispatch error (zalo image): %v", err)
	}
	log.Printf("zalo dispatch: result=chat_image chat_type=%q chat=%s", zalobot.SafeField(msg.Chat.ChatType, 40), zalobot.MaskID(msg.Chat.ID))
}

// buildConvoStore opens the Zalo conversation store; a failure disables
// recording only (the bot keeps working).
func buildConvoStore(cfg *config.Config) *convo.Store {
	c := cfg.Convo
	if !c.Enabled {
		return nil
	}
	prices, err := convoPrices(c, os.Getenv("CONVO_PRICES"))
	if err != nil {
		log.Printf("Convo prices ignored: %v", err)
	}
	st, err := convo.Open(c.DBPath, convo.Options{RetentionDays: c.RetentionDays,
		PricePromptPer1K: c.PricePromptPer1K, PriceCompletionPer1K: c.PriceCompletionPer1K,
		Prices: prices, Currency: c.Currency})
	if err != nil {
		log.Printf("Convo store open failed: %v (conversation recording disabled)", err)
		return nil
	}
	log.Printf("Convo store initialized (db=%s, retention_days=%d)", c.DBPath, c.RetentionDays)
	return st
}

// convoPrices merges the config-file prices with CONVO_PRICES
// ("model:prompt:completion,..." per 1M tokens); the env entry wins per model.
// On a malformed env value only the file prices are returned, with the error.
func convoPrices(c config.ConvoConfig, env string) ([]convo.Price, error) {
	var out []convo.Price
	idx := map[string]int{}
	put := func(p convo.Price) {
		k := strings.ToLower(p.Model)
		if i, ok := idx[k]; ok {
			out[i] = p
			return
		}
		idx[k] = len(out)
		out = append(out, p)
	}
	for _, p := range c.Prices {
		if strings.TrimSpace(p.Model) != "" {
			put(convo.Price{Model: strings.TrimSpace(p.Model), PromptPer1M: p.PromptPer1M, CompletionPer1M: p.CompletionPer1M})
		}
	}
	envP, err := convo.ParsePrices(env)
	if err != nil {
		return out, err
	}
	for _, p := range envP {
		put(p)
	}
	return out, nil
}

// adminMount returns a mux mounter for the admin UI, or nil when it is not
// fully configured (credentials + conversation store). No default password.
func adminMount(cfg *config.Config, store *convo.Store) func(*http.ServeMux) int {
	a := cfg.Admin
	if a.Username == "" || (a.PasswordHash == "" && a.PasswordHashB64 == "") {
		log.Printf("Admin UI disabled: admin.username and admin.password_hash (or password_hash_b64) are not both set")
		return nil
	}
	if store == nil {
		log.Printf("Admin UI disabled: conversation store is not available")
		return nil
	}
	h, err := admin.New(admin.Config{Username: a.Username, PasswordHash: a.PasswordHash, PasswordHashB64: a.PasswordHashB64, SessionSecret: a.SessionSecret, ResetPassword: a.PasswordReset}, store, admin.Options{})
	if err != nil {
		log.Printf("Admin UI disabled: %v", err)
		return nil
	}
	return func(mux *http.ServeMux) int {
		h.Mount(mux)
		log.Printf("Admin UI mounted at /admin")
		return 1
	}
}

// zaloModel names the model the Zalo assistant actually calls: the
// OpenAI-compatible one when that completer is in use, else the Anthropic chat model.
func zaloModel(cfg *config.Config, completer llm.Completer) string {
	if completer != nil {
		return cfg.LLMOpenAI.Model
	}
	return cfg.Chat.Model
}
