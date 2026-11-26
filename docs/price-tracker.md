# Price Tracker Service

The Price Tracker service provides a flexible, extensible system for fetching and normalizing price data from multiple sources with direct source instantiation.

## 🏗 Architecture Overview

```
PriceService (manages multiple sources)
└── PriceSource[] (individual price sources)
```

## 🔧 Core Components

### 1. Price Structure

The unified price data structure used across all sources:

```go
type Price struct {
    Type       string    `json:"type"`        // e.g., "SJC 1L", "Bitcoin"
    BuyPrice   float64   `json:"buy_price"`   // Buying price
    SellPrice  float64   `json:"sell_price"`  // Selling price
    Unit       string    `json:"unit"`        // e.g., "chỉ", "BTC"
    Currency   string    `json:"currency"`    // e.g., "VND", "USD"
    Source     string    `json:"source"`      // Source identifier
    Timestamp  time.Time `json:"timestamp"`   // When data was fetched
}
```

### 2. PriceSource Interface

Contract that all price sources must implement:

```go
type PriceSource interface {
    GetPrices() ([]Price, error)  // Fetch prices from source
    GetSourceName() string        // Return unique source identifier
}
```

### 3. PriceService

Core service managing multiple price sources:

```go
type PriceService struct {
    sources []PriceSource
    client  *http.Client
}
```

**Key Methods:**
- `AddSource(source PriceSource)` - Add a new price source
- `GetAllPrices() (map[string][]Price, error)` - Get prices from all sources
- `GetPricesFromSource(sourceName string) ([]Price, error)` - Get prices from specific source

## 📊 Available Sources

### Kim Khoa Cam Ranh Source (disabled)
- **Status**: disabled (`price_sources.kimkhoa.enabled: false` in `config.docker.yaml` and the built-in default) until a reliable upstream exists. The parser and tests are kept; to re-enable set `enabled: true` (and `url`) or env `PRICE_SOURCES_KIMKHOA_ENABLED=true`.
- **Description**: Gold prices of Kim Khoa (Cam Ranh), scraped from the HTML price table
- **Page**: `https://tuanquangdong.com/gia-vang/kim-khoa-cam-ranh/`
- **Filter**: only gold products with purity ≥ 97% (999.9, 9999, 999, 98, 97); lower purities and silver are dropped; a sell price of 0 on the page means not quoted and is reported as 0
- **Usage**:
  ```go
  kimKhoaSource := pricetracker.NewKimKhoaSource("") // empty → DefaultKimKhoaURL
  ```

### CoinGecko Source and VND value (USDT/VND P2P)

Crypto prices stay in USD (and percentage changes are computed on USD). Wherever
they are shown (scheduled price report, chat `get_prices`), an approximate VND
value is appended: `≈ 1.797.512.000 ₫ (USDT/VND P2P)`. Code:
`services/price-tracker/p2prate.go`; fixtures: `testdata/p2p/`.

Rate semantics (fixed constants, no configuration):

- **Source**: Binance P2P public advert search, `POST /bapi/c2c/v2/friendly/c2c/adv/search`
  with `{"fiat":"VND","asset":"USDT","tradeType":"BUY"|"SELL","page":1,"rows":10}`; no auth.
- **Sides**: both. `BUY` lists adverts a user buys USDT from (their cost), `SELL` lists adverts a
  user sells USDT to (their proceeds). Rate = midpoint of the two per-side medians (neither the buy
  nor the sell price).
- **Top N**: the first 10 adverts of each side.
- **Valid advert**: price is a positive number within 15,000 to 40,000 VND/USDT and
  `tradableQuantity`, when present, is positive. Invalid adverts are dropped.
- **Minimum sample**: 5 valid adverts per side, otherwise the rate is unavailable (never one-sided).
- **Median**: odd count = middle value; even count = mean of the two middle values.
- **Freshness**: served as is for 10 min; then refreshed. If the refresh fails the previous rate is
  kept only until 30 min after it was fetched; after that **no VND is shown** (USD only).
- **Failures**: a failed fetch (timeout 8 s, non-200, 429, malformed or too few ads, body capped at
  1 MiB) starts a 2 min backoff (5 min after HTTP 429) with no new requests. Concurrent callers share
  one refresh. One reader (`sharedP2PRate`) serves both the scheduler and chat.
- When the rate is unavailable the message is sent with USD only; it never fails and never shows a
  zero or wrong VND value.

### Fear & Greed emphasis

The crypto Fear & Greed index is emphasized only when it is Extreme Fear (<= 25) or Extreme
Greed (>= 75); 26-74 render exactly as before. Presentation is per channel:

- Scheduled price report (Telegram legacy `Markdown`): `⚠️ *😱 Fear & Greed: 20 — ...*` (bold + emoji).
- `/fng` and `/feargreed` direct reply (Telegram, plain `SendReply`): `⚠️ ` prefix only, no markup.
- Chat `fear_greed_index` tool (Zalo, `SendMarkdown`): the tool output is model input, so for
  extreme values it appends an explicit instruction note (leading `⚠️`) asking the model to state the
  index in a bold `**...**` sentence starting with `⚠️`. Emphasis there depends on the model following
  the note; the note is absent at 26-74.

