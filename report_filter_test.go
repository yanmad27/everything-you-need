package main

import (
	"strings"
	"testing"

	pricetracker "everything-you-need/m/services/price-tracker"
)

func TestGoldRowFilter(t *testing.T) {
	type row struct {
		name string
		keep bool
	}
	cases := map[string][]row{
		"Doji": {
			{"VÀNG MIẾNG SJC", true}, {"KIM TT/AVPL", false}, {"NHẪN TRÒN 9999 HƯNG THỊNH VƯỢNG", true},
			{"VÀNG NGUYÊN LIỆU 9999", false}, {"VÀNG NGUYÊN LIỆU 999", false}, {"NỮ TRANG 9999", false},
			{"NỮ TRANG 999", false}, {"NỮ TRANG 99", false}, {"GIÁ NGUYÊN LIỆU 18K", false},
			{"GIÁ NGUYÊN LIỆU 14K", false}, {"GIÁ NGUYÊN LIỆU 10K", false}, {"BẠC DOJI 99.9 - 1 LƯỢNG", false},
			{"BẠC DOJI 99.9 - 5 LƯỢNG", false}, {"BẠC 999 (MIẾNG - THANH - THỎI)", false}, {"VÀNG", false}, {"BẠC", false},
		},
		"Kim Khoa": {
			{"KIM KHOA 999.9 ÉP VĨ", true}, {"KIM KHOA 999.9", true}, {"NỮ TRANG 98%", false}, {"NHẪN 97%", true},
		},
		"Mi Hồng": {
			{"VÀNG MIẾNG SJC", true}, {"VÀNG MIẾNG 9999", true}, {"VÀNG MIẾNG 985", true}, {"VÀNG MIẾNG 980", true},
			{"VÀNG MIẾNG 950", false}, {"VÀNG MIẾNG 750", false}, {"VÀNG MIẾNG 680", false}, {"VÀNG MIẾNG 610", false},
			{"VÀNG MIẾNG 580", false}, {"VÀNG MIẾNG 410", false}, {"VÀNG MIẾNG 975", true}, {"VÀNG MIẾNG 970", true},
			{"VÀNG MIẾNG 999", false},
		},
		"BTMC (generic, real names untested)": {
			{"Vàng miếng SJC", true}, {"Nhẫn tròn trơn 9999", true}, {"Nữ trang 99", false}, {"Bạc 999", false},
			{"Vàng NL 9999", false}, {"Vàng 999.9", true}, {"Vàng 99.99", true}, {"Vàng 99%", true}, {"Vàng 98%", true},
			{"Vàng 24K", false}, {"Vàng 18K", false}, {"Vàng 999", false},
		},
		"token and accent edges": {
			{"Vàng ENLARGED 9999", true}, {"Vàng 9999 NL", false}, {"Vàng-NL.9999", false}, {"vàng (NL) 9999", false},
			{"nguyên liệu 9999", false}, {"nguyen lieu 9999", false}, {"NGUYÊN LIỆU 9999", false},
			{"nu trang 9999", false}, {"trang suc 9999", false}, {"TRANG SỨC 9999", false}, {"bac 9999", false},
			{"Nhẫn 9999", true}, {"sjc", true}, {"Vàng 99.9", false}, {"Vàng 9999x", false}, {"Vàng 1999", false},
			{"Nu\u0302\u0303 trang 9999", false}, {"Bạc", false}, {"", false},
		},
	}
	for src, rows := range cases {
		for _, r := range rows {
			if got := goldRowWanted(r.name); got != r.keep {
				t.Errorf("%s %q: keep=%v want %v", src, r.name, got, r.keep)
			}
		}
	}
}

func TestGoldFilterDoesNotTouchOtherConsumers(t *testing.T) {
	for _, n := range []string{"Bitcoin", "BNB", "Ethereum", "Vàng 9999", "NHẪN TRÒN TRƠN", "Nữ trang 9999", "Solana", "Nhẫn 999"} {
		p := pricetracker.Price{Type: n}
		want := false
		for _, k := range []string{"9999", "tròn trơn", "sjc", "bitcoin", "ethereum", "bnb"} {
			if strings.Contains(strings.ToLower(n), k) {
				want = true
			}
		}
		if matchesKeyword(p) != want || showCrypto(p) != want {
			t.Errorf("shared/crypto filter changed for %q", n)
		}
	}
	// gold-watch keeps its own filter: a Doji "Nữ trang 9999" move still alerts.
	msg, ok := generateGoldWatchNotification(map[string][]pricetracker.PriceChange{
		"Doji": {{Price: pricetracker.Price{Type: "NỮ TRANG 9999", Currency: "VND", BuyPrice: 1, SellPrice: 2}, BuyChange: 5, SellChange: 5}},
	})
	if !ok || !strings.Contains(msg, "NỮ TRANG 9999") {
		t.Errorf("gold-watch output changed:\n%s", msg)
	}
	// Telegram report: a source left with no wanted rows shows N/A.
	m := generatePriceNotificationVND(map[string][]pricetracker.Price{
		"Doji": {{Type: "NỮ TRANG 9999", Currency: "VND", BuyPrice: 1, SellPrice: 2}},
	}, nil, nil, "", 0)
	if strings.Contains(m, "NỮ TRANG") || !strings.Contains(m, "Doji") || !strings.Contains(m, "N/A") {
		t.Errorf("empty source should show N/A:\n%s", m)
	}
}
