// Package telebot sends and receives Telegram messages for the bot.
package telebot

import (
	"bytes"
	"context"
	"encoding/json"
	"everything-you-need/m/services/config"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/submodule-org/submodule.go/v2"
)

type TeleBotService struct {
	botToken string
	client   *http.Client
	apiBase  string // empty = https://api.telegram.org (overridden in tests)
}

type SendMessageRequest struct {
	ChatID           string `json:"chat_id"`
	Text             string `json:"text"`
	ParseMode        string `json:"parse_mode,omitempty"`
	ReplyToMessageID int64  `json:"reply_to_message_id,omitempty"`
}

type TelegramResponse struct {
	Ok          bool   `json:"ok"`
	Description string `json:"description,omitempty"`
}

func NewTeleBotService(config *config.Config) *TeleBotService {
	if !config.Telegram.Enabled {
		log.Printf("Telegram bot service is disabled")
		return nil
	}

	if config.Telegram.BotToken == "" || config.Telegram.ChannelID == "" {
		log.Printf("Telegram bot service is disabled: bot_token or channel_id is missing")
		return nil
	}

	log.Printf("Telegram bot service initialized")

	return &TeleBotService{
		botToken: config.Telegram.BotToken,
		client:   &http.Client{Timeout: 30 * time.Second},
	}
}

var TeleBotServiceMod = submodule.Make[*TeleBotService](NewTeleBotService, config.ConfigMod)

func (t *TeleBotService) SendToChannel(channelID, message string) error {
	return t.sendMessage(channelID, message, "", 0)
}

func (t *TeleBotService) SendToChannelWithMarkdown(channelID, message string) error {
	return t.sendMessage(channelID, message, "Markdown", 0)
}

func (t *TeleBotService) SendToChannelWithHTML(channelID, message string) error {
	return t.sendMessage(channelID, message, "HTML", 0)
}

// SendMessage satisfies the reminder.Sender interface.
func (t *TeleBotService) SendMessage(_ context.Context, chatID, text string) error {
	return t.sendMessage(chatID, text, "", 0)
}

// SendReply satisfies the reminder.Sender interface by replying to a specific message.
func (t *TeleBotService) SendReply(_ context.Context, chatID string, replyToMessageID int64, text string) error {
	return t.sendMessage(chatID, text, "", replyToMessageID)
}

func (t *TeleBotService) sendMessage(chatID, text, parseMode string, replyToMessageID int64) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", t.botToken)

	payload := SendMessageRequest{
		ChatID:           chatID,
		Text:             text,
		ParseMode:        parseMode,
		ReplyToMessageID: replyToMessageID,
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	var teleResp TelegramResponse
	if err := json.Unmarshal(body, &teleResp); err != nil {
		return fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if !teleResp.Ok {
		return fmt.Errorf("telegram API error: %s", teleResp.Description)
	}

	return nil
}

type reactionType struct {
	Type  string `json:"type"`
	Emoji string `json:"emoji"`
}

// SetReactionRequest is the body of Telegram's setMessageReaction.
type SetReactionRequest struct {
	ChatID    string         `json:"chat_id"`
	MessageID int64          `json:"message_id"`
	Reaction  []reactionType `json:"reaction"`
}

// reactionTimeout bounds one setMessageReaction call.
const reactionTimeout = 5 * time.Second

// SetReaction sets a single emoji reaction on a message. Errors are generic on
// purpose: the request URL contains the bot token and must never leak.
func (t *TeleBotService) SetReaction(ctx context.Context, chatID string, messageID int64, emoji string) error {
	base := t.apiBase
	if base == "" {
		base = "https://api.telegram.org"
	}
	body, err := json.Marshal(SetReactionRequest{
		ChatID:    chatID,
		MessageID: messageID,
		Reaction:  []reactionType{{Type: "emoji", Emoji: emoji}},
	})
	if err != nil {
		return fmt.Errorf("telegram reaction: bad payload")
	}
	ctx, cancel := context.WithTimeout(ctx, reactionTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/bot"+t.botToken+"/setMessageReaction", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telegram reaction: bad request")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("telegram reaction: request failed")
	}
	defer resp.Body.Close()
	var tr TelegramResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&tr); err != nil || !tr.Ok {
		return fmt.Errorf("telegram reaction: rejected (status %d)", resp.StatusCode)
	}
	return nil
}

// React satisfies chat.Reactor.
func (t *TeleBotService) React(ctx context.Context, chatID string, msgID int64, emoji string) error {
	return t.SetReaction(ctx, chatID, msgID, emoji)
}
