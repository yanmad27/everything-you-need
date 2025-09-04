# API Reference

Complete API reference for all services and interfaces in the Everything You Need application.

## 📦 Package Overview

- `pricetracker`: Price tracking and management services
- `telenoti`: Telegram notification services

---

## 🏷 Price Tracker API

### Data Types

#### Price
Unified price data structure used across all sources.

```go
type Price struct {
    Type       string    `json:"type"`        // Price type identifier
    BuyPrice   float64   `json:"buy_price"`   // Buying price
    SellPrice  float64   `json:"sell_price"`  // Selling price  
    Unit       string    `json:"unit"`        // Unit of measurement
    Currency   string    `json:"currency"`    // Currency code
    Source     string    `json:"source"`      // Source identifier
    Timestamp  time.Time `json:"timestamp"`   // Data timestamp
}
```

**Field Details:**
- `Type`: Product/commodity type (e.g., "SJC 1L", "Bitcoin")
- `BuyPrice`: Price for buying (consumer perspective)
- `SellPrice`: Price for selling (consumer perspective)
- `Unit`: Measurement unit (e.g., "chỉ", "gram", "BTC")
- `Currency`: ISO currency code or local currency
- `Source`: Unique source identifier (e.g., "doji", "binance")
- `Timestamp`: When the price was fetched (UTC)

---

### Interfaces

#### PriceSource
Contract for all price data sources.

```go
type PriceSource interface {
    GetPrices() ([]Price, error)
    GetSourceName() string
}
```

**Methods:**

##### GetPrices
Fetches current prices from the source.

```go
GetPrices() ([]Price, error)
```

**Returns:**
- `[]Price`: Array of current prices
- `error`: Error if fetch fails

**Error Cases:**
- Network connectivity issues
- API authentication failures
- Invalid API responses
- Rate limiting

##### GetSourceName
Returns the unique identifier for this source.

```go
GetSourceName() string
```

**Returns:**
- `string`: Unique source identifier

---

#### PriceFactory
Factory interface for creating price sources.

```go
type PriceFactory interface {
    CreateSource(sourceType string, config map[string]interface{}) (PriceSource, error)
    GetSupportedSources() []string
}
```

**Methods:**

##### CreateSource
Creates a new price source instance.

```go
CreateSource(sourceType string, config map[string]interface{}) (PriceSource, error)
```

**Parameters:**
- `sourceType`: Type of source to create (e.g., "doji")
- `config`: Source-specific configuration

**Returns:**
- `PriceSource`: New source instance
- `error`: Error if creation fails

**Example:**
```go
source, err := factory.CreateSource("doji", map[string]interface{}{
    "api_key": "your-api-key",
})
```

##### GetSupportedSources
Returns list of supported source types.

```go
GetSupportedSources() []string
```

**Returns:**
- `[]string`: Array of supported source types

---

### Services

#### PriceService
Core service for managing multiple price sources.

```go
type PriceService struct {
    sources []PriceSource
    client  *http.Client
}
```

**Constructor:**

##### NewPriceService
Creates a new price service instance.

```go
func NewPriceService() *PriceService
```

**Returns:**
- `*PriceService`: New service instance with 30-second HTTP timeout

**Methods:**

##### AddSource
Adds a price source to the service.

```go
func (g *PriceService) AddSource(source PriceSource)
```

**Parameters:**
- `source`: Price source to add

**Thread Safety:** Not thread-safe for concurrent writes

##### GetAllPrices
Retrieves prices from all configured sources.

```go
func (g *PriceService) GetAllPrices() (map[string][]Price, error)
```

**Returns:**
- `map[string][]Price`: Map of source name to prices
- `error`: Only returns error if no sources are configured

**Behavior:**
- Continues if individual sources fail
- Failed sources return empty price array
- Never fails due to individual source errors

**Example Response:**
```go
{
    "doji": []Price{
        {Type: "SJC 1L", BuyPrice: 82000000, SellPrice: 84200000, ...},
    },
    "binance": []Price{},  // Failed source
}
```

##### GetPricesFromSource
Retrieves prices from a specific source.

```go
func (g *PriceService) GetPricesFromSource(sourceName string) ([]Price, error)
```

**Parameters:**
- `sourceName`: Name of the source

**Returns:**
- `[]Price`: Prices from the specified source
- `error`: Error if source not found or fetch fails

**Error Cases:**
- Source not found: `"source {name} not found"`
- Network/API errors from the specific source

---

#### PriceManager  
High-level manager with factory integration.

```go
type PriceManager struct {
    service *PriceService
    factory PriceFactory
}
```

**Constructor:**

##### NewPriceManager
Creates a new price manager with default factory.

```go
func NewPriceManager() *PriceManager
```

**Returns:**
- `*PriceManager`: Manager with PriceService and DefaultPriceFactory

**Methods:**

##### AddSourceByType
Creates and adds a source using the factory.

```go
func (m *PriceManager) AddSourceByType(sourceType string, config map[string]interface{}) error
```

**Parameters:**
- `sourceType`: Type of source (e.g., "doji")
- `config`: Source configuration

**Returns:**
- `error`: Error if source creation fails

**Example:**
```go
err := manager.AddSourceByType("doji", map[string]interface{}{
    "api_key": "258fbd2a72ce8481089d88c678e9fe4f",
})
```

##### GetAllPrices
Delegates to underlying PriceService.

```go
func (m *PriceManager) GetAllPrices() (map[string][]Price, error)
```

##### GetPricesFromSource
Delegates to underlying PriceService.

