package pricetracker

import (
	"os"
	"testing"
)

// Trimmed from the real page (table.goldbox-table), captured 2026-10-06.
const kimKhoaSampleHTML = `<div class="goldbox"><table class="goldbox-table"><thead><tr><th>Sản Phẩm</th><th>Mua (đ)</th><th>Bán (đ)</th></tr>
</thead><tbody><tr><td class="goldbox-product-name">Vàng 999.9</td><td class="goldbox-price goldbox-buy">13.530.000đ</td><td class="goldbox-price goldbox-sell">13.710.000đ</td></tr>
<tr><td class="goldbox-product-name">Vàng 98 Kim Khoa 24k</td><td class="goldbox-price goldbox-buy">13.190.000đ</td><td class="goldbox-price goldbox-sell">13.350.000đ</td></tr>
<tr><td class="goldbox-product-name">Vàng 97</td><td class="goldbox-price goldbox-buy">12.970.000đ</td><td class="goldbox-price goldbox-sell">13.130.000đ</td></tr>
<tr><td class="goldbox-product-name">Vàng 96</td><td class="goldbox-price goldbox-buy">12.840.000đ</td><td class="goldbox-price goldbox-sell">13.000.000đ</td></tr>
<tr><td class="goldbox-product-name">Vàng Nữ Trang KK 98</td><td class="goldbox-price goldbox-buy">13.200.000đ</td><td class="goldbox-price goldbox-sell">13.530.000đ</td></tr>
<tr><td class="goldbox-product-name">Vàng NL Ngoài 9999</td><td class="goldbox-price goldbox-buy">13.480.000đ</td><td class="goldbox-price goldbox-sell">0</td></tr>
<tr><td class="goldbox-product-name">Vàng NL Ngoài 999</td><td class="goldbox-price goldbox-buy">13.430.000đ</td><td class="goldbox-price goldbox-sell">0</td></tr>
<tr><td class="goldbox-product-name">Vàng 41.7</td><td class="goldbox-price goldbox-buy">5.710.000đ</td><td class="goldbox-price goldbox-sell">6.020.000đ</td></tr>
<tr><td class="goldbox-product-name">Vàng 41.7 W (Trắng)</td><td class="goldbox-price goldbox-buy">5.760.000đ</td><td class="goldbox-price goldbox-sell">6.100.000đ</td></tr>
<tr><td class="goldbox-product-name">Vàng Kim Khoa 610</td><td class="goldbox-price goldbox-buy">8.170.000đ</td><td class="goldbox-price goldbox-sell">8.540.000đ</td></tr>
<tr><td class="goldbox-product-name">Vàng Đúc</td><td class="goldbox-price goldbox-buy">8.310.000đ</td><td class="goldbox-price goldbox-sell">8.680.000đ</td></tr>
<tr><td class="goldbox-product-name">Bạc Kim Khoa 1kilo Thỏi</td><td class="goldbox-price goldbox-buy">61.060.000đ</td><td class="goldbox-price goldbox-sell">63.600.000đ</td></tr>
<tr><td class="goldbox-product-name">Bạc miếng 1L</td><td class="goldbox-price goldbox-buy">2.295.000đ</td><td class="goldbox-price goldbox-sell">2.390.000đ</td></tr>
<tr><td class="goldbox-product-name">Trang Sức Bạc Kim Khoa</td><td class="goldbox-price goldbox-buy">130.000.000đ</td><td class="goldbox-price goldbox-sell">250.000.000đ</td></tr>
</tbody></table></div>`

func TestKimKhoaSource_GetSourceName(t *testing.T) {
	if name := NewKimKhoaSource("").GetSourceName(); name != KimKhoaSourceName {
		t.Errorf("unexpected source name %q", name)
	}
}

func TestParseKimKhoaPrices_KeepsOnlyPurity97Plus(t *testing.T) {
	prices, err := parseKimKhoaPrices(kimKhoaSampleHTML)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	want := []Price{
		{Type: "VÀNG 999.9", BuyPrice: 13530, SellPrice: 13710},
		{Type: "VÀNG 98 KIM KHOA 24K", BuyPrice: 13190, SellPrice: 13350},
		{Type: "VÀNG 97", BuyPrice: 12970, SellPrice: 13130},
		{Type: "VÀNG NỮ TRANG KK 98", BuyPrice: 13200, SellPrice: 13530},
		{Type: "VÀNG NL NGOÀI 9999", BuyPrice: 13480, SellPrice: 0}, // sell not quoted
		{Type: "VÀNG NL NGOÀI 999", BuyPrice: 13430, SellPrice: 0},
	}
	if len(prices) != len(want) {
		t.Fatalf("got %d prices, want %d: %+v", len(prices), len(want), prices)
	}
	for i, w := range want {
		g := prices[i]
		if g.Type != w.Type || g.BuyPrice != w.BuyPrice || g.SellPrice != w.SellPrice {
			t.Errorf("row %d: got %+v, want %+v", i, g, w)
		}
		if g.Currency != "VND" || g.Unit != "nghìn/chỉ" {
			t.Errorf("row %d: unexpected currency/unit %q/%q", i, g.Currency, g.Unit)
		}
	}
}

func TestParseKimKhoaPrices_NoRows(t *testing.T) {
	if _, err := parseKimKhoaPrices("<html></html>"); err == nil {
		t.Error("expected error when page has no price rows")
	}
}

func TestParseKimKhoaPrices_NothingSurvivesFilter(t *testing.T) {
	page := `<table><tr><td class="goldbox-product-name">Vàng 96</td><td class="goldbox-price goldbox-buy">1.000.000đ</td><td class="goldbox-price goldbox-sell">1.100.000đ</td></tr></table>`
	if _, err := parseKimKhoaPrices(page); err == nil {
		t.Error("expected error when no product reaches the purity threshold")
	}
}

func TestParseKimKhoaPrices_ExtraCellDoesNotFuseDigits(t *testing.T) {
	page := `<table><tr><td class="goldbox-product-name">Vàng 97</td><td class="goldbox-price goldbox-buy">12.970.000đ</td><td class="goldbox-price goldbox-sell">13.130.000đ</td><td class="chg">▲ 50.000</td></tr></table>`
	prices, err := parseKimKhoaPrices(page)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(prices) != 1 || prices[0].BuyPrice != 12970 || prices[0].SellPrice != 13130 {
		t.Errorf("unexpected prices: %+v", prices)
	}
}

func TestParseKimKhoaVND(t *testing.T) {
	cases := map[string]float64{
		"13.530.000đ":       13530000,
		" 13.530.000 đ ":    13530000,
		"0":                 0,
		"":                  0,
		"13.130.000đ 50":    0,
		"13.130.000đ50.000": 0,
		"abc":               0,
		"1.2.3":             0,
	}
	for in, want := range cases {
		if got := parseKimKhoaVND(in); got != want {
			t.Errorf("%q: got %v, want %v", in, got, want)
		}
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
