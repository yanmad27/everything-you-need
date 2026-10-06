package main

import (
	pricetracker "everything-you-need/m/services/price-tracker"
	"strings"
	"testing"
)

func TestGenerateGoldWatchNotification(t *testing.T) {
	t.Run("no change returns silent", func(t *testing.T) {
		changes := map[string][]pricetracker.PriceChange{
			"Doji": {
				{
					Price:      pricetracker.Price{Type: "SJC 1L", Currency: "VND", BuyPrice: 8000000, SellPrice: 8200000},
					BuyChange:  0,
					SellChange: 0,
				},
			},
		}
		message, hasChange := generateGoldWatchNotification(changes)
		if hasChange {
			t.Errorf("expected hasChange=false, got true (message=%q)", message)
		}
		if message != "" {
			t.Errorf("expected empty message, got %q", message)
		}
	})

	t.Run("changed gold price notifies", func(t *testing.T) {
		changes := map[string][]pricetracker.PriceChange{
			"Doji": {
				{
					Price:      pricetracker.Price{Type: "SJC 1L", Currency: "VND", BuyPrice: 8100000, SellPrice: 8200000},
					BuyChange:  1.25,
					SellChange: 0,
				},
			},
		}
		message, hasChange := generateGoldWatchNotification(changes)
		if !hasChange {
			t.Fatalf("expected hasChange=true, got false")
		}
		if !strings.Contains(message, "Gold Price Change") {
			t.Errorf("expected title in message, got %q", message)
		}
		if !strings.Contains(message, "📈+1.2%") {
			t.Errorf("expected up marker in message, got %q", message)
		}
	})

	t.Run("non-keyword change ignored", func(t *testing.T) {
		changes := map[string][]pricetracker.PriceChange{
			"Doji": {
				{
					Price:      pricetracker.Price{Type: "Ring 24K", Currency: "VND", BuyPrice: 7000000, SellPrice: 7100000},
					BuyChange:  2.0,
					SellChange: 2.0,
				},
			},
		}
		_, hasChange := generateGoldWatchNotification(changes)
		if hasChange {
			t.Errorf("expected non-keyword change to be ignored, got hasChange=true")
		}
	})
}

// kimKhoaFixture mirrors the rows the Kim Khoa source keeps from the live page.
func kimKhoaFixture() []pricetracker.Price {
	row := func(name string, buy, sell float64) pricetracker.Price {
		return pricetracker.Price{Type: name, BuyPrice: buy, SellPrice: sell, Unit: "nghìn/chỉ", Currency: "VND", Source: pricetracker.KimKhoaSourceName}
	}
	return []pricetracker.Price{
		row("VÀNG 999.9", 13530, 13710),
		row("VÀNG 98 KIM KHOA 24K", 13190, 13350),
		row("VÀNG 97", 12970, 13130),
		row("VÀNG NỮ TRANG KK 98", 13200, 13530),
		row("VÀNG NL NGOÀI 9999", 13480, 0),
		row("VÀNG NL NGOÀI 999", 13430, 0),
	}
}

func TestKimKhoaRowsBypassKeywordFilter(t *testing.T) {
	kimKhoa := kimKhoaFixture()
	other := []pricetracker.Price{
		{Type: "Vàng nhẫn 999.9", Currency: "VND", BuyPrice: 100, SellPrice: 110},
		{Type: "SJC 1L", Currency: "VND", BuyPrice: 200, SellPrice: 210},
	}

	t.Run("price update", func(t *testing.T) {
		message := generatePriceNotification(map[string][]pricetracker.Price{
			pricetracker.KimKhoaSourceName: kimKhoa,
			"Doji":                         other,
		}, nil, nil)

		for _, p := range kimKhoa {
			line := " - " + p.Type + "\n"
			if n := strings.Count(message, line); n != 1 {
				t.Errorf("%q appears %d times, want 1\n%s", p.Type, n, message)
			}
		}
		if !strings.Contains(message, "*13.480* - *0* - VÀNG NL NGOÀI 9999\n") {
			t.Errorf("sell 0 row not rendered as expected\n%s", message)
		}
		if strings.Contains(message, "Vàng nhẫn 999.9") {
			t.Errorf("non-keyword row from another source must stay hidden\n%s", message)
		}
		if !strings.Contains(message, " - SJC 1L\n") {
			t.Errorf("keyword row from another source must stay visible\n%s", message)
		}
	})

	t.Run("gold watch", func(t *testing.T) {
		var kkChanges []pricetracker.PriceChange
		for _, p := range kimKhoa {
			kkChanges = append(kkChanges, pricetracker.PriceChange{Price: p, BuyChange: 2, SellChange: 2})
		}
		quiet := pricetracker.PriceChange{
			Price:     pricetracker.Price{Type: "VÀNG 97 QUIET", Currency: "VND", BuyPrice: 1, SellPrice: 1},
			BuyChange: 0.5, SellChange: 0.5,
		}
		kkChanges = append(kkChanges, quiet)

		message, hasChange := generateGoldWatchNotification(map[string][]pricetracker.PriceChange{
			pricetracker.KimKhoaSourceName: kkChanges,
			"Doji": {
				{Price: other[0], BuyChange: 2, SellChange: 2},
				{Price: other[1], BuyChange: 2, SellChange: 2},
			},
		})
		if !hasChange {
			t.Fatal("expected hasChange=true")
		}
		for _, p := range kimKhoa {
			line := " - " + p.Type + "\n"
			if n := strings.Count(message, line); n != 1 {
				t.Errorf("%q appears %d times, want 1\n%s", p.Type, n, message)
			}
		}
		if strings.Contains(message, "QUIET") {
			t.Errorf("Kim Khoa row below the change threshold must be skipped\n%s", message)
		}
		if strings.Contains(message, "Vàng nhẫn 999.9") {
			t.Errorf("non-keyword row from another source must stay hidden\n%s", message)
		}
		if !strings.Contains(message, " - SJC 1L\n") {
			t.Errorf("keyword row from another source must stay visible\n%s", message)
		}
	})
}
