# Everything You Need

A comprehensive Go application featuring price tracking and notification services with extensible architecture.

## 🚀 Features

- **Price Tracker Service**: Track prices from multiple sources using factory pattern
- **Telegram Notifications**: Send price updates and alerts to Telegram channels
- **Gold Watch**: Every 5 minutes, alerts the channel when a tracked gold price (SJC/9999/tròn trơn) moves
- **News Digest**: Daily 8PM bilingual (EN+VN) digest of the hottest AI/tech/VN stories, curated from RSS feeds and ranked by OpenAI
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

A Vietnamese natural-language reminder bot. Type a message into the Telegram
channel and the bot parses the time + task, schedules it, and fires the
reminder back into the channel when it's due.

### How to use

Just post in the channel using everyday Vietnamese — the bot picks up anything
containing **`nhắc tao`**, **`nhắc tôi`**, **`nhắc mình`**, **`nhắc t`**,
**`nhắc ae`**, **`nhắc mọi người`**, or **`nhắc anh em`**.

#### Creating reminders

| What you type | What happens |
|---|---|
| `2h nữa nhắc tao ăn cơm` | Fires 2 hours from now with "ăn cơm" |
| `30 phút nữa nhắc tôi gọi mẹ` | Fires in 30 minutes |
| `19h ngày mai nhắc tôi họp với Huy` | Fires at 19:00 tomorrow |
| `8h sáng mai nhắc mình đi khám` | Fires at 08:00 tomorrow |
| `thứ 2 tới nhắc mình nộp báo cáo` | Fires next Monday (default 09:00) |
| `lúc 20:30 nhắc ae đi đá bóng` | Fires at 20:30 today |
| `1/5 nhắc mọi người họp team 10h` | Fires at 10:00 on 1 May |

When the bot understands the request, it replies immediately with:

```
✅ Đã đặt nhắc #12: "ăn cơm" vào 19:30 17/04/2026
```

If the time is ambiguous or in the past, it replies with a reason:

```
❓ Mình không hiểu: Thời điểm mơ hồ, vui lòng nói rõ giờ/ngày. Thử lại nhé.
```

#### Firing

When the reminder is due, the bot fires it in the channel as a **reply to
your original message**, @-mentioning you:

```
⏰ Nhắc @doan: ăn cơm
```

Granularity is 1 minute. If the bot was down when it should have fired,
the reminder is sent on the next minute tick with `(trễ N phút)` prefix.

#### Listing and canceling

| Command | Effect |
|---|---|
| `/reminders` or `danh sách nhắc` | Show all pending reminders with IDs |
| `/cancel <id>` or `hủy nhắc <id>` | Cancel a pending reminder |

Example session:

```
You:  2h nữa nhắc tao họp
Bot:  ✅ Đã đặt nhắc #7: "họp" vào 19:30 17/04/2026

You:  /reminders
Bot:  📋 Nhắc nhở đang chờ:
      • #7 — 19:30 17/04/2026 — họp
      Hủy bằng: /cancel <id>

You:  /cancel 7
Bot:  🗑 Đã hủy nhắc #7.
```

### One-time setup

1. `reminder.enabled: true` is already set in `config.docker.yaml`.
2. Provide an OpenAI API key via the `REMINDER_OPENAI_API_KEY` env var
   (create one at https://platform.openai.com/api-keys).
3. Generate a webhook secret and set it via the `TELEGRAM_WEBHOOK_SECRET`
   env var:

   ```bash
   openssl rand -hex 32
   ```

4. Register the webhook with Telegram (once), pointing at the app's public
   Dokploy domain:

   ```bash
   curl -X POST "https://api.telegram.org/bot$BOT_TOKEN/setWebhook" \
        -d url=https://eyn.rsrm.dev/telegram-webhook \
        -d secret_token=$WEBHOOK_SECRET \
        -d 'allowed_updates=["message","channel_post"]'
   ```

   `allowed_updates` must include `channel_post` for broadcast channels.
5. Make sure the bot is an **admin** of the channel (so it can read messages
   and post replies).

Telegram delivers updates straight to the app over HTTPS at
`https://eyn.rsrm.dev/telegram-webhook` (Traefik → `price-tracker:8080`);
the app validates the `X-Telegram-Bot-Api-Secret-Token` header against
`telegram.webhook_secret`.

Reminders are stored in SQLite at `data/reminders.db` and persist across
deploys (the `eyn-data` volume).

## 🚢 Deployment

Runs on a [Dokploy](https://dokploy.com) instance as a Docker Compose service,
built from the `main` branch. **Auto-deploy is on** — pushing to `main`
rebuilds and redeploys automatically. To redeploy by hand, hit **Deploy** on the
Compose service in the Dokploy dashboard.

### How config & secrets work

The image bakes in `config.docker.yaml` (renamed to `config.yaml` inside the
container) — this holds all **non-secret** config, including the list values
(`news.feeds`, `news.search_queries`) that can't be expressed as env vars.

**Secrets are supplied as Dokploy Compose environment variables** and override
the blanked fields at runtime via Viper's `AutomaticEnv`. Set these in the
Dokploy Compose → Environment tab:

| Env var | Overrides |
|---|---|
| `TELEGRAM_BOT_TOKEN` | `telegram.bot_token` |
| `TELEGRAM_WEBHOOK_SECRET` | `telegram.webhook_secret` |
| `NEWS_OPENAI_API_KEY` | `news.openai_api_key` |
| `NEWS_SERPER_API_KEY` | `news.serper_api_key` |
| `REMINDER_OPENAI_API_KEY` | `reminder.openai_api_key` |
| `PRICE_SOURCES_BTMC_API_URL` | `price_sources.btmc.api_url` |

Doji is **not** in this table: since it moved to `banggia.doji.vn` it needs no API
key, so its URL lives in `config.docker.yaml` and is no longer passed through the
Compose environment. A leftover `PRICE_SOURCES_DOJI_API_URL` in the Dokploy
dashboard is now inert, but is worth deleting to avoid confusion.

SQLite databases (`data/*.db`) persist in the named volume `eyn-data` across
redeploys.

> The repo is private, so Dokploy clones it through a GitHub App installed on
> the account with access to `yanmad27/everything-you-need`.

## 🤝 Contributing

See [CONTRIBUTING.md](docs/CONTRIBUTING.md) for contribution guidelines.

## 📚 Documentation

- [Price Tracker Service](docs/price-tracker.md)
- [Telegram Service](docs/telegram-service.md)
- [API Reference](docs/api-reference.md)
- [Adding New Sources](docs/adding-sources.md)

## 📄 License

This project is licensed under the MIT License - see the LICENSE file for details.