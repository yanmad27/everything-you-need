# Adding New Price Sources

This guide explains how to extend the price tracker service by adding new price sources with direct instantiation.

## 🎯 Overview

Adding a new price source involves:
1. Implementing the `PriceSource` interface
2. Creating a constructor function
3. Testing the integration
4. Documenting the new source

## 🏗 Implementation Steps

### Step 1: Implement PriceSource Interface

Create a new file in `services/price-tracker/` for your source:

```go
// services/price-tracker/your-source.source.go
package pricetracker

import (
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "time"
)

type YourSource struct {
    apiKey    string
    baseURL   string
    client    *http.Client
    // Add other configuration fields as needed
}

// Constructor
func NewYourSource(apiKey string) *YourSource {
    return &YourSource{
        apiKey:  apiKey,
        baseURL: "https://api.yourdomain.com",
        client:  &http.Client{Timeout: 30 * time.Second},
    }
}

// Constructor with custom configuration
func NewYourSourceWithConfig(apiKey, baseURL string) *YourSource {
    return &YourSource{
        apiKey:  apiKey,
        baseURL: baseURL,
        client:  &http.Client{Timeout: 30 * time.Second},
    }
}

// Required: Implement PriceSource interface
func (y *YourSource) GetSourceName() string {
    return "your-source"
}

func (y *YourSource) GetPrices() ([]Price, error) {
    // Implement API call logic
    return y.fetchPrices()
}

// Private method to handle API logic
func (y *YourSource) fetchPrices() ([]Price, error) {
    url := fmt.Sprintf("%s/prices?api_key=%s", y.baseURL, y.apiKey)
    
    req, err := http.NewRequest("GET", url, nil)
    if err != nil {
        return nil, fmt.Errorf("failed to create request: %w", err)
    }
    
    // Add headers if needed
    req.Header.Set("User-Agent", "PriceTracker/1.0")
    
    resp, err := y.client.Do(req)
    if err != nil {
        return nil, fmt.Errorf("failed to make request: %w", err)
    }
    defer resp.Body.Close()
    
    if resp.StatusCode != http.StatusOK {
        return nil, fmt.Errorf("API returned status %d", resp.StatusCode)
    }
    
    body, err := io.ReadAll(resp.Body)
    if err != nil {
        return nil, fmt.Errorf("failed to read response: %w", err)
    }
    
    return y.parseResponse(body)
}

// Define your API response structure
type YourAPIResponse struct {
    Status string `json:"status"`
    Data   []struct {
        Symbol    string  `json:"symbol"`
        Price     float64 `json:"price"`
        BidPrice  float64 `json:"bid_price,omitempty"`
        AskPrice  float64 `json:"ask_price,omitempty"`
        Currency  string  `json:"currency"`
        Timestamp string  `json:"timestamp"`
    } `json:"data"`
}

func (y *YourSource) parseResponse(body []byte) ([]Price, error) {
    var apiResp YourAPIResponse
    if err := json.Unmarshal(body, &apiResp); err != nil {
        return nil, fmt.Errorf("failed to unmarshal response: %w", err)
    }
    
    if apiResp.Status != "success" {
        return nil, fmt.Errorf("API returned error status: %s", apiResp.Status)
    }
    
    var prices []Price
    for _, item := range apiResp.Data {
        // Handle different price formats
        buyPrice := item.BidPrice
        sellPrice := item.AskPrice
        
        // If no bid/ask, use single price
        if buyPrice == 0 && sellPrice == 0 {
            buyPrice = item.Price
            sellPrice = item.Price
        }
        
        price := Price{
            Type:      item.Symbol,
            BuyPrice:  buyPrice,
            SellPrice: sellPrice,
            Unit:      "unit", // Set appropriate unit
            Currency:  item.Currency,
            Source:    y.GetSourceName(),
            Timestamp: time.Now(), // Or parse item.Timestamp
        }
        
        prices = append(prices, price)
    }
    
    return prices, nil
}
```

### Step 2: Test Your Source

Now you can use your source directly:

```go
// Create your source
yourSource := NewYourSource("your-api-key")

// Use with service
service := pricetracker.NewPriceService()
service.AddSource(yourSource)

// Get prices
prices, err := service.GetPricesFromSource("your-source")
```

### Step 3: Unit Tests

Create a test file `services/price-tracker/your-source_test.go`:

