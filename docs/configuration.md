# Configuration Guide

This guide explains how to configure the Everything You Need application using Viper for flexible configuration management.

## 🎯 Configuration Methods

The application supports multiple configuration methods with the following priority:

1. **Environment Variables** (highest priority)
2. **Configuration File** (config.yaml)
3. **Default Values** (lowest priority)

## 📁 Configuration Files

### Location Search Order

The application searches for configuration files in the following locations:

1. Current directory (`./config.yaml`)
2. Config directory (`./config/config.yaml`)
3. System directory (`/etc/everything-you-need/config.yaml`)

### Supported Formats

- **YAML** (recommended): `config.yaml`
- **JSON**: `config.json`
- **TOML**: `config.toml`

## ⚙️ Configuration Structure

### Complete Configuration Example

```yaml
# Application settings
app:
  name: "Everything You Need"           # Application name
  environment: "production"             # Environment: development, staging, production
  log_level: "info"                    # Log level: debug, info, warn, error

# Price source configurations
price_sources:
  doji:
    enabled: true                      # Enable/disable Doji source
    api_key: "your-doji-api-key"      # Doji API key

# Telegram notification settings
telegram:
  enabled: true                        # Enable/disable Telegram notifications
  bot_token: "your-bot-token"         # Telegram bot token from @BotFather
  channel_id: "@your_channel"         # Channel ID or username
```

## 🌍 Environment Variables

### Variable Naming Convention

Environment variables use uppercase with underscores and follow this pattern:
- Nested keys are separated by underscores
- Example: `price_sources.doji.api_key` → `PRICE_SOURCES_DOJI_API_KEY`

### Application Settings

| Variable | Type | Default | Description |
|----------|------|---------|-------------|
| `APP_NAME` | string | "Everything You Need" | Application name |
| `APP_ENVIRONMENT` | string | "development" | Environment (development, staging, production) |
| `APP_LOG_LEVEL` | string | "info" | Log level (debug, info, warn, error) |

### Price Source Settings

#### Doji Source

| Variable | Type | Default | Description |
|----------|------|---------|-------------|
| `PRICE_SOURCES_DOJI_ENABLED` | bool | true | Enable Doji price source |
| `PRICE_SOURCES_DOJI_API_KEY` | string | "" | Doji API key (required if enabled) |

### Telegram Settings

| Variable | Type | Default | Description |
|----------|------|---------|-------------|
| `TELEGRAM_ENABLED` | bool | false | Enable Telegram notifications |
| `TELEGRAM_BOT_TOKEN` | string | "" | Bot token from @BotFather |
| `TELEGRAM_CHANNEL_ID` | string | "" | Channel ID (@channel or -100123...) |

## 🔧 Configuration Examples

### Development Environment

```yaml
app:
  name: "Price Tracker Dev"
  environment: "development"
  log_level: "debug"

price_sources:
  doji:
    enabled: true
    api_key: "dev-api-key"

telegram:
  enabled: false
```

### Production Environment

```yaml
app:
  name: "Production Price Tracker"
  environment: "production"
  log_level: "warn"

price_sources:
  doji:
    enabled: true
    api_key: "${DOJI_API_KEY}"  # Use environment variable

telegram:
  enabled: true
  bot_token: "${TELEGRAM_BOT_TOKEN}"
  channel_id: "${TELEGRAM_CHANNEL_ID}"
```

### Using Only Environment Variables

```bash
# Application
export APP_NAME="My Price Tracker"
export APP_ENVIRONMENT="production"
export APP_LOG_LEVEL="info"

# Price Sources
export PRICE_SOURCES_DOJI_ENABLED="true"
export PRICE_SOURCES_DOJI_API_KEY="your-real-api-key"

# Telegram
export TELEGRAM_ENABLED="true"
export TELEGRAM_BOT_TOKEN="1234567890:ABCdefGhIjKlMnOpQrStUvWxYz"
export TELEGRAM_CHANNEL_ID="@your_alerts_channel"
```

### Docker Environment

```dockerfile
# Dockerfile
ENV APP_ENVIRONMENT=production
ENV APP_LOG_LEVEL=info
ENV PRICE_SOURCES_DOJI_ENABLED=true
ENV TELEGRAM_ENABLED=true
```

