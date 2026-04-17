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
