package pricetracker

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// DefaultKimKhoaURL is the JSON endpoint the kimkhoalegend.vn price page itself polls.
const DefaultKimKhoaURL = "https://kimkhoalegend.vn/gold-price"

// KimKhoaSourceName is the name this source reports; other packages use it to
// recognise Kim Khoa rows.
const KimKhoaSourceName = "Kim Khoa Cam Ranh"

// kimKhoaMinPurity is the lowest gold purity (percent) the source reports.
const kimKhoaMinPurity = 97.0

// kimKhoaMaxBody bounds the response body read from the upstream.
const kimKhoaMaxBody = 1 << 20

// kimKhoaCell matches cell content without crossing a closing tag.
const kimKhoaCell = `((?:[^<]|<[^/])*)`

var (
	kimKhoaRowRe = regexp.MustCompile(`(?s)<tr[^>]*>\s*<td[^>]*>` + kimKhoaCell +
		`</td>\s*<td[^>]*>` + kimKhoaCell +
		`</td>\s*<td[^>]*>` + kimKhoaCell + `</td>\s*</tr>`)
	kimKhoaTagRe    = regexp.MustCompile(`<[^>]*>`)
	kimKhoaNumberRe = regexp.MustCompile(`\d+(?:\.\d+)?`)
	kimKhoaAmountRe = regexp.MustCompile(`^\d{1,3}(,\d{3})*$`)
)

type KimKhoaSource struct {
	url    string
	client *http.Client
}

func NewKimKhoaSource(url string) *KimKhoaSource {
	if url == "" {
		url = DefaultKimKhoaURL
	}

	return &KimKhoaSource{
		url:    url,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (k *KimKhoaSource) GetSourceName() string {
	return KimKhoaSourceName
}

func (k *KimKhoaSource) GetPrices() ([]Price, error) {
	req, err := http.NewRequest("GET", k.url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")

	resp, err := k.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch data from Kim Khoa Legend: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Kim Khoa Legend returned status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, kimKhoaMaxBody))
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	prices, err := parseKimKhoaPrices(body)
	if err != nil {
		return nil, err
	}

	timestamp := time.Now()
	for i := range prices {
		prices[i].Source = k.GetSourceName()
		prices[i].Timestamp = timestamp
	}

	return prices, nil
}

// parseKimKhoaPrices decodes the /gold-price JSON payload, extracts the price
// table rows from data.view and keeps gold products with purity >=
// kimKhoaMinPurity that carry a valid buy and sell price (sell >= buy > 0).
func parseKimKhoaPrices(payload []byte) ([]Price, error) {
	var doc struct {
		Data struct {
			View string `json:"view"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &doc); err != nil {
		return nil, fmt.Errorf("invalid Kim Khoa payload: %w", err)
	}

	rows := kimKhoaRowRe.FindAllStringSubmatch(doc.Data.View, -1)
	if len(rows) == 0 {
		return nil, fmt.Errorf("no price rows found in Kim Khoa payload")
	}

	var prices []Price
	for _, row := range rows {
		name := cleanKimKhoaText(row[1])
		if purity, ok := kimKhoaGoldPurity(name); !ok || purity < kimKhoaMinPurity {
			continue
		}

		buy, sell := parseKimKhoaAmount(row[2]), parseKimKhoaAmount(row[3])
		if buy <= 0 || sell <= 0 || sell < buy {
			continue
		}

		// Values are already thousands of VND per chỉ.
		prices = append(prices, Price{
			Type:      strings.ToUpper(name),
			BuyPrice:  buy,
			SellPrice: sell,
			Unit:      "nghìn/chỉ",
			Currency:  "VND",
		})
	}

	if len(prices) == 0 {
		return nil, fmt.Errorf("no valid gold prices with purity >= %.0f%% in Kim Khoa payload", kimKhoaMinPurity)
	}

	return prices, nil
}

// kimKhoaGoldPurity derives the purity percent from a gold product name.
// A fineness token (999.9 / 9999 → 99.99, 98 → 98, 610 → 61, 41.7 → 41.7)
// wins over a karat label (24K → 100). Weights and counts ("1 chỉ",
// "1.5 lượng", "10 gram") and karat labels outside (0, 24] are ignored.
// Silver rows and rows with neither report false.
func kimKhoaGoldPurity(name string) (float64, bool) {
	lower := strings.ToLower(name)
	if strings.Contains(lower, "bạc") {
		return 0, false
	}

	karat := 0.0
	for _, loc := range kimKhoaNumberRe.FindAllStringIndex(lower, -1) {
		token := lower[loc[0]:loc[1]]
		rest := strings.TrimLeft(lower[loc[1]:], " ")

		if kimKhoaHasUnit(rest, "chỉ", "lượng", "g", "gram", "kg", "cây", "l") {
			continue
		}
		if kimKhoaHasUnit(rest, "k") {
			// "24k" / "24 K" is a karat label ("98 Kim Khoa" is not, and
			// karats above 24 are not karats).
			if k, err := strconv.ParseFloat(token, 64); err == nil && k > 0 && k <= 24 && karat == 0 {
				karat = k
			}
			continue
		}

		digits := strings.ReplaceAll(token, ".", "")
		if len(digits) < 2 {
			continue
		}
		fineness, err := strconv.ParseFloat("0."+digits, 64)
		if err != nil {
			return 0, false
		}
		return fineness * 100, true
	}

	if karat > 0 {
		return karat / 24 * 100, true
	}
	return 0, false
}

// kimKhoaHasUnit reports whether s starts with one of units as a whole word.
func kimKhoaHasUnit(s string, units ...string) bool {
	for _, u := range units {
		if !strings.HasPrefix(s, u) {
			continue
		}
		next, _ := utf8.DecodeRuneInString(s[len(u):])
		if next == utf8.RuneError || !unicode.IsLetter(next) {
			return true
		}
	}
	return false
}

func cleanKimKhoaText(s string) string {
	s = kimKhoaTagRe.ReplaceAllString(s, "")
	return strings.Join(strings.Fields(html.UnescapeString(s)), " ")
}

// parseKimKhoaAmount parses a single amount like "11,670" into 11670;
// anything else (empty, several numbers, stray text, signs) is 0.
func parseKimKhoaAmount(s string) float64 {
	s = cleanKimKhoaText(s)
	if !kimKhoaAmountRe.MatchString(s) {
		return 0
	}
	v, err := strconv.ParseFloat(strings.ReplaceAll(s, ",", ""), 64)
	if err != nil {
		return 0
	}
	return v
}
