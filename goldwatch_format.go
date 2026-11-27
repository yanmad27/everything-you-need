package main

import (
	"fmt"
	"math"
	"strings"
	"time"

	pricetracker "everything-you-need/m/services/price-tracker"
)

// generateGoldWatchNotification builds the gold-watch alert from the rows that
// moved past goldWatchChangeThreshold and pass the report's gold filter
// (goldRowWanted), rendered with the report's monospace tables. The bool is
// false (caller stays silent) when no kept row moved.
func generateGoldWatchNotification(changes map[string][]pricetracker.PriceChange) (string, bool) {
	kept := make(map[string][]pricetracker.Price)
	for source, sourceChanges := range changes {
		for _, c := range sourceChanges {
			if math.Abs(c.BuyChange) <= goldWatchChangeThreshold && math.Abs(c.SellChange) <= goldWatchChangeThreshold {
				continue
			}
			if goldRowWanted(c.Price.Type) {
				kept[source] = append(kept[source], c.Price)
			}
		}
	}
	if len(kept) == 0 {
		return "", false
	}

	loc, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		loc = time.FixedZone("UTC+7", 7*60*60)
	}

	var sb strings.Builder
	for _, source := range sortedKeys(kept) {
		goldSection(&sb, source, kept[source], changes)
	}

	message := "🟡 *Gold Price Change*\n"
	message += fmt.Sprintf("🕐 %s\n\n", time.Now().In(loc).Format("15:04 02/01/2006"))
	message += guardWidth(strings.TrimRight(sb.String(), "\n")) + "\n\n"
	message += "🤖 _Gold watch (5m)_"
	return message, true
}
