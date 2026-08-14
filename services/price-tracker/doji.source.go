package pricetracker

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// DefaultDojiAPIURL is the price table endpoint behind https://banggia.doji.vn/gold-price.
// The old giavang.doji.vn XML API was retired and now returns HTTP 503.
const DefaultDojiAPIURL = "https://banggia.doji.vn/api/TablePrice/GetTablePrice"

// dojiAESKey decrypts the response payload. The DOJI web app ships this same key
// in its public JS bundle (main bundle, `_k` field), so it is not a secret — it is
// only light obfuscation on an otherwise public endpoint.
const dojiAESKey = "7a4b8c3d1e9f2a5b6c0d4e8f3a7b1c5d9e2f6a0b4c8d3e7f1a5b9c2d6e0f4a8b"

type DojiSource struct {
	apiUrl string
	client *http.Client
}

// DojiResponse wraps the encrypted payload: Data is base64(iv||ciphertext).
type DojiResponse struct {
	Status bool   `json:"status"`
	Data   string `json:"data"`
}

// DojiRow is one row of the decrypted price table.
type DojiRow struct {
	MaterialCode string   `json:"materialCode"`
	MaterialName string   `json:"materialName"`
	BuyIn        *float64 `json:"priceDojiBuyIn"`
	SellOut      *float64 `json:"priceDojiSellOut"`
	Type         string   `json:"type"` // "G" = gold, "S" = silver
	IsActive     *bool    `json:"isActive"`
	UpdateDate   string   `json:"updateDate"`
}

func NewDojiSource(apiUrl string) *DojiSource {
	if apiUrl == "" {
		apiUrl = DefaultDojiAPIURL
	}

	return &DojiSource{
		apiUrl: apiUrl,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (d *DojiSource) GetSourceName() string {
	return "Doji"
}

func (d *DojiSource) GetPrices() ([]Price, error) {
	req, err := http.NewRequest("GET", d.apiUrl, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Referer", "https://banggia.doji.vn/gold-price")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36")

	resp, err := d.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	return d.parseResponse(body)
}

func (d *DojiSource) parseResponse(body []byte) ([]Price, error) {
	var dojiResp DojiResponse
	if err := json.Unmarshal(body, &dojiResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON response: %w", err)
	}

	if !dojiResp.Status || dojiResp.Data == "" {
		return nil, fmt.Errorf("API returned no data")
	}

	plaintext, err := decryptDojiPayload(dojiResp.Data)
	if err != nil {
		return nil, err
	}

	var rows []DojiRow
	if err := json.Unmarshal(plaintext, &rows); err != nil {
		return nil, fmt.Errorf("failed to unmarshal decrypted payload: %w", err)
	}

	timestamp := time.Now()
	prices := make([]Price, 0, len(rows))
	for _, row := range rows {
		// isActive is null for silver rows; only skip rows explicitly disabled.
		if row.IsActive != nil && !*row.IsActive {
			continue
		}
		if row.MaterialName == "" {
			continue
		}

		// Gold is quoted per chỉ, silver per lượng; both in thousands of VND,
		// which matches the other sources (BTMC/Mi Hồng divide by 1000).
		unit := "nghìn/chỉ"
		if row.Type == "S" {
			unit = "nghìn/lượng"
		}

		prices = append(prices, Price{
			Type:      row.MaterialName,
			BuyPrice:  floatOrZero(row.BuyIn),
			SellPrice: floatOrZero(row.SellOut),
			Unit:      unit,
			Currency:  "VND",
			Source:    d.GetSourceName(),
			Timestamp: timestamp,
		})
	}

	if len(prices) == 0 {
		return nil, fmt.Errorf("API returned empty price table")
	}

	return prices, nil
}

// decryptDojiPayload undoes the AES-256-CBC envelope used by banggia.doji.vn:
// base64(iv[16] || ciphertext), PKCS#7 padded.
func decryptDojiPayload(data string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return nil, fmt.Errorf("failed to base64-decode payload: %w", err)
	}

	key, err := hex.DecodeString(dojiAESKey)
	if err != nil {
		return nil, fmt.Errorf("failed to decode decryption key: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	if len(raw) < aes.BlockSize*2 || (len(raw)-aes.BlockSize)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("invalid payload length: %d", len(raw))
	}

	iv, ciphertext := raw[:aes.BlockSize], raw[aes.BlockSize:]
	plaintext := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plaintext, ciphertext)

	return pkcs7Unpad(plaintext, aes.BlockSize)
}

func pkcs7Unpad(data []byte, blockSize int) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("cannot unpad empty payload")
	}

	padding := int(data[len(data)-1])
	if padding == 0 || padding > blockSize || padding > len(data) {
		return nil, fmt.Errorf("invalid padding: %d", padding)
	}

	if !bytes.Equal(data[len(data)-padding:], bytes.Repeat([]byte{byte(padding)}, padding)) {
		return nil, fmt.Errorf("invalid padding bytes")
	}

	return data[:len(data)-padding], nil
}

func floatOrZero(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}
