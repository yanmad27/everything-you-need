package pricetracker

import (
	"encoding/xml"
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

type DataList struct {
	XMLName xml.Name `xml:"DataList"`
	Data    []Data   `xml:"Data"`
}

type Data struct {
	Row string `xml:"row,attr"`
	N1  string `xml:"n_1,attr"`
	K1  string `xml:"k_1,attr"`
	H1  string `xml:"h_1,attr"`
	PB1 string `xml:"pb_1,attr"`
	PS1 string `xml:"ps_1,attr"`
	PT1 string `xml:"pt_1,attr"`
	D1  string `xml:"d_1,attr"`
	N2  string `xml:"n_2,attr"`
	K2  string `xml:"k_2,attr"`
	H2  string `xml:"h_2,attr"`
	PB2 string `xml:"pb_2,attr"`
	PS2 string `xml:"ps_2,attr"`
	PT2 string `xml:"pt_2,attr"`
	D2  string `xml:"d_2,attr"`
	N3  string `xml:"n_3,attr"`
	K3  string `xml:"k_3,attr"`
	H3  string `xml:"h_3,attr"`
	PB3 string `xml:"pb_3,attr"`
	PS3 string `xml:"ps_3,attr"`
	PT3 string `xml:"pt_3,attr"`
	D3  string `xml:"d_3,attr"`
	N4  string `xml:"n_4,attr"`
	K4  string `xml:"k_4,attr"`
	H4  string `xml:"h_4,attr"`
	PB4 string `xml:"pb_4,attr"`
	PS4 string `xml:"ps_4,attr"`
	PT4 string `xml:"pt_4,attr"`
	D4  string `xml:"d_4,attr"`
	N5  string `xml:"n_5,attr"`
	K5  string `xml:"k_5,attr"`
	H5  string `xml:"h_5,attr"`
	PB5 string `xml:"pb_5,attr"`
	PS5 string `xml:"ps_5,attr"`
	PT5 string `xml:"pt_5,attr"`
	D5  string `xml:"d_5,attr"`
	N6  string `xml:"n_6,attr"`
	K6  string `xml:"k_6,attr"`
	H6  string `xml:"h_6,attr"`
	PB6 string `xml:"pb_6,attr"`
	PS6 string `xml:"ps_6,attr"`
	PT6 string `xml:"pt_6,attr"`
	D6  string `xml:"d_6,attr"`
	N7  string `xml:"n_7,attr"`
	K7  string `xml:"k_7,attr"`
	H7  string `xml:"h_7,attr"`
	PB7 string `xml:"pb_7,attr"`
	PS7 string `xml:"ps_7,attr"`
	PT7 string `xml:"pt_7,attr"`
	D7  string `xml:"d_7,attr"`
}

func NewBTMCSource(apiUrl string) *BTMCSource {
	return &BTMCSource{
		apiUrl: apiUrl,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (v *BTMCSource) GetSourceName() string {
	return "BTMC"
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
	var dataList DataList
	if err := xml.Unmarshal(body, &dataList); err != nil {
		return nil, fmt.Errorf("failed to unmarshal XML: %w", err)
	}

	var prices []Price
	for _, data := range dataList.Data {
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
