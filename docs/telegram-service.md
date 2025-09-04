# Telegram Notification Service

The Telegram Notification Service provides a simple, reliable way to send messages to Telegram channels using the Telegram Bot API.

## 🚀 Overview

This service allows you to:
- Send plain text messages to Telegram channels
- Send formatted messages (Markdown/HTML)
- Handle errors gracefully
- Integrate easily with other services

## 🏗 Architecture

```
TeleNotiService
├── HTTP Client (for API calls)
├── Bot Token (authentication)
└── Message Formatting (plain/markdown/html)
```

## 🔧 Core Components

### TeleNotiService Structure

```go
type TeleNotiService struct {
    botToken string      // Bot token from @BotFather
    client   *http.Client // HTTP client for API calls
}
```

### Message Request Structure

```go
type SendMessageRequest struct {
    ChatID    string `json:"chat_id"`           // Channel ID or chat ID
    Text      string `json:"text"`              // Message text
    ParseMode string `json:"parse_mode,omitempty"` // "Markdown" or "HTML"
}
```

### API Response Structure

```go
type TelegramResponse struct {
    Ok          bool   `json:"ok"`             // Success status
    Description string `json:"description,omitempty"` // Error description
}
```

## 📱 Setup

### 1. Create a Telegram Bot

1. Message [@BotFather](https://t.me/botfather) on Telegram
2. Send `/newbot` command
3. Choose a name and username for your bot
4. Copy the bot token provided

### 2. Add Bot to Channel

1. Create a Telegram channel or use existing one
2. Add your bot as an administrator
3. Give the bot permission to send messages
4. Get your channel ID (e.g., `@yourchannel` or `-1001234567890`)

### 3. Environment Variables

```bash
export TELEGRAM_BOT_TOKEN="1234567890:ABCdefGhIjKlMnOpQrStUvWxYz"
export TELEGRAM_CHANNEL_ID="@yourchannel"
```

## 💻 Usage Examples

### Basic Usage

```go
package main

import (
    "log"
    "everything-you-need/m/services/tele-noti"
)

func main() {
    // Create service
    teleService := telenoti.NewTeleNotiService("your-bot-token")
    
    // Send simple message
    err := teleService.SendToChannel("@yourchannel", "Hello World!")
    if err != nil {
        log.Fatal(err)
    }
}
```

### Formatted Messages

```go
// Markdown formatting
message := `
*Price Alert!* 🚨

Gold SJC: *84.2M VND*
Silver: *1.2M VND*

_Updated: 2023-12-01 10:30_
`
err := teleService.SendToChannelWithMarkdown("@yourchannel", message)

// HTML formatting
htmlMessage := `
<b>Price Alert!</b> 🚨

Gold SJC: <b>84.2M VND</b>
Silver: <b>1.2M VND</b>

<i>Updated: 2023-12-01 10:30</i>
`
err := teleService.SendToChannelWithHTML("@yourchannel", htmlMessage)
```

### Integration with Price Tracker

```go
// Get prices
allPrices, err := priceManager.GetAllPrices()
if err != nil {
    log.Fatal(err)
}

// Format as JSON and send
pricesJSON, _ := json.MarshalIndent(allPrices, "", "  ")
message := fmt.Sprintf("Price Update:\n```json\n%s\n```", string(pricesJSON))

err = teleService.SendToChannelWithMarkdown("@yourchannel", message)
if err != nil {
    log.Printf("Failed to send notification: %v", err)
}
```

## 🎨 Message Formatting

### Markdown Support

```go
message := `
*Bold text*
_Italic text_
\`Monospace text\`
[Link](https://example.com)

\`\`\`json
{
  "price": 84200000,
  "currency": "VND"
}
\`\`\`
`
```

### HTML Support

```go
htmlMessage := `
<b>Bold text</b>
<i>Italic text</i>
<code>Monospace text</code>
<a href="https://example.com">Link</a>

<pre>
{
  "price": 84200000,
  "currency": "VND"  
}
</pre>
`
```

## 📋 API Methods

### NewTeleNotiService
Creates a new Telegram notification service instance.

```go
func NewTeleNotiService(botToken string) *TeleNotiService
```

**Parameters:**
- `botToken`: Bot token from @BotFather

**Returns:**
- `*TeleNotiService`: Service instance

### SendToChannel
Sends a plain text message to a channel.

```go
func (t *TeleNotiService) SendToChannel(channelID, message string) error
```

**Parameters:**
- `channelID`: Channel ID (e.g., `@channel` or `-1001234567890`)
- `message`: Plain text message

**Returns:**
- `error`: nil on success, error on failure

### SendToChannelWithMarkdown
Sends a Markdown-formatted message to a channel.

```go
func (t *TeleNotiService) SendToChannelWithMarkdown(channelID, message string) error
```

**Parameters:**
- `channelID`: Channel ID
- `message`: Markdown-formatted message

### SendToChannelWithHTML
Sends an HTML-formatted message to a channel.

```go
func (t *TeleNotiService) SendToChannelWithHTML(channelID, message string) error
```

**Parameters:**
- `channelID`: Channel ID  
- `message`: HTML-formatted message

## 🚨 Error Handling

### Common Errors

1. **Invalid Bot Token**
   ```
   telegram API error: Unauthorized
   ```

2. **Bot Not Admin**
   ```
   telegram API error: Chat not found
   ```

3. **Invalid Channel ID**
   ```
   telegram API error: Bad Request: chat not found
   ```

4. **Message Too Long**
   ```
   telegram API error: Bad Request: message is too long
   ```

### Error Handling Example

```go
err := teleService.SendToChannel(channelID, message)
if err != nil {
    switch {
    case strings.Contains(err.Error(), "Unauthorized"):
        log.Printf("Invalid bot token: %v", err)
    case strings.Contains(err.Error(), "chat not found"):
        log.Printf("Channel not found or bot not admin: %v", err)
    case strings.Contains(err.Error(), "message is too long"):
        log.Printf("Message too long, truncating...")
        // Truncate message and retry
    default:
        log.Printf("Unknown error: %v", err)
    }
}
```

## 🔧 Configuration

### Message Limits
- **Maximum message length**: 4096 characters
- **Rate limit**: 30 messages per second
- **File size limit**: 50 MB for documents

### Channel Types
- **Public channels**: Use `@channelname`
- **Private channels**: Use numeric ID (e.g., `-1001234567890`)
- **Private chats**: Use user ID (e.g., `123456789`)

### Bot Permissions Required
- Send messages
- Read channel info (optional)
- Delete messages (if needed for cleanup)

## 🎯 Best Practices

### 1. Token Security
```go
// Use environment variables
botToken := os.Getenv("TELEGRAM_BOT_TOKEN")
if botToken == "" {
    log.Fatal("TELEGRAM_BOT_TOKEN is required")
}
```

### 2. Message Length Management
```go
func truncateMessage(msg string, maxLen int) string {
    if len(msg) <= maxLen {
        return msg
    }
    return msg[:maxLen-3] + "..."
}

message := truncateMessage(longMessage, 4093) // 4096 - 3 for "..."
```

### 3. Retry Logic
```go
func sendWithRetry(service *telenoti.TeleNotiService, channelID, message string, maxRetries int) error {
    for i := 0; i < maxRetries; i++ {
        err := service.SendToChannel(channelID, message)
        if err == nil {
            return nil
        }
        
        log.Printf("Attempt %d failed: %v", i+1, err)
        time.Sleep(time.Duration(i+1) * time.Second)
    }
    return fmt.Errorf("failed after %d attempts", maxRetries)
}
```

## 📊 Monitoring

### Metrics to Track
- Message send success rate
- Response times
- Error types and frequency
- Message queue depth (if implemented)

### Logging
```go
log.Printf("Sending message to channel %s", channelID)
log.Printf("Message sent successfully")
log.Printf("Failed to send message: %v", err)
```

## 🚀 Advanced Features

### Message Templates
```go
type MessageTemplate struct {
    Template string
    Data     interface{}
}

func (t *MessageTemplate) Render() string {
    // Use text/template or similar
}
```

### Scheduled Messages
```go
type ScheduledMessage struct {
    Message   string
    ChannelID string
    SendAt    time.Time
}

// Implement with time.Timer or cron-like scheduler
```

### Message Queue
```go
type MessageQueue struct {
    messages chan Message
    workers  int
}

// Implement for high-volume scenarios
```

## 🔗 Related Documentation

- [Telegram Bot API Documentation](https://core.telegram.org/bots/api)
- [Telegram Bot Features](https://core.telegram.org/bots/features)
- [Price Tracker Integration](price-tracker.md)
- [API Reference](api-reference.md)