### Doji Source
- **Description**: Vietnamese gold and silver price provider
- **API**: `https://banggia.doji.vn/api/TablePrice/GetTablePrice` (backs https://banggia.doji.vn/gold-price)
- **Usage**:
  ```go
  dojiSource := pricetracker.NewDojiSource("") // empty → DefaultDojiAPIURL
  ```

The old `giavang.doji.vn` XML API was retired (HTTP 503) and replaced by this endpoint.

**Response Format:** the endpoint returns an encrypted envelope; `data` is
`base64(iv[16] || AES-256-CBC ciphertext)`, PKCS#7 padded. The key is published in
the site's own JS bundle, so it is obfuscation rather than authentication.

```json
{ "status": true, "data": "UP3G1VYtOUUEhiji5Pka7b..." }
```

Decrypted, it is a flat array of price rows:

```json
[
    {
        "materialCode": "01",
        "materialName": "VÀNG MIẾNG SJC",
        "priceDojiBuyIn": 14030,
        "priceDojiSellOut": 14330,
        "type": "G",
        "isActive": true,
        "updateDate": "2026-08-14T05:08:22.6611968Z"
    }
]
```

`type` is `G` for gold (quoted per chỉ) or `S` for silver (per lượng); both are in
thousands of VND, matching the other sources. Rows with `isActive: false` are skipped.

## 💻 Usage Examples

### Basic Usage

```go
package main

import (
    "fmt"
    "log"
    "everything-you-need/m/services/price-tracker"
)

func main() {
    // Create service
    service := pricetracker.NewPriceService()
    
    // Create and add Doji source
    dojiSource := pricetracker.NewDojiSource("258fbd2a72ce8481089d88c678e9fe4f")
    service.AddSource(dojiSource)
    
    // Get all prices
    allPrices, err := service.GetAllPrices()
    if err != nil {
        log.Fatal(err)
    }
    
    // Display results
    for source, prices := range allPrices {
        fmt.Printf("Source: %s\n", source)
        for _, price := range prices {
            fmt.Printf("  %s: Buy=%.0f, Sell=%.0f %s\n",
                price.Type, price.BuyPrice, price.SellPrice, price.Currency)
        }
    }
}
```

### Advanced Usage

```go
// Create service directly
service := pricetracker.NewPriceService()

// Create and add custom source
customSource := NewMyCustomSource("config-param")
service.AddSource(customSource)

// Get prices from specific source
dojiPrices, err := service.GetPricesFromSource("doji")
if err != nil {
    log.Printf("Failed to get Doji prices: %v", err)
}
```

## 🔧 Configuration

### Environment Variables
None required for the core service. Individual sources may require configuration.

### Source Configuration
Sources are created directly with their required parameters:

```go
// Doji source
dojiSource := pricetracker.NewDojiSource("your-api-key")

// Custom source with multiple parameters
customSource := NewMyCustomSource(&Config{
    APIKey:  "your-api-key",
    Secret:  "your-secret", 
    BaseURL: "https://api.example.com",
})
```

## 🚨 Error Handling

The service implements graceful error handling:

- **Source Failures**: If one source fails, others continue to work
- **Network Timeouts**: 30-second timeout for all HTTP requests
- **Invalid Data**: Malformed responses are skipped with logging
- **Missing Config**: Clear error messages for missing configuration

```go
allPrices, err := manager.GetAllPrices()
if err != nil {
    // This only fails if ALL sources fail
    log.Fatal(err)
}

// Individual source errors are handled internally
for source, prices := range allPrices {
    if len(prices) == 0 {
        log.Printf("No data from source: %s", source)
    }
}
```

## 🎯 Best Practices

### 1. Error Handling
```go
// Always check for errors
prices, err := manager.GetPricesFromSource("doji")
if err != nil {
    log.Printf("Failed to get prices: %v", err)
    return
}
```

### 2. Concurrent Access
```go
// The service is NOT thread-safe for writes
// Use mutex for concurrent access:
var mu sync.Mutex

mu.Lock()
manager.AddSourceByType("new-source", config)
mu.Unlock()
```

### 3. Resource Management
```go
// Sources with HTTP clients should implement cleanup
type MySource struct {
    client *http.Client
}

func (s *MySource) Close() error {
    s.client.CloseIdleConnections()
    return nil
}
```

## 📈 Performance Considerations

- **HTTP Client Reuse**: Single HTTP client per service instance
- **Concurrent Fetching**: Sources are queried sequentially (can be optimized)
- **Caching**: No built-in caching (implement at application level)
- **Rate Limiting**: Implement per-source if needed

## 🔍 Monitoring

### Metrics to Track
- Response times per source
- Success/failure rates
- Price volatility alerts
- API quota usage

### Logging
The service logs:
- Source addition/removal
- API failures
- Data parsing errors
- Performance metrics

## 🚀 Future Enhancements

- **Concurrent Source Fetching**: Parallel price retrieval
- **Built-in Caching**: Redis/memory caching layer  
- **Rate Limiting**: Per-source rate limiting
- **Circuit Breaker**: Fault tolerance for failing sources
- **Metrics Collection**: Prometheus metrics
- **WebSocket Support**: Real-time price streaming