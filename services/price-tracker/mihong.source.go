package pricetracker

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"time"
)

type MihongSource struct {
	baseUrl string
	apiUrl  string
	client  *http.Client
}

type MihongResponse struct {
	Success bool          `json:"success"`
	Data    []MihongPrice `json:"data"`
}

type MihongPrice struct {
	BuyingPrice       float64 `json:"buyingPrice"`
	SellingPrice      float64 `json:"sellingPrice"`
	Code              string  `json:"code"`
	SellChange        float64 `json:"sellChange"`
	SellChangePercent float64 `json:"sellChangePercent"`
	BuyChange         float64 `json:"buyChange"`
	BuyChangePercent  float64 `json:"buyChangePercent"`
	DateTime          string  `json:"dateTime"`
}

func NewMihongSource(baseUrl, apiUrl string) *MihongSource {
	// Create cookie jar for session management
	jar, _ := cookiejar.New(nil)

	// Create TLS config that skips certificate verification for problematic certificates
	tlsConfig := &tls.Config{
		InsecureSkipVerify: true,
	}

	// Create transport with custom TLS config
	transport := &http.Transport{
		TLSClientConfig: tlsConfig,
	}

	return &MihongSource{
		baseUrl: baseUrl,
		apiUrl:  apiUrl,
		client: &http.Client{
			Timeout:   30 * time.Second,
			Jar:       jar,
			Transport: transport,
		},
	}
}

func (m *MihongSource) GetSourceName() string {
	return "Mi Hồng"
}

func (m *MihongSource) GetPrices() ([]Price, error) {
	// Step 1: Visit the main page to establish session and get cookies
	req, err := http.NewRequest("GET", m.baseUrl, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create base request: %w", err)
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to visit base page: %w", err)
	}
	resp.Body.Close()

	// Step 2: Make the API request with cookies
	req, err = http.NewRequest("GET", m.apiUrl, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create API request: %w", err)
	}

	// Set required headers
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Referer", m.baseUrl)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")

	resp, err = m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch data from Mi Hồng: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var mihongResp MihongResponse
	if err := json.Unmarshal(body, &mihongResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON: %w", err)
	}

	if !mihongResp.Success {
		return nil, fmt.Errorf("API returned success=false")
	}

	var prices []Price
	for _, mihongPrice := range mihongResp.Data {
		// Create descriptive type name based on code
		var typeName string
		switch mihongPrice.Code {
		case "SJC":
			typeName = "VÀNG MIẾNG SJC"
		case "999":
			typeName = "VÀNG MIẾNG 9999"
		case "985":
			typeName = "VÀNG MIẾNG 985"
		case "980":
			typeName = "VÀNG MIẾNG 980"
		case "950":
			typeName = "VÀNG MIẾNG 950"
		case "750":
			typeName = "VÀNG MIẾNG 750"
		case "680":
			typeName = "VÀNG MIẾNG 680"
		case "610":
			typeName = "VÀNG MIẾNG 610"
		case "580":
			typeName = "VÀNG MIẾNG 580"
		case "410":
			typeName = "VÀNG MIẾNG 410"
		default:
			typeName = fmt.Sprintf("VÀNG MIẾNG %s", mihongPrice.Code)
		}

		// Handle cases where selling price might be 0
		sellPrice := mihongPrice.SellingPrice
		if sellPrice == 0 {
			sellPrice = mihongPrice.BuyingPrice
		}

		prices = append(prices, Price{
			Type:      typeName,
			BuyPrice:  mihongPrice.BuyingPrice,
			SellPrice: sellPrice,
			Currency:  "VND",
		})
	}

	return prices, nil
}
