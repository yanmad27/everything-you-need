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
