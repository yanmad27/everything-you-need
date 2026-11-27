package main

import (
	"strings"
	"testing"

	pricetracker "everything-you-need/m/services/price-tracker"
)

func gwChange(typ string, buy, sell, bc, sc float64) pricetracker.PriceChange {
	return pricetracker.PriceChange{
		Price:     pricetracker.Price{Type: typ, Currency: "VND", Unit: "nghìn/chỉ", BuyPrice: buy, SellPrice: sell},
		BuyChange: bc, SellChange: sc,
	}
}

func checkAlertShape(t *testing.T, msg string) {
	t.Helper()
	if !strings.HasPrefix(msg, "🟡 *Gold Price Change*\n🕐 ") || !strings.HasSuffix(msg, "🤖 _Gold watch (5m)_") {
		t.Errorf("header/footer changed:\n%s", msg)
	}
	if strings.Count(msg, "```")%2 != 0 || strings.Count(msg, "```") == 0 {
		t.Errorf("unbalanced fences:\n%s", msg)
	}
	inBlock, w := false, 0
	for _, l := range strings.Split(msg, "\n") {
		if l == "```" {
			inBlock, w = !inBlock, 0
			continue
		}
		if !inBlock {
			continue
		}
		if runeLen(l) > tableBudget {
			t.Errorf("line wider than %d: %q", tableBudget, l)
		}
		if w == 0 {
			w = runeLen(l)
		} else if runeLen(l) != w {
			t.Errorf("unequal widths in block: %q", l)
		}
	}
}

func TestGoldWatchExcludedOnlySilent(t *testing.T) {
	msg, ok := generateGoldWatchNotification(map[string][]pricetracker.PriceChange{
		"Doji":                         {gwChange("VÀNG NGUYÊN LIỆU 9999", 1, 2, 3, 3), gwChange("NỮ TRANG 9999", 1, 2, 3, 3)},
		pricetracker.KimKhoaSourceName: {gwChange("NỮ TRANG 98%", 11400, 11700, 2, 2)},
	})
	if ok || msg != "" {
		t.Fatalf("want silent, got %q", msg)
	}
}

func TestGoldWatchReplayDoji0921(t *testing.T) {
	msg, ok := generateGoldWatchNotification(map[string][]pricetracker.PriceChange{
		"Doji": {
			gwChange("VÀNG NGUYÊN LIỆU 9999", 12050, 12250, 1.3, 1.2),
			gwChange("NỮ TRANG 9999", 12000, 12300, 1.3, 1.2),
			gwChange("VÀNG MIẾNG SJC", 15100, 15300, 1.4, 1.1),
		},
	})
	if !ok {
		t.Fatal("expected alert")
	}
	checkAlertShape(t, msg)
	if !strings.Contains(msg, "SJC miếng") || strings.Contains(msg, "guyên") || strings.Contains(msg, "ữ trang") || strings.Count(msg, "\n") > 10 {
		t.Errorf("only SJC miếng expected:\n%s", msg)
	}
}

func TestGoldWatchMixedMultiSource(t *testing.T) {
	changes := map[string][]pricetracker.PriceChange{
		"Doji":                         {gwChange("VÀNG MIẾNG SJC", 15100, 15300, 1.4, 1.1), gwChange("NỮ TRANG 9999", 1, 2, 5, 5), gwChange("NHẪN TRÒN 9999 HƯNG THỊNH VƯỢNG", 14900, 15100, 0.2, 1.5)},
		pricetracker.KimKhoaSourceName: {gwChange("KIM KHOA 999.9", 11650, 11800, -1.6, -1.2), gwChange("NỮ TRANG 98%", 11400, 11700, 2, 2), gwChange("NHẪN 97%", 11250, 11400, 0.1, 0.1)},
	}
	msg, ok := generateGoldWatchNotification(changes)
	if !ok {
		t.Fatal("expected alert")
	}
	checkAlertShape(t, msg)
	for _, want := range []string{"SJC miếng", "-1.6", "+1.4"} {
		if !strings.Contains(msg, want) {
			t.Errorf("missing %q:\n%s", want, msg)
		}
	}
	for _, bad := range []string{"ữ trang", "11.250"} {
		if strings.Contains(msg, bad) {
			t.Errorf("unexpected %q:\n%s", bad, msg)
		}
	}
	if strings.Index(msg, "Doji") > strings.Index(msg, "Kim Khoa") {
		t.Errorf("sources not sorted:\n%s", msg)
	}
	t.Log("\n" + msg)
}

func TestGoldWatchThresholdUnchanged(t *testing.T) {
	_, ok := generateGoldWatchNotification(map[string][]pricetracker.PriceChange{
		"Doji": {gwChange("VÀNG MIẾNG SJC", 1, 2, 1.0, -1.0)},
	})
	if ok {
		t.Error("exactly 1.0% must not alert")
	}
	_, ok = generateGoldWatchNotification(map[string][]pricetracker.PriceChange{
		"Doji": {gwChange("VÀNG MIẾNG SJC", 1, 2, 0, -1.01)},
	})
	if !ok {
		t.Error("sell-only >1% must alert")
	}
}
