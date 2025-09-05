package telenoti

import (
	"bytes"
	"encoding/json"
	"everything-you-need/m/services/config"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/submodule-org/submodule.go/v2"
)

type TeleNotiService struct {
	botToken string
	client   *http.Client
}

type SendMessageRequest struct {
	ChatID    string `json:"chat_id"`
	Text      string `json:"text"`
	ParseMode string `json:"parse_mode,omitempty"`
}

type TelegramResponse struct {
	Ok          bool   `json:"ok"`
	Description string `json:"description,omitempty"`
}

func NewTeleNotiService(config *config.Config) *TeleNotiService {
	if !config.Telegram.Enabled {
		log.Printf("Telegram notification is disabled")
		return nil
	}

	if config.Telegram.BotToken == "" || config.Telegram.ChannelID == "" {
		log.Printf("Telegram notification is disabled: bot_token or channel_id is missing")
		return nil
	}

	log.Printf("Telegram notification service initialized")

	return &TeleNotiService{
		botToken: config.Telegram.BotToken,
		client:   &http.Client{Timeout: 30 * time.Second},
	}
}

var TeleNotiServiceMod = submodule.Make[*TeleNotiService](NewTeleNotiService, config.ConfigMod)

func (t *TeleNotiService) SendToChannel(channelID, message string) error {
	return t.sendMessage(channelID, message, "")
}

func (t *TeleNotiService) SendToChannelWithMarkdown(channelID, message string) error {
	return t.sendMessage(channelID, message, "Markdown")
}

func (t *TeleNotiService) SendToChannelWithHTML(channelID, message string) error {
	return t.sendMessage(channelID, message, "HTML")
}

func (t *TeleNotiService) sendMessage(chatID, text, parseMode string) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", t.botToken)

	payload := SendMessageRequest{
		ChatID:    chatID,
		Text:      text,
		ParseMode: parseMode,
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
