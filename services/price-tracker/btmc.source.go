package pricetracker

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

type BTMCSource struct {
	apiUrl string
	client *http.Client
}

type BTMCResponse struct {
	DataList struct {
		Data []map[string]string `json:"Data"`
	} `json:"DataList"`
}

func NewBTMCSource(apiUrl string) *BTMCSource {
	return &BTMCSource{
		apiUrl: apiUrl,
		client: &http.Client{Timeout: 60 * time.Second},
	}
}

func (v *BTMCSource) GetSourceName() string {
	return "Bảo Tín Minh Châu"
}

func (v *BTMCSource) GetPrices() ([]Price, error) {
	resp, err := v.client.Get(v.apiUrl)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch data from BTMC: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var response BTMCResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON: %w", err)
	}

	var prices []Price
	seenNames := make(map[string]bool)
	for _, data := range response.DataList.Data {
		row, hasRow := data["@row"]
		if !hasRow {
			continue
		}

		suffix := "_" + row
		name := data["@n"+suffix]
		if name == "" {
			continue
		}

		if seenNames[name] {
			continue
		}
		seenNames[name] = true

		buyPriceStr := data["@pb"+suffix]
		if buyPriceStr == "" {
			continue
		}

		buyPrice, err := strconv.ParseFloat(buyPriceStr, 64)
		if err != nil {
			continue
		}

		sellPriceStr := data["@ps"+suffix]
		var sellPrice float64
		if sellPriceStr == "" || sellPriceStr == "0" {
			sellPrice = buyPrice
		} else {
			sellPrice, err = strconv.ParseFloat(sellPriceStr, 64)
			if err != nil {
				sellPrice = buyPrice
			}
		}

		// Divide by 1000 to match Doji format (thousands VND)
		prices = append(prices, Price{
			Type:      name,
			BuyPrice:  buyPrice / 1000,
			SellPrice: sellPrice / 1000,
			Currency:  "VND",
		})
	}

	return prices, nil
}
