package pricetracker

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

type PriceHistory struct {
	Timestamp time.Time          `json:"timestamp"`
	Prices    map[string][]Price `json:"prices"`
}

type PriceChange struct {
	Price         Price   `json:"price"`
	PrevBuyPrice  float64 `json:"prev_buy_price"`
	PrevSellPrice float64 `json:"prev_sell_price"`
	BuyChange     float64 `json:"buy_change_percent"`
	SellChange    float64 `json:"sell_change_percent"`
}

type PriceHistoryService struct {
	historyFilePath string
}

func NewPriceHistoryService() *PriceHistoryService {
	// Create logs directory if it doesn't exist
	logsDir := "logs"
	if _, err := os.Stat(logsDir); os.IsNotExist(err) {
		os.MkdirAll(logsDir, 0755)
	}

	return &PriceHistoryService{
		historyFilePath: filepath.Join(logsDir, "price_history.json"),
	}
}

func (p *PriceHistoryService) SavePriceHistory(prices map[string][]Price) error {
	history := PriceHistory{
		Timestamp: time.Now(),
		Prices:    prices,
	}

	data, err := json.MarshalIndent(history, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal price history: %w", err)
	}

	err = os.WriteFile(p.historyFilePath, data, 0644)
	if err != nil {
		return fmt.Errorf("failed to write price history: %w", err)
	}

	return nil
}

func (p *PriceHistoryService) GetLastPriceHistory() (*PriceHistory, error) {
	data, err := os.ReadFile(p.historyFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // No history file exists yet
		}
		return nil, fmt.Errorf("failed to read price history: %w", err)
	}

	var history PriceHistory
	err = json.Unmarshal(data, &history)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal price history: %w", err)
	}

	return &history, nil
}

func (p *PriceHistoryService) ComparePrices(currentPrices, previousPrices map[string][]Price) map[string][]PriceChange {
	changes := make(map[string][]PriceChange)

	for source, currentSourcePrices := range currentPrices {
		prevSourcePrices, exists := previousPrices[source]
		if !exists {
			// No previous data for this source, treat all as new
			for _, price := range currentSourcePrices {
				changes[source] = append(changes[source], PriceChange{
					Price:         price,
					PrevBuyPrice:  0,
					PrevSellPrice: 0,
					BuyChange:     0,
					SellChange:    0,
				})
			}
			continue
		}

		// Create a map for quick lookup of previous prices by type
		prevPriceMap := make(map[string]Price)
		for _, prevPrice := range prevSourcePrices {
			prevPriceMap[prevPrice.Type] = prevPrice
		}

		for _, currentPrice := range currentSourcePrices {
			prevPrice, exists := prevPriceMap[currentPrice.Type]
			
			var buyChange, sellChange float64
			var prevBuy, prevSell float64
			
			if exists {
				prevBuy = prevPrice.BuyPrice
				prevSell = prevPrice.SellPrice
				
				// Calculate percentage change
				if prevPrice.BuyPrice > 0 {
					buyChange = ((currentPrice.BuyPrice - prevPrice.BuyPrice) / prevPrice.BuyPrice) * 100
				}
				if prevPrice.SellPrice > 0 {
					sellChange = ((currentPrice.SellPrice - prevPrice.SellPrice) / prevPrice.SellPrice) * 100
				}
			}

			changes[source] = append(changes[source], PriceChange{
				Price:         currentPrice,
				PrevBuyPrice:  prevBuy,
				PrevSellPrice: prevSell,
				BuyChange:     buyChange,
				SellChange:    sellChange,
			})
		}
	}

	return changes
}

func (p *PriceHistoryService) RotateHistoryFiles() error {
	// Keep only the last 30 history files (optional feature)
	logsDir := "logs"
	
	files, err := os.ReadDir(logsDir)
	if err != nil {
		return err
	}

	var historyFiles []fs.FileInfo
	for _, file := range files {
		if filepath.Ext(file.Name()) == ".json" && file.Name() != "price_history.json" {
			info, err := file.Info()
			if err == nil {
				historyFiles = append(historyFiles, info)
			}
		}
	}

	// If we have more than 30 files, delete the oldest ones
	if len(historyFiles) > 30 {
		// Sort by modification time and delete oldest
		// This is a simplified version - could be enhanced
	}

	return nil
}