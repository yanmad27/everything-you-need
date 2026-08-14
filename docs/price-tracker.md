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