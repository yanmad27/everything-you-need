package pricetracker

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// Fixture: testdata/kimkhoa/gold-price.json, a plain GET of
// https://kimkhoalegend.vn/gold-price captured 2026-11-26T08:19:02Z
// (payload last_update "14:45 26/11/2026"). See testdata/kimkhoa/CAPTURE.txt.
func kimKhoaFixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/kimkhoa/gold-price.json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func kimKhoaPayload(rows ...[3]string) []byte {
	var sb strings.Builder
	sb.WriteString(`<table><thead><tr><th>Loại vàng</th><th>Mua vào</th><th>Bán ra</th></tr></thead><tbody>`)
	for _, r := range rows {
		fmt.Fprintf(&sb, `<tr><td>%s</td><td class="buy-price">%s</td><td class="sell-price">%s</td></tr>`, r[0], r[1], r[2])
	}
	sb.WriteString(`</tbody></table>`)
	b, _ := json.Marshal(sb.String())
	return []byte(`{"message":"success","data":{"view":` + string(b) + `,"last_update":"14:45 26/11/2026"}}`)
}

func TestKimKhoaSource_GetSourceName(t *testing.T) {
	if got := NewKimKhoaSource("").GetSourceName(); got != KimKhoaSourceName {
		t.Fatalf("got %q", got)
	}
}

func TestParseKimKhoaPrices_Fixture(t *testing.T) {
	prices, err := parseKimKhoaPrices(kimKhoaFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	want := []Price{
		{Type: "KIM KHOA 999.9 ÉP VĨ", BuyPrice: 11670, SellPrice: 11800},
		{Type: "KIM KHOA 999.9", BuyPrice: 11650, SellPrice: 11800},
		{Type: "NỮ TRANG 98%", BuyPrice: 11400, SellPrice: 11700},
		{Type: "NHẪN 97%", BuyPrice: 11250, SellPrice: 11400},
	}
	if len(prices) != len(want) {
		t.Fatalf("got %d rows, want %d: %+v", len(prices), len(want), prices)
	}
	for i, w := range want {
		g := prices[i]
		if g.Type != w.Type || g.BuyPrice != w.BuyPrice || g.SellPrice != w.SellPrice || g.Unit != "nghìn/chỉ" || g.Currency != "VND" {
			t.Errorf("row %d: got %+v, want %+v", i, g, w)
		}
	}
}

func TestParseKimKhoaPrices_DropsInvalidRows(t *testing.T) {
	good := [3]string{"KIM KHOA 999.9", "11,650", "11,800"}
	bad := [][3]string{
		{"Vàng 999.9 A", "11,650", ""},
		{"Vàng 999.9 B", "11,650", "0"},
		{"Vàng 999.9 C", "11,650", "-11,800"},
		{"Vàng 999.9 D", "-11,650", "11,800"},
		{"Vàng 999.9 E", "11,650", "abc"},
		{"Vàng 999.9 F", "11,650", "11.800"},
		{"Vàng 999.9 G", "11,650", "11,800 11,900"},
		{"Vàng 999.9 H", "0", "11,800"},
		{"Vàng 999.9 I", "11,800", "11,650"},
		{"Vàng 999.9 J", "11,670,000", "11,800,000"},
		{"Vàng 999.9 K", "11,650", "11,800,000"},
		{"Vàng 999.9 L", "999", "1,100"},
		{"Vàng 999.9 M", "11,650", "100,001"},
		{"Nhẫn 96%", "11,000", "11,100"},
		{"Bạc 999.9", "1,000", "1,100"},
	}
	for _, b := range bad {
		prices, err := parseKimKhoaPrices(kimKhoaPayload(good, b))
		if err != nil {
			t.Fatalf("%v: %v", b, err)
		}
		if len(prices) != 1 || prices[0].Type != "KIM KHOA 999.9" {
			t.Errorf("%v must be dropped, got %+v", b, prices)
		}
	}
}

func TestParseKimKhoaPrices_Errors(t *testing.T) {
	cases := map[string][]byte{
		"all invalid":     kimKhoaPayload([3]string{"Vàng 999.9", "11,650", "0"}, [3]string{"Nhẫn 97%", "", "11,400"}),
		"all full-dong":   kimKhoaPayload([3]string{"Vàng 999.9", "11,670,000", "11,800,000"}, [3]string{"Nhẫn 97%", "11,250,000", "11,400,000"}),
		"no rows":         kimKhoaPayload(),
		"changed schema":  []byte(`{"message":"success","data":{"rows":[{"name":"Vàng 999.9","buy":11650,"sell":11800}]}}`),
		"missing data":    []byte(`{"message":"success"}`),
		"not json":        []byte(`<html>blocked</html>`),
		"short rows only": []byte(`{"data":{"view":"<table><tr><td>Vàng 999.9</td><td>11,650</td></tr></table>"}}`),
		"empty":           nil,
		"view not string": []byte(`{"data":{"view":42}}`),
	}
	for name, body := range cases {
		prices, err := parseKimKhoaPrices(body)
		if err == nil || len(prices) != 0 {
			t.Errorf("%s: want error and no prices, got %+v, %v", name, prices, err)
		}
	}
}

func TestParseKimKhoaPrices_SanitizesName(t *testing.T) {
	long := "Vàng 999.9 " + strings.Repeat("x", 200)
	prices, err := parseKimKhoaPrices(kimKhoaPayload(
		[3]string{"KIM KHOA 999.9 [x](http://evil) *b* _i_ `c`", "11,650", "11,800"},
		[3]string{long, "11,650", "11,800"}))
	if err != nil || len(prices) != 2 {
		t.Fatalf("got %+v, %v", prices, err)
	}
	if want := "KIM KHOA 999.9 X(HTTP://EVIL) B I C"; prices[0].Type != want {
		t.Errorf("got %q, want %q", prices[0].Type, want)
	}
	if n := len([]rune(prices[1].Type)); n > 100 {
		t.Errorf("name not capped: %d runes", n)
	}
}

func TestParseKimKhoaPrices_PartialLayoutChange(t *testing.T) {
	view := `<table><tr><td>Vàng 999.9 short</td><td>11,650</td></tr><tr><td>KIM KHOA 999.9</td><td>11,650</td><td>11,800</td></tr></table>`
	b, _ := json.Marshal(view)
	prices, err := parseKimKhoaPrices([]byte(`{"data":{"view":` + string(b) + `}}`))
	if err != nil || len(prices) != 1 || prices[0].Type != "KIM KHOA 999.9" {
		t.Fatalf("got %+v, %v", prices, err)
	}
}

func TestParseKimKhoaAmount(t *testing.T) {
	cases := map[string]float64{
		"11,670": 11670, " 11,800 ": 11800, "950": 950, "1,234,567": 0, "999,999": 999999,
		"": 0, "0": 0, "-5": 0, "11.670": 0, "11,67": 0, "1,2345": 0, "abc": 0, "11,670 11,800": 0, "11,670đ": 0,
	}
	for in, want := range cases {
		if got := parseKimKhoaAmount(in); got != want {
			t.Errorf("%q: got %v, want %v", in, got, want)
		}
	}
}

func TestKimKhoaSource_GetPrices_Server(t *testing.T) {
	fixture := kimKhoaFixture(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/err" {
			http.Error(w, "boom", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(fixture)
	}))
	defer srv.Close()

	prices, err := NewKimKhoaSource(srv.URL).GetPrices()
	if err != nil || len(prices) != 4 {
		t.Fatalf("got %d prices, err %v", len(prices), err)
	}
	for _, p := range prices {
		if p.Source != KimKhoaSourceName || p.Timestamp.IsZero() || p.SellPrice <= 0 {
			t.Errorf("bad row %+v", p)
		}
	}
	if _, err := NewKimKhoaSource(srv.URL + "/err").GetPrices(); err == nil {
		t.Fatal("non-200 must error")
	}
}