```go
package pricetracker

import (
    "testing"
)

func TestYourSource_GetSourceName(t *testing.T) {
    source := NewYourSource("test-key")
    
    name := source.GetSourceName()
    if name != "your-source" {
        t.Errorf("Expected 'your-source', got '%s'", name)
    }
}

func TestYourSource_GetPrices(t *testing.T) {
    if testing.Short() {
        t.Skip("Skipping integration test in short mode")
    }
    
    source := NewYourSource("your-test-api-key") // Use test API key
    
    prices, err := source.GetPrices()
    if err != nil {
        t.Fatalf("Failed to get prices: %v", err)
    }
    
    if len(prices) == 0 {
        t.Error("Expected at least one price")
    }
    
    for _, price := range prices {
        if price.Source != "your-source" {
            t.Errorf("Expected source 'your-source', got '%s'", price.Source)
        }
        
        if price.Currency == "" {
            t.Error("Price currency should not be empty")
        }
    }
}

func TestYourSource_InvalidConfig(t *testing.T) {
    source := NewYourSource("") // Empty API key
    
    _, err := source.GetPrices()
    if err == nil {
        t.Error("Expected error for empty api_key")
    }
}
```

Run tests:
```bash
go test ./services/price-tracker -v
```

### Step 4: Integration Test

Test with the full application:

```go
// In main.go or test file
func main() {
    service := pricetracker.NewPriceService()
    
    // Add your new source
    yourSource := pricetracker.NewYourSource("your-real-api-key")
    service.AddSource(yourSource)
    
    // Test fetching prices
    prices, err := service.GetPricesFromSource("your-source")
    if err != nil {
        log.Fatalf("Failed to get prices: %v", err)
    }
    
    fmt.Printf("Got %d prices from your-source\n", len(prices))
}
```

## 📋 Source Examples

### Example 1: Cryptocurrency Exchange (Binance-style)

```go
type BinanceSource struct {
    apiKey    string
    secretKey string
    client    *http.Client
}

func (b *BinanceSource) GetPrices() ([]Price, error) {
    url := "https://api.binance.com/api/v3/ticker/24hr"
    
    resp, err := b.client.Get(url)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()
    
    var tickers []struct {
        Symbol   string `json:"symbol"`
        BidPrice string `json:"bidPrice"`
        AskPrice string `json:"askPrice"`
    }
    
    if err := json.NewDecoder(resp.Body).Decode(&tickers); err != nil {
        return nil, err
    }
    
    var prices []Price
    for _, ticker := range tickers {
        bidPrice, _ := strconv.ParseFloat(ticker.BidPrice, 64)
        askPrice, _ := strconv.ParseFloat(ticker.AskPrice, 64)
        
        prices = append(prices, Price{
            Type:      ticker.Symbol,
            BuyPrice:  askPrice, // You buy at ask price
            SellPrice: bidPrice, // You sell at bid price
            Unit:      "1",
            Currency:  "USDT",
            Source:    "binance",
            Timestamp: time.Now(),
        })
    }
    
    return prices, nil
}
```

### Example 2: REST API with Authentication

```go
type AuthenticatedSource struct {
    apiKey     string
    authToken  string
    client     *http.Client
    tokenExpiry time.Time
}

func (a *AuthenticatedSource) GetPrices() ([]Price, error) {
    // Check if token needs refresh
    if time.Now().After(a.tokenExpiry) {
        if err := a.refreshToken(); err != nil {
            return nil, err
        }
    }
    
    req, err := http.NewRequest("GET", "https://api.example.com/prices", nil)
    if err != nil {
        return nil, err
    }
    
    req.Header.Set("Authorization", "Bearer "+a.authToken)
    req.Header.Set("X-API-Key", a.apiKey)
    
    // ... rest of implementation
}

func (a *AuthenticatedSource) refreshToken() error {
    // Implement token refresh logic
}
```

### Example 3: WebSocket Source (Advanced)

```go
type WebSocketSource struct {
    wsURL     string
    conn      *websocket.Conn
    pricesCh  chan []Price
    errorsCh  chan error
}

func (w *WebSocketSource) GetPrices() ([]Price, error) {
    // For WebSocket sources, you might want to implement
    // a different pattern with channels or caching
    
    select {
    case prices := <-w.pricesCh:
        return prices, nil
    case err := <-w.errorsCh:
        return nil, err
    case <-time.After(10 * time.Second):
        return nil, fmt.Errorf("timeout waiting for prices")
    }
}
```

## 🔧 Configuration Patterns

### Simple API Key
```go
config := map[string]interface{}{
    "api_key": "your-api-key",
}
```

### Multiple Parameters
```go
config := map[string]interface{}{
    "api_key":    "your-api-key",
    "secret_key": "your-secret",
    "base_url":   "https://api.example.com",
    "timeout":    30,
    "symbols":    []string{"BTC", "ETH", "ADA"},
}
```

### Nested Configuration
```go
config := map[string]interface{}{
    "credentials": map[string]interface{}{
        "username": "your-username",
        "password": "your-password",
    },
    "endpoints": map[string]interface{}{
        "prices": "/v1/prices",
        "auth":   "/v1/auth",
    },
    "options": map[string]interface{}{
        "retry_count": 3,
        "cache_ttl":   300,
    },
}
```

## 🚨 Error Handling Best Practices

### 1. Specific Error Types
```go
type APIError struct {
    StatusCode int
    Message    string
}

func (e *APIError) Error() string {
    return fmt.Sprintf("API error %d: %s", e.StatusCode, e.Message)
}
```

