package main

import (
	"testing"

	pricetracker "everything-you-need/m/services/price-tracker"
)

// productionFixture mirrors the production report: BTMC N/A, Doji (16 rows,
// real names fetched 2026-11-26), Kim Khoa (captured fixture), Mi Hồng
// (names emitted by mihong.source.go), CoinGecko (3).
func productionFixture() (map[string][]pricetracker.Price, map[string][]pricetracker.PriceChange) {
	row := func(name string, buy, sell float64, unit string) pricetracker.Price {
		return pricetracker.Price{Type: name, BuyPrice: buy, SellPrice: sell, Unit: unit, Currency: "VND"}
	}
	c, l := "nghìn/chỉ", "nghìn/lượng"
	doji := []pricetracker.Price{
		row("VÀNG MIẾNG SJC", 12950, 13450, c), row("KIM TT/AVPL", 12850, 13350, c),
		row("NHẪN TRÒN 9999 HƯNG THỊNH VƯỢNG", 12850, 13350, c), row("VÀNG NGUYÊN LIỆU 9999", 12000, 12200, c),
		row("VÀNG NGUYÊN LIỆU 999", 11950, 12150, c), row("NỮ TRANG 9999", 12400, 12800, c),
		row("NỮ TRANG 999", 12350, 12750, c), row("NỮ TRANG 99", 12170, 12600, c),
		row("GIÁ NGUYÊN LIỆU 18K", 8843, 9843, c), row("GIÁ NGUYÊN LIỆU 14K", 6752, 7752, c),
		row("GIÁ NGUYÊN LIỆU 10K", 4565, 5565, c), row("BẠC DOJI 99.9 - 1 LƯỢNG", 1564, 1629, l),
		row("BẠC DOJI 99.9 - 5 LƯỢNG", 7820, 8145, l), row("BẠC 999 (MIẾNG - THANH - THỎI)", 1519, 1599, l),
		row("VÀNG", 3576.2475650224, 3576.2475650224, c), row("BẠC", 41.6765245539, 41.6765245539, c),
	}
	mh := []pricetracker.Price{
		row("VÀNG MIẾNG SJC", 12940, 13440, ""), row("VÀNG MIẾNG 9999", 12780, 13180, ""),
	}
	for _, code := range []string{"985", "980", "950", "750", "680", "610", "580", "410"} {
		mh = append(mh, row("VÀNG MIẾNG "+code, 9000, 9300, ""))
	}
	usd := func(n, u string, v float64) pricetracker.Price {
		return pricetracker.Price{Type: n, BuyPrice: v, SellPrice: v, Unit: u, Currency: "USD"}
	}
	prices := map[string][]pricetracker.Price{
		"Bảo Tín Minh Châu":            {},
		"Doji":                         doji,
		pricetracker.KimKhoaSourceName: kimKhoaFixture(),
		"Mi Hồng":                      mh,
		"CoinGecko":                    {usd("Bitcoin", "BTC", 60809), usd("Ethereum", "ETH", 1845), usd("BNB", "BNB", 598)},
	}
	chg := func(n string, b, s float64) pricetracker.PriceChange {
		return pricetracker.PriceChange{Price: pricetracker.Price{Type: n}, BuyChange: b, SellChange: s}
	}
	changes := map[string][]pricetracker.PriceChange{
		"Doji":                         {chg("VÀNG MIẾNG SJC", 0.4, 0.4), chg("NHẪN TRÒN 9999 HƯNG THỊNH VƯỢNG", -0.8, 0.4), chg("NỮ TRANG 9999", 1.2, 1.2)},
		pricetracker.KimKhoaSourceName: {chg("KIM KHOA 999.9", 0.2, 0.2)},
		"Mi Hồng":                      {chg("VÀNG MIẾNG 9999", -0.5, -0.5)},
		"CoinGecko":                    {chg("Bitcoin", 0.3, 0.3), chg("Ethereum", -1.1, -1.1), chg("BNB", 2.4, 2.4)},
	}
	return prices, changes
}

func TestProductionSample(t *testing.T) {
	p, c := productionFixture()
	fg := fgAt(20, "Extreme Fear")
	t.Logf("=== WITH VND ===\n%s", stripTime(generatePriceNotificationVND(p, c, nil, fearGreedLine(stubFG{f: fg}), 26500)))
	t.Logf("=== WITHOUT VND ===\n%s", stripTime(generatePriceNotificationVND(p, c, nil, fearGreedLine(stubFG{f: fg}), 0)))
}