```yaml
# docker-compose.yml
services:
  price-tracker:
    build: .
    environment:
      - APP_NAME=Containerized Price Tracker
      - PRICE_SOURCES_DOJI_API_KEY=${DOJI_API_KEY}
      - TELEGRAM_BOT_TOKEN=${TELEGRAM_BOT_TOKEN}
      - TELEGRAM_CHANNEL_ID=${TELEGRAM_CHANNEL_ID}
```

## 🛠 Configuration Validation

### Required Fields

The application validates configuration at startup:

- If `price_sources.doji.enabled` is true, `api_key` must be provided
- If `telegram.enabled` is true, both `bot_token` and `channel_id` must be provided

### Warning Messages

The application logs warnings for:
- Missing API keys when sources are enabled
- Missing Telegram credentials when notifications are enabled
- No configuration file found (falls back to defaults)

## 📋 Configuration File Templates

### Minimal Configuration

```yaml
# Minimal working configuration
price_sources:
  doji:
    api_key: "your-api-key"
```

### Full Production Configuration

```yaml
app:
  name: "Production Price Monitor"
  environment: "production"
  log_level: "info"

price_sources:
  doji:
    enabled: true
    api_key: "prod-doji-api-key"

telegram:
  enabled: true
  bot_token: "1234567890:ABCdefGhIjKlMnOpQrStUvWxYz"
  channel_id: "@price_alerts"
```

## 🔍 Troubleshooting

### Common Issues

#### Configuration File Not Found
```bash
2023/12/01 10:30:00 No config file found, using defaults and environment variables
```
**Solution**: Create `config.yaml` in project root or set environment variables

#### Missing API Key
```bash
2023/12/01 10:30:00 Warning: Doji is enabled but no API key provided
```
**Solution**: Set `PRICE_SOURCES_DOJI_API_KEY` or add to config file

#### Telegram Configuration Issue
```bash
2023/12/01 10:30:00 Warning: Telegram is enabled but bot_token or channel_id is missing
```
**Solution**: Provide both `TELEGRAM_BOT_TOKEN` and `TELEGRAM_CHANNEL_ID`

### Debug Configuration

To see which configuration file is being used:
```bash
go run main.go config.go
```

The application logs:
```
2023/12/01 10:30:00 Using config file: /path/to/config.yaml
```

### Environment Variable Testing

```bash
# Test environment variable override
APP_NAME="Test Override" go run main.go config.go
```

## 🚀 Best Practices

### Security

1. **Never commit secrets** to version control:
   ```bash
   # Add to .gitignore
   config.yaml
   .env
   ```

2. **Use environment variables** for production secrets:
   ```yaml
   telegram:
     bot_token: "${TELEGRAM_BOT_TOKEN}"  # Read from env
   ```

3. **Restrict file permissions**:
   ```bash
   chmod 600 config.yaml
   ```

### Organization

1. **Use example files**:
   - `config.example.yaml` for documentation
   - `.env.example` for environment variables

2. **Environment-specific configs**:
   - `config.dev.yaml`
   - `config.prod.yaml`

3. **Validation**:
   ```go
   if config.PriceSources.Doji.Enabled && config.PriceSources.Doji.APIKey == "" {
       return errors.New("doji api_key is required when enabled")
   }
   ```

### Deployment

1. **Container environments**:
   ```yaml
   # docker-compose.yml
   environment:
     - APP_ENVIRONMENT=production
     - PRICE_SOURCES_DOJI_API_KEY_FILE=/run/secrets/doji_key
   ```

2. **Kubernetes**:
   ```yaml
   # ConfigMap for non-sensitive data
   apiVersion: v1
   kind: ConfigMap
   metadata:
     name: price-tracker-config
   data:
     APP_ENVIRONMENT: "production"
     APP_LOG_LEVEL: "info"
   
   ---
   # Secret for sensitive data
   apiVersion: v1
   kind: Secret
   metadata:
     name: price-tracker-secrets
   data:
     TELEGRAM_BOT_TOKEN: base64encodedtoken
   ```

## 🔗 Related Documentation

- [Viper Documentation](https://github.com/spf13/viper)
- [Main README](../README.md)
- [Telegram Service Setup](telegram-service.md)
- [Price Tracker Configuration](price-tracker.md)