```go
func (m *PriceManager) GetPricesFromSource(sourceName string) ([]Price, error)
```

##### GetSupportedSources
Returns supported source types from factory.

```go
func (m *PriceManager) GetSupportedSources() []string
```

---

### Source Implementations

#### DojiSource
Vietnamese gold price source.

```go
type DojiSource struct {
    apiKey string
    client *http.Client
}
```

**Constructor:**

##### NewDojiSource
Creates a new Doji source instance.

```go
func NewDojiSource(apiKey string) *DojiSource
```

**Parameters:**
- `apiKey`: Doji API key

**Configuration:**
- API endpoint: `http://giavang.doji.vn/api/giavang/`
- Timeout: 30 seconds
- Currency: VND (Vietnamese Dong)

**Price Types Supported:**
- SJC gold bars (various sizes)
- Gold rings and jewelry
- Other precious metals

---

## 📱 Telegram Notification API

### Data Types

#### SendMessageRequest
Request structure for Telegram API.

```go
type SendMessageRequest struct {
    ChatID    string `json:"chat_id"`
    Text      string `json:"text"`  
    ParseMode string `json:"parse_mode,omitempty"`
}
```

#### TelegramResponse
Response structure from Telegram API.

```go
type TelegramResponse struct {
    Ok          bool   `json:"ok"`
    Description string `json:"description,omitempty"`
}
```

---

### Services

#### TeleNotiService
Telegram notification service.

```go
type TeleNotiService struct {
    botToken string
    client   *http.Client
}
```

**Constructor:**

##### NewTeleNotiService
Creates a new Telegram notification service.

```go
func NewTeleNotiService(botToken string) *TeleNotiService
```

**Parameters:**
- `botToken`: Telegram bot token from @BotFather

**Returns:**
- `*TeleNotiService`: Service instance with 30-second timeout

**Methods:**

##### SendToChannel
Sends plain text message to a channel.

```go
func (t *TeleNotiService) SendToChannel(channelID, message string) error
```

**Parameters:**
- `channelID`: Channel ID (`@channel` or `-1001234567890`)
- `message`: Plain text message (max 4096 chars)

**Returns:**
- `error`: Error if send fails

**Error Cases:**
- Invalid bot token
- Channel not found
- Bot not admin
- Message too long
- Network errors

##### SendToChannelWithMarkdown
Sends Markdown-formatted message.

```go
func (t *TeleNotiService) SendToChannelWithMarkdown(channelID, message string) error
```

**Parameters:**
- `channelID`: Channel ID
- `message`: Markdown-formatted message

**Supported Markdown:**
- `*bold*`, `_italic_`, `` `code` ``
- `[link](url)`
- ``` ```code block``` ```

##### SendToChannelWithHTML
Sends HTML-formatted message.

```go
func (t *TeleNotiService) SendToChannelWithHTML(channelID, message string) error
```

**Parameters:**
- `channelID`: Channel ID
- `message`: HTML-formatted message

**Supported HTML:**
- `<b>bold</b>`, `<i>italic</i>`, `<code>code</code>`
- `<a href="url">link</a>`
- `<pre>code block</pre>`

---

## 🔧 Configuration

### Environment Variables

| Variable | Type | Required | Description |
|----------|------|----------|-------------|
| `TELEGRAM_BOT_TOKEN` | string | No | Bot token from @BotFather |
| `TELEGRAM_CHANNEL_ID` | string | No | Default channel ID |

### Source Configurations

#### Doji Configuration
```go
config := map[string]interface{}{
    "api_key": "your-doji-api-key",  // Required
}
```

---

## 🚨 Error Reference

### Common Error Types

#### Price Tracker Errors
- `"source {name} not found"`: Source not registered
- `"failed to create source {type}"`: Factory creation failed  
- `"failed to marshal payload"`: JSON encoding error
- `"failed to make request"`: Network/HTTP error
- `"failed to unmarshal response"`: Invalid API response
- `"API returned error status"`: API-specific error

#### Telegram Service Errors
- `"telegram API error: Unauthorized"`: Invalid bot token
- `"telegram API error: Chat not found"`: Channel not found/bot not admin
- `"telegram API error: Bad Request: message is too long"`: Message > 4096 chars
- `"failed to create request"`: HTTP request creation failed
- `"failed to send request"`: Network error

---

## 📊 Response Examples

### Price Tracker Responses

#### GetAllPrices Success
```json
{
  "doji": [
    {
      "type": "SJC 1L",
      "buy_price": 82000000,
      "sell_price": 84200000,
      "unit": "chỉ",
      "currency": "VND", 
      "source": "doji",
      "timestamp": "2023-12-01T10:30:00Z"
    }
  ]
}
```

#### GetAllPrices with Failed Source
```json
{
  "doji": [
    {
      "type": "SJC 1L",
      "buy_price": 82000000,
      "sell_price": 84200000,
      "unit": "chỉ",
      "currency": "VND",
      "source": "doji", 
      "timestamp": "2023-12-01T10:30:00Z"
    }
  ],
  "failed-source": []
}
```

### Telegram Service Responses

#### Success Response
```json
{
  "ok": true
}
```

#### Error Response  
```json
{
  "ok": false,
  "description": "Bad Request: chat not found"
}
```

---

## 🔗 Related Documentation

- [Price Tracker Service Guide](price-tracker.md)
- [Telegram Service Guide](telegram-service.md) 
- [Adding New Sources](adding-sources.md)
- [Contributing Guidelines](CONTRIBUTING.md)