func TestKimKhoaGoldPurity(t *testing.T) {
	cases := map[string]struct {
		purity float64
		ok     bool
	}{
		"Vàng 999.9":              {99.99, true},
		"Vàng 98 Kim Khoa 24k":    {98, true},
		"Vàng 97":                 {97, true},
		"Vàng 96":                 {96, true},
		"Vàng NL Ngoài 999":       {99.9, true},
		"Vàng 41.7 W (Trắng)":     {41.7, true},
		"Vàng Kim Khoa 610":       {61, true},
		"Vàng Đúc":                {0, false},
		"Bạc Kim Khoa 1kilo Thỏi": {0, false},
		"Bạc miếng 1L":            {0, false},
		"Trang Sức Bạc Kim Khoa":  {0, false},
		"Vàng 1 chỉ 999.9":        {99.99, true},
		"Vàng 5 chỉ 999.9":        {99.99, true},
		"Vàng 1.5 chỉ 999.9":      {99.99, true},
		"Vàng 1 Lượng 9999":       {99.99, true},
		"Vàng 10 gram 9999":       {99.99, true},
		"Vàng 24K":                {100, true},
		"Vàng 18K":                {75, true},
		"Vàng 23K":                {23.0 / 24 * 100, true},
		"Vàng 24 K 98":            {98, true},
		"Vàng 96 K":               {0, false},
		"Vàng 98k":                {0, false},
		"Vàng 1 chỉ":              {0, false},
		"KIM KHOA 999.9 ép vĩ":    {99.99, true},
		"Nữ trang 98%":            {98, true},
		"Nhẫn 97%":                {97, true},
		"Nhẫn 96%":                {96, true},
	}
	for name, c := range cases {
		got, ok := kimKhoaGoldPurity(name)
		if ok != c.ok || (ok && (got < c.purity-1e-9 || got > c.purity+1e-9)) {
			t.Errorf("%q: got (%v, %v), want (%v, %v)", name, got, ok, c.purity, c.ok)
		}
	}
}

// Live fetch; run with KIMKHOA_LIVE=1 go test ./services/price-tracker -run Live -v
func TestKimKhoaSource_GetPricesLive(t *testing.T) {
	if os.Getenv("KIMKHOA_LIVE") == "" {
		t.Skip("set KIMKHOA_LIVE=1 to hit the live page")
	}

	prices, err := NewKimKhoaSource("").GetPrices()
	if err != nil {
		t.Fatalf("GetPrices failed: %v", err)
	}
	if len(prices) == 0 {
		t.Fatal("expected at least one price")
	}
	for _, p := range prices {
		t.Logf("%-28s buy=%.0f sell=%.0f %s %s", p.Type, p.BuyPrice, p.SellPrice, p.Unit, p.Source)
	}
}
