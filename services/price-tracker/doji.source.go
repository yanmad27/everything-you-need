package pricetracker

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type DojiSource struct {
	apiUrl string
	client *http.Client
}

type DojiResponse struct {
	XMLName     xml.Name    `xml:"GoldList"`
	DGPList     DGPList     `xml:"DGPlist"`
	JewelryList JewelryList `xml:"JewelryList"`
	IGPList     IGPList     `xml:"IGPList"`
	Source      string      `xml:"Source"`
}

type DGPList struct {
	DateTime string    `xml:"DateTime"`
	Rows     []DojiRow `xml:"Row"`
}

type JewelryList struct {
	DateTime string    `xml:"DateTime"`
	Rows     []DojiRow `xml:"Row"`
}

type IGPList struct {
	DateTime string    `xml:"DateTime"`
	Rows     []DojiRow `xml:"Row"`
}

type DojiRow struct {
	Name string `xml:"Name,attr"`
	Key  string `xml:"Key,attr"`
	Sell string `xml:"Sell,attr"`
	Buy  string `xml:"Buy,attr"`
}

func NewDojiSource(apiUrl string) *DojiSource {
	return &DojiSource{
		apiUrl: apiUrl,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (d *DojiSource) GetSourceName() string {
	return "doji"
}

func (d *DojiSource) GetPrices() ([]Price, error) {

	req, err := http.NewRequest("GET", d.apiUrl, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := d.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	return d.parseResponse(body)
}

func (d *DojiSource) parseResponse(body []byte) ([]Price, error) {
	var dojiResp DojiResponse
	if err := xml.Unmarshal(body, &dojiResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal XML response: %w", err)
	}

	var prices []Price

	// Parse DGP (Domestic Gold Price) list
	for _, row := range dojiResp.DGPList.Rows {
		price, err := d.parseRowToPrice(row, "lượng")
		if err != nil {
			continue
		}
		prices = append(prices, price)
	}

	// Parse Jewelry list
	for _, row := range dojiResp.JewelryList.Rows {
		unit := "chỉ"
		if strings.Contains(strings.ToLower(row.Name), "nguyên liệu") {
			unit = "nghìn/chỉ"
		}
		price, err := d.parseRowToPrice(row, unit)
		if err != nil {
			continue
		}
		prices = append(prices, price)
	}

	// Parse IGP (International Gold Price) list - USD/VND exchange rate
	for _, row := range dojiResp.IGPList.Rows {
		if row.Key == "usdvnd" {
			price, err := d.parseRowToPrice(row, "VND")
			if err != nil {
				continue
			}
			prices = append(prices, price)
		}
	}

	prices = filterPrices(prices)
	return prices, nil
}

func filterPrices(prices []Price) []Price {
	var filteredPrices []Price
	for _, price := range prices {
		if strings.Contains(strings.ToLower(price.Type), "9999") {
			filteredPrices = append(filteredPrices, price)
		}
	}
	return filteredPrices
}

func (d *DojiSource) parseRowToPrice(row DojiRow, unit string) (Price, error) {
	buyPrice, err := d.parsePrice(row.Buy)
	if err != nil {
		buyPrice = 0 // Set to 0 if parsing fails
	}

	sellPrice, err := d.parsePrice(row.Sell)
	if err != nil {
		sellPrice = 0 // Set to 0 if parsing fails
	}

	price := Price{
		Type:      row.Name,
		BuyPrice:  buyPrice,
		SellPrice: sellPrice,
		Unit:      unit,
		Currency:  "VND",
		Source:    d.GetSourceName(),
		Timestamp: time.Now(),
	}

	return price, nil
}

func (d *DojiSource) parsePrice(priceStr string) (float64, error) {
	cleanPrice := strings.ReplaceAll(priceStr, ",", "")
	cleanPrice = strings.ReplaceAll(cleanPrice, ".", "")
	cleanPrice = strings.TrimSpace(cleanPrice)

	if cleanPrice == "" || cleanPrice == "-" {
		return 0, nil
	}

	price, err := strconv.ParseFloat(cleanPrice, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse price %s: %w", priceStr, err)
	}

	return price, nil
}