### 2. Retry Logic
```go
func (s *YourSource) fetchWithRetry(url string, maxRetries int) (*http.Response, error) {
    var lastErr error
    
    for i := 0; i < maxRetries; i++ {
        resp, err := s.client.Get(url)
        if err == nil && resp.StatusCode == 200 {
            return resp, nil
        }
        
        if err != nil {
            lastErr = err
        } else {
            lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
            resp.Body.Close()
        }
        
        // Exponential backoff
        time.Sleep(time.Duration(1<<i) * time.Second)
    }
    
    return nil, fmt.Errorf("failed after %d retries: %w", maxRetries, lastErr)
}
```

### 3. Validation
```go
func (s *YourSource) validatePrice(price *Price) error {
    if price.BuyPrice < 0 {
        return fmt.Errorf("invalid buy price: %f", price.BuyPrice)
    }
    
    if price.SellPrice < 0 {
        return fmt.Errorf("invalid sell price: %f", price.SellPrice)
    }
    
    if price.Currency == "" {
        return fmt.Errorf("currency is required")
    }
    
    return nil
}
```

## 📊 Testing Strategies

### Unit Tests
```bash
# Test individual methods
go test -v -run TestYourSource_GetSourceName ./services/price-tracker

# Test with coverage
go test -v -cover ./services/price-tracker
```

### Integration Tests
```bash
# Skip integration tests in short mode
go test -short ./services/price-tracker

# Run only integration tests
go test -v -run Integration ./services/price-tracker
```

### Mock Testing
```go
type MockHTTPClient struct {
    DoFunc func(req *http.Request) (*http.Response, error)
}

func (m *MockHTTPClient) Do(req *http.Request) (*http.Response, error) {
    return m.DoFunc(req)
}

func TestYourSource_MockedAPI(t *testing.T) {
    mockResponse := `{"status":"success","data":[{"symbol":"BTC","price":50000}]}`
    
    mockClient := &MockHTTPClient{
        DoFunc: func(req *http.Request) (*http.Response, error) {
            return &http.Response{
                StatusCode: 200,
                Body:       io.NopCloser(strings.NewReader(mockResponse)),
            }, nil
        },
    }
    
    source := &YourSource{client: mockClient}
    prices, err := source.GetPrices()
    
    assert.NoError(t, err)
    assert.Len(t, prices, 1)
    assert.Equal(t, "BTC", prices[0].Type)
}
```

## 📚 Documentation

### Update API Documentation
Add your source to `docs/api-reference.md`:

```markdown
#### YourSource
Description of your price source.

**Configuration:**
- `api_key`: Your API key (required)
- `base_url`: API base URL (optional, default: https://api.example.com)

**Supported Assets:**
- List supported assets/commodities

**Example:**
```go
config := map[string]interface{}{
    "api_key": "your-api-key",
}
```

### Update Main Documentation
Add to `README.md`:

```markdown
### Supported Sources
- **Doji**: Vietnamese gold prices
- **YourSource**: Description of your source
```

## 🚀 Advanced Features

### Rate Limiting
```go
type RateLimitedSource struct {
    source  PriceSource
    limiter *rate.Limiter
}

func (r *RateLimitedSource) GetPrices() ([]Price, error) {
    if err := r.limiter.Wait(context.Background()); err != nil {
        return nil, err
    }
    
    return r.source.GetPrices()
}
```

### Caching
```go
type CachedSource struct {
    source PriceSource
    cache  map[string]CacheEntry
    ttl    time.Duration
}

type CacheEntry struct {
    Prices    []Price
    Timestamp time.Time
}

func (c *CachedSource) GetPrices() ([]Price, error) {
    key := c.source.GetSourceName()
    
    if entry, exists := c.cache[key]; exists {
        if time.Since(entry.Timestamp) < c.ttl {
            return entry.Prices, nil
        }
    }
    
    prices, err := c.source.GetPrices()
    if err != nil {
        return nil, err
    }
    
    c.cache[key] = CacheEntry{
        Prices:    prices,
        Timestamp: time.Now(),
    }
    
    return prices, nil
}
```

## 🎯 Checklist

Before submitting your new source:

- [ ] ✅ Implements `PriceSource` interface
- [ ] ✅ Registered in factory
- [ ] ✅ Has unit tests
- [ ] ✅ Has integration tests
- [ ] ✅ Handles errors gracefully
- [ ] ✅ Validates configuration
- [ ] ✅ Documents API requirements
- [ ] ✅ Updates README.md
- [ ] ✅ Updates api-reference.md
- [ ] ✅ Follows naming conventions
- [ ] ✅ Includes example usage

## 🔗 Related Documentation

- [Price Tracker Service](price-tracker.md)
- [API Reference](api-reference.md)
- [Contributing Guidelines](CONTRIBUTING.md)