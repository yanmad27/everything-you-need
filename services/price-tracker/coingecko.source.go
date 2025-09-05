package pricetracker

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type CoinGeckoSource struct {
	apiUrl string
	client *http.Client
}

type CoinGeckoResponse struct {
	Bitcoin  CoinGeckoPrice `json:"bitcoin"`
	Ethereum CoinGeckoPrice `json:"ethereum"`
}

type CoinGeckoPrice struct {
	USD float64 `json:"usd"`
}

func NewCoinGeckoSource(apiUrl string) *CoinGeckoSource {
	return &CoinGeckoSource{
		apiUrl: apiUrl,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *CoinGeckoSource) GetSourceName() string {
	return "CoinGecko"
}

func (c *CoinGeckoSource) GetPrices() ([]Price, error) {
	resp, err := c.client.Get(c.apiUrl)
	if err != nil {
		return []Price{}, fmt.Errorf("failed to fetch from CoinGecko API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return []Price{}, fmt.Errorf("CoinGecko API returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return []Price{}, fmt.Errorf("failed to read response body: %w", err)
	}

	var coinGeckoResp CoinGeckoResponse
	if err := json.Unmarshal(body, &coinGeckoResp); err != nil {
		return []Price{}, fmt.Errorf("failed to parse CoinGecko response: %w", err)
	}

	var prices []Price
	timestamp := time.Now().UTC()

	if coinGeckoResp.Bitcoin.USD > 0 {
		prices = append(prices, Price{
			Type:      "Bitcoin",
			BuyPrice:  coinGeckoResp.Bitcoin.USD,
			SellPrice: coinGeckoResp.Bitcoin.USD,
			Unit:      "BTC",
			Currency:  "USD",
			Source:    "coingecko",
			Timestamp: timestamp,
		})
	}

	if coinGeckoResp.Ethereum.USD > 0 {
		prices = append(prices, Price{
			Type:      "Ethereum",
			BuyPrice:  coinGeckoResp.Ethereum.USD,
			SellPrice: coinGeckoResp.Ethereum.USD,
			Unit:      "ETH",
			Currency:  "USD",
			Source:    "coingecko",
			Timestamp: timestamp,
		})
	}

	return prices, nil
}