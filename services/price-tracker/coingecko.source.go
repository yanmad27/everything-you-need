package pricetracker

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type CoinGeckoSource struct {
	apiUrl string
	client *http.Client
}

type CoinGeckoResponse map[string]CoinGeckoPrice

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

	coinNameMap := map[string]string{
		"bitcoin":     "Bitcoin",
		"ethereum":    "Ethereum",
		"binancecoin": "BNB",
	}

	coinUnitMap := map[string]string{
		"bitcoin":     "BTC",
		"ethereum":    "ETH",
		"binancecoin": "BNB",
	}

	for coinId, priceData := range coinGeckoResp {
		if priceData.USD > 0 {
			displayName := coinNameMap[coinId]
			if displayName == "" {
				displayName = strings.Title(coinId)
			}

			unit := coinUnitMap[coinId]
			if unit == "" {
				unit = strings.ToUpper(coinId[:3])
			}

			prices = append(prices, Price{
				Type:      displayName,
				BuyPrice:  priceData.USD,
				SellPrice: priceData.USD,
				Unit:      unit,
				Currency:  "USD",
				Source:    "coingecko",
				Timestamp: timestamp,
			})
		}
	}

	return prices, nil
}
