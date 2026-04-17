# Everything You Need

A comprehensive Go application featuring price tracking and notification services with extensible architecture.

## 🚀 Features

- **Price Tracker Service**: Track prices from multiple sources using factory pattern
- **Telegram Notifications**: Send price updates and alerts to Telegram channels
- **Extensible Architecture**: Easy to add new price sources and notification channels
- **Real-time Data**: Fetch live prices from various APIs

## 📁 Project Structure

```
everything-you-need/
├── services/
│   ├── price-tracker/          # Price tracking service
│   │   ├── price-tracker.service.go
│   │   ├── factory.go
│   │   └── doji.source.go
│   └── tele-noti/              # Telegram notification service
│       └── tele-noti.service.go
├── main.go                     # Application entry point
├── go.mod
└── docs/                       # Documentation
```

## 🛠 Services

### Price Tracker Service
- **Location**: `services/price-tracker/`
- **Purpose**: Fetch and normalize price data from multiple sources
- **Architecture**: Factory pattern for easy extensibility
- **Current Sources**: Doji (Vietnamese gold prices)

### Telegram Notification Service  
- **Location**: `services/tele-noti/`
- **Purpose**: Send notifications to Telegram channels
- **Features**: Plain text, Markdown, and HTML formatting support

## 🚦 Quick Start

### Prerequisites
- Go 1.24.5 or later
- Telegram Bot Token (optional, for notifications)
- Price source API keys (e.g., Doji API key)

### Installation

1. Clone the repository:
```bash
git clone <repository-url>
cd everything-you-need
```

2. Install dependencies:
```bash
go mod tidy
```

3. Configure the application:
```bash
# Copy example config and modify
cp config.example.yaml config.yaml

# OR use environment variables
cp .env.example .env
```

4. Run the application:
```bash
go run main.go config.go
```

## 📖 Usage Examples

### Basic Price Tracking
```go
import "everything-you-need/m/services/price-tracker"

// Create price service
service := pricetracker.NewPriceService()

// Create and add price sources directly
dojiSource := pricetracker.NewDojiSource("your-doji-api-key")
service.AddSource(dojiSource)

// Get all prices
allPrices, err := service.GetAllPrices()
```

### Telegram Notifications
```go
import "everything-you-need/m/services/tele-noti"

// Create notification service
teleService := telenoti.NewTeleNotiService("your-bot-token")

// Send message to channel
err := teleService.SendToChannel("@yourchannel", "Hello World!")

// Send formatted message
err := teleService.SendToChannelWithMarkdown("@yourchannel", "*Bold* text")
```

## 🏗 Architecture

The application follows clean architecture principles with:

- **Interface-based Design**: Clear contracts between components
- **Direct Instantiation**: Simple, explicit source creation
- **Dependency Injection**: Loose coupling between services
- **Error Handling**: Comprehensive error management

## ⚙️ Configuration

The application uses [Viper](https://github.com/spf13/viper) for configuration management, supporting:
- YAML configuration files
- Environment variables
- Default values

### Configuration File

Create a `config.yaml` file in the project root:

```yaml
app:
  name: "Everything You Need"
  environment: "production"
  log_level: "info"

price_sources:
  doji:
    enabled: true
    api_key: "your-doji-api-key"

telegram:
  enabled: true
  bot_token: "your-telegram-bot-token"
  channel_id: "@your_channel"
```

### Environment Variables

You can override any configuration using environment variables:

| Variable | Description | Example |
|----------|-------------|---------|
| `APP_NAME` | Application name | `"My Price Tracker"` |
| `APP_ENVIRONMENT` | Environment (dev/prod) | `"production"` |
| `PRICE_SOURCES_DOJI_API_KEY` | Doji API key | `"your-api-key"` |
| `TELEGRAM_BOT_TOKEN` | Telegram bot token | `"1234567890:ABC..."` |
| `TELEGRAM_CHANNEL_ID` | Channel ID | `"@yourchannel"` |

### Configuration Priority

1. Environment variables (highest)
2. Configuration file (`config.yaml`)
3. Default values (lowest)

## ⏰ Reminder Bot

A Vietnamese natural-language reminder bot reads messages from the Telegram
channel and schedules reminders. It understands phrases like:

- `@bot 2h nữa nhắc tao ăn cơm`
- `@bot 19h ngày mai nhắc tôi họp với Huy`
- `@bot thứ 2 tới nhắc mình nộp báo cáo`

Commands:

| Command | Effect |
|---|---|
| `nhắc tao/tôi/mình …` | Create a reminder |
| `/reminders` or `danh sách nhắc` | List pending reminders |
| `/cancel <id>` or `hủy nhắc <id>` | Cancel a pending reminder |

Reminders fire in the same channel as a reply to the original message,
@-mentioning the author.

### One-time setup

1. Set `reminder.enabled: true` in `config.yaml`.
2. Put a Gemini API key in `reminder.gemini_api_key`
   (get one free at https://aistudio.google.com/app/apikey).
3. Generate and save a webhook secret:

   ```bash
   openssl rand -hex 32
   ```

   Put it in `telegram.webhook_secret`.
4. Register the webhook with Telegram (once):

   ```bash
   curl -X POST "https://api.telegram.org/bot$BOT_TOKEN/setWebhook" \
        -d url=https://smee.io/2iKk7zcT8arhn5WB \
        -d secret_token=$WEBHOOK_SECRET
   ```

The `smee-client` container in `docker-compose.yml` forwards webhook events
from smee.io to the bot's `:8080/telegram-webhook` endpoint.

Reminders are stored in SQLite at `data/reminders.db` and persist across
deploys (the `./data` directory is volume-mounted).

## 🚢 Deployment

Deploy (or redeploy) the latest `main` to the production host:

```bash
ssh hrm.gittunner
cd ~/workspace/everything-you-need
git pull
docker compose up -d --build
```

## 🤝 Contributing

See [CONTRIBUTING.md](docs/CONTRIBUTING.md) for contribution guidelines.

## 📚 Documentation

- [Price Tracker Service](docs/price-tracker.md)
- [Telegram Service](docs/telegram-service.md)
- [API Reference](docs/api-reference.md)
- [Adding New Sources](docs/adding-sources.md)

## 📄 License

This project is licensed under the MIT License - see the LICENSE file for details.