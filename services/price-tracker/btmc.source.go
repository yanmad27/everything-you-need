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
	DataList DataList `json:"DataList"`
}

type DataList struct {
	Data []DataEntry `json:"Data"`
}

type DataEntry struct {
	Row string `json:"@row"`
	N1  string `json:"@n_1"`
	K1  string `json:"@k_1"`
	H1  string `json:"@h_1"`
	PB1 string `json:"@pb_1"`
	PS1 string `json:"@ps_1"`
	PT1 string `json:"@pt_1"`
	D1  string `json:"@d_1"`
	N2  string `json:"@n_2"`
	K2  string `json:"@k_2"`
	H2  string `json:"@h_2"`
	PB2 string `json:"@pb_2"`
	PS2 string `json:"@ps_2"`
	PT2 string `json:"@pt_2"`
	D2  string `json:"@d_2"`
	N3  string `json:"@n_3"`
	K3  string `json:"@k_3"`
	H3  string `json:"@h_3"`
	PB3 string `json:"@pb_3"`
	PS3 string `json:"@ps_3"`
	PT3 string `json:"@pt_3"`
	D3  string `json:"@d_3"`
	N4  string `json:"@n_4"`
	K4  string `json:"@k_4"`
	H4  string `json:"@h_4"`
	PB4 string `json:"@pb_4"`
	PS4 string `json:"@ps_4"`
	PT4 string `json:"@pt_4"`
	D4  string `json:"@d_4"`
	N5  string `json:"@n_5"`
	K5  string `json:"@k_5"`
	H5  string `json:"@h_5"`
	PB5 string `json:"@pb_5"`
	PS5 string `json:"@ps_5"`
	PT5 string `json:"@pt_5"`
	D5  string `json:"@d_5"`
	N6  string `json:"@n_6"`
	K6  string `json:"@k_6"`
	H6  string `json:"@h_6"`
	PB6 string `json:"@pb_6"`
	PS6 string `json:"@ps_6"`
	PT6 string `json:"@pt_6"`
	D6  string `json:"@d_6"`
	N7  string `json:"@n_7"`
	K7  string `json:"@k_7"`
	H7  string `json:"@h_7"`
	PB7 string `json:"@pb_7"`
	PS7 string `json:"@ps_7"`
	PT7 string `json:"@pt_7"`
	D7  string `json:"@d_7"`
}

func NewBTMCSource(apiUrl string) *BTMCSource {
	return &BTMCSource{
		apiUrl: apiUrl,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (v *BTMCSource) GetSourceName() string {
	return "Bảo Tín Minh Châu"
}

func (v *BTMCSource) GetPrices() ([]Price, error) {
	resp, err := v.client.Get(v.apiUrl)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch data from VietGold: %w", err)
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
	for _, data := range response.DataList.Data {
		entries := []struct {
			name, pb, ps string
		}{
			{data.N1, data.PB1, data.PS1},
			{data.N2, data.PB2, data.PS2},
			{data.N3, data.PB3, data.PS3},
			{data.N4, data.PB4, data.PS4},
			{data.N5, data.PB5, data.PS5},
			{data.N6, data.PB6, data.PS6},
			{data.N7, data.PB7, data.PS7},
		}

		for _, entry := range entries {
			if entry.name == "" || entry.pb == "" {
				continue
			}

			pb, err := strconv.ParseFloat(entry.pb, 64)
			if err != nil {
				continue
			}

			var ps float64
			if entry.ps == "" || entry.ps == "0" {
				ps = pb
			} else {
				ps, err = strconv.ParseFloat(entry.ps, 64)
				if err != nil {
					ps = pb
				}
			}

			prices = append(prices, Price{
				Type:      entry.name,
				BuyPrice:  pb,
				SellPrice: ps,
				Currency:  "VND",
			})
		}
	}

	return prices, nil
}
