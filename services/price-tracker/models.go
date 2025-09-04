package pricetracker

import "time"

// Price represents unified price data from any source
type Price struct {
	Type       string    `json:"type"`        // Price type identifier (e.g., "SJC 1L", "Bitcoin")
	BuyPrice   float64   `json:"buy_price"`   // Buying price (consumer perspective)
	SellPrice  float64   `json:"sell_price"`  // Selling price (consumer perspective)
	Unit       string    `json:"unit"`        // Unit of measurement (e.g., "chỉ", "gram", "BTC")
	Currency   string    `json:"currency"`    // Currency code (e.g., "VND", "USD")
	Source     string    `json:"source"`      // Source identifier (e.g., "doji", "binance")
	Timestamp  time.Time `json:"timestamp"`   // When the price was fetched (UTC)
}

// PriceSource defines the contract for all price data sources
type PriceSource interface {
	// GetPrices fetches current price data from the source
	// Returns empty slice (not nil) if no prices are available
	GetPrices() ([]Price, error)
	
	// GetSourceName returns a unique identifier for this source
	// This name is used for configuration and logging
	GetSourceName() string
}