package main

import (
	"context"
	"everything-you-need/m/services/config"
	"everything-you-need/m/services/duedate"
	jobscheduler "everything-you-need/m/services/job-scheduler"
	"everything-you-need/m/services/lunar"
	"everything-you-need/m/services/news"
	pricetracker "everything-you-need/m/services/price-tracker"
	"everything-you-need/m/services/reminder"
	telebot "everything-you-need/m/services/tele-bot"
	"fmt"
	"log"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var currencyMap = map[string]string{
	"VND": "💰",
	"USD": "💵",
}

// goldWatchChangeThreshold: gold-watch only alerts when a buy or sell price
// moved more than this percent since the last alert.
const goldWatchChangeThreshold = 1.0

func registerJobs(scheduler *jobscheduler.JobScheduler, priceService *pricetracker.PriceTrackerService, teleService *telebot.TeleBotService, reminderService *reminder.Service, newsService *news.Service, dueDateService *duedate.Service, cfg *config.Config) {
	historyService := pricetracker.NewPriceHistoryService()
	now := time.Now().In(time.FixedZone("UTC+7", 7*60*60))

	if teleService != nil && cfg.Telegram.ChannelID != "" {
		jobName := "price-notification"
		err := scheduler.RegisterCronJob(jobName, "0 7 * * *", func() error {
			log.Printf("Sending price notification at %s...", now.Format("15:04 02/01/2006"))

			var allPrices map[string][]pricetracker.Price
			var lastFetchErr error
			maxFetchRetries := 3
			fetchRetryDelay := 10 * time.Second

			for attempt := 1; attempt <= maxFetchRetries; attempt++ {
				allPrices, lastFetchErr = priceService.GetAllPrices()
				if lastFetchErr == nil {
					break
				}

				log.Printf("Attempt %d failed to fetch prices: %v", attempt, lastFetchErr)

				if attempt < maxFetchRetries {
					log.Printf("Retrying price fetch in %v...", fetchRetryDelay)
					time.Sleep(fetchRetryDelay)
					fetchRetryDelay *= 2
				}
			}

			if lastFetchErr != nil {
				log.Printf("Some sources failed after %d attempts: %v", maxFetchRetries, lastFetchErr)
			}

			lastHistory, err := historyService.GetLastPriceHistory()
			if err != nil {
				log.Printf("Warning: failed to get price history: %v", err)
			}
			var changes map[string][]pricetracker.PriceChange
			if lastHistory != nil {
				changes = historyService.ComparePrices(allPrices, lastHistory.Prices)
			}

			message := generatePriceNotification(allPrices, changes, lastFetchErr)

			err = teleService.SendToChannelWithMarkdown(cfg.Telegram.ChannelID, message)
			if err != nil {
				return fmt.Errorf("failed to send notification: %w", err)
			}

			if err := historyService.SavePriceHistory(allPrices); err != nil {
				log.Printf("Warning: failed to save price history: %v", err)
			}

			log.Printf("Price notification at %s sent successfully", now.Format("15:04 02/01/2006"))
			return nil
		})
		if err != nil {
			log.Printf("Failed to register notification job for %s: %v", now.Format("15:04 02/01/2006"), err)
		}
	}

	if teleService != nil && cfg.Telegram.ChannelID != "" {
		lunarJobName := "lunar-notification"
		err := scheduler.RegisterCronJob(lunarJobName, "0 9 * * *", func() error {
			loc, tzErr := time.LoadLocation("Asia/Ho_Chi_Minh")
			if tzErr != nil {
				loc = time.FixedZone("UTC+7", 7*60*60)
			}
			today := time.Now().In(loc)

			lunarDay, lunarMonth, lunarYear, leap := lunar.SolarToLunar(
				today.Year(), int(today.Month()), today.Day(), lunar.VietnamTimeZone,
			)
			isLastDay := lunar.IsLastDayOfLunarMonth(today, lunar.VietnamTimeZone)
			shouldNotify, label := lunar.IsNotifyDay(lunarDay, isLastDay)

			log.Printf("Lunar check %s → %d/%d/%d (leap=%d, lastDay=%v) notify=%v (%s)",
				today.Format("2006-01-02"), lunarDay, lunarMonth, lunarYear, leap, isLastDay, shouldNotify, label)

			if !shouldNotify {
				return nil
			}

			message := generateLunarNotification(today, lunarDay, lunarMonth, lunarYear, leap == 1, label)
			if sendErr := teleService.SendToChannelWithMarkdown(cfg.Telegram.ChannelID, message); sendErr != nil {
				return fmt.Errorf("failed to send lunar notification: %w", sendErr)
			}

			log.Printf("Lunar notification sent for %s (%s)", today.Format("2006-01-02"), label)
			return nil
		})
		if err != nil {
			log.Printf("Failed to register lunar-notification job: %v", err)
		}
	}

	if teleService != nil && cfg.Telegram.ChannelID != "" {
		chayHandler := func() error {
			loc, tzErr := time.LoadLocation("Asia/Ho_Chi_Minh")
			if tzErr != nil {
				loc = time.FixedZone("UTC+7", 7*60*60)
			}
			today := time.Now().In(loc)

			lunarDay, _, _, _ := lunar.SolarToLunar(
				today.Year(), int(today.Month()), today.Day(), lunar.VietnamTimeZone,
			)
			isLastDay := lunar.IsLastDayOfLunarMonth(today, lunar.VietnamTimeZone)
			if !lunar.IsEveOfMung1OrRam(lunarDay, isLastDay) {
				return nil
			}

			if sendErr := teleService.SendToChannelWithMarkdown(cfg.Telegram.ChannelID, "Ngày mai nhớ ăn chay"); sendErr != nil {
				return fmt.Errorf("failed to send chay reminder: %w", sendErr)
			}
			log.Printf("Chay reminder sent for %s (lunar day=%d, lastDay=%v)", today.Format("2006-01-02 15:04"), lunarDay, isLastDay)
			return nil
		}

		if err := scheduler.RegisterCronJob("chay-reminder-day", "30 7,11,15 * * *", chayHandler); err != nil {
			log.Printf("Failed to register chay-reminder-day job: %v", err)
		}
		if err := scheduler.RegisterCronJob("chay-reminder-evening", "0 19 * * *", chayHandler); err != nil {
			log.Printf("Failed to register chay-reminder-evening job: %v", err)
		}
	}

	if teleService != nil && cfg.Telegram.ChannelID != "" {
		goldHistoryService := pricetracker.NewPriceHistoryServiceWithFileName("gold_watch_history.json")
		err := scheduler.RegisterCronJob("gold-watch", "*/5 * * * *", func() error {
			allPrices, fetchErr := priceService.GetAllPrices()
			if fetchErr != nil {
				log.Printf("gold-watch: some sources failed: %v", fetchErr)
			}

			goldPrices := make(map[string][]pricetracker.Price)
			for source, prices := range allPrices {
				if source == "CoinGecko" {
					continue
				}
				goldPrices[source] = prices
			}
			if len(goldPrices) == 0 {
				return nil
			}

			lastHistory, historyErr := goldHistoryService.GetLastPriceHistory()
			if historyErr != nil {
				log.Printf("gold-watch: failed to read history: %v", historyErr)
			}

			message := ""
			hasChange := false
			if lastHistory != nil {
				goldChanges := goldHistoryService.ComparePrices(goldPrices, lastHistory.Prices)
				message, hasChange = generateGoldWatchNotification(goldChanges)
			}

			// Persist baseline only on the first run or when an alert fires, so the
			// next comparison is against the last ALERTED price. Sub-threshold drift
			// keeps the old baseline, letting cumulative moves eventually trip 1%.
			if lastHistory == nil || hasChange {
				if saveErr := goldHistoryService.SavePriceHistory(goldPrices); saveErr != nil {
					log.Printf("gold-watch: failed to save history: %v", saveErr)
				}
			}

			if !hasChange {
				return nil
			}

			if sendErr := teleService.SendToChannelWithMarkdown(cfg.Telegram.ChannelID, message); sendErr != nil {
				return fmt.Errorf("failed to send gold watch notification: %w", sendErr)
			}
			log.Printf("gold-watch: change detected, notification sent")
			return nil
		})
		if err != nil {
			log.Printf("Failed to register gold-watch job: %v", err)
		}
	}

	if newsService != nil && teleService != nil && cfg.Telegram.ChannelID != "" {
		channelID := cfg.Telegram.ChannelID
		newsErr := scheduler.RegisterCronJob("news-digest", "0 * * * *", func() error {
			send := func(message string) error {
				return teleService.SendToChannelWithMarkdown(channelID, message)
			}
			return newsService.RunDigest(context.Background(), send)
		})
		if newsErr != nil {
			log.Printf("Failed to register news-digest job: %v", newsErr)
		}
	}

	if dueDateService != nil && teleService != nil && cfg.Telegram.ChannelID != "" {
		channelID := cfg.Telegram.ChannelID
		dueErr := scheduler.RegisterCronJob("duedate-reminder", "0 8 * * *", func() error {
			send := func(message string) error {
				return teleService.SendToChannel(channelID, message)
			}
			return dueDateService.RunDigest(context.Background(), send)
		})
		if dueErr != nil {
			log.Printf("Failed to register duedate-reminder job: %v", dueErr)
		}
	}

	if reminderService != nil {
		dispatchErr := scheduler.RegisterCronJob("reminder-dispatch", "* * * * *", func() error {
			return reminderService.Sweep(context.Background(), time.Now().UTC())
		})
		if dispatchErr != nil {
			log.Printf("Failed to register reminder-dispatch job: %v", dispatchErr)
		}

		retentionDays := cfg.Reminder.RetentionDays
		if retentionDays <= 0 {
			retentionDays = 30
		}
		purgeErr := scheduler.RegisterCronJob("reminder-purge", "30 3 * * *", func() error {
			cutoff := time.Now().UTC().Add(-time.Duration(retentionDays) * 24 * time.Hour)
			updatesCutoff := time.Now().UTC().Add(-14 * 24 * time.Hour)
			rCount, uCount, err := reminderService.PurgeOld(context.Background(), cutoff, updatesCutoff)
			if err != nil {
				return fmt.Errorf("purge: %w", err)
			}
			log.Printf("Reminder purge: removed %d reminders, %d processed_updates", rCount, uCount)
			return nil
		})
		if purgeErr != nil {
			log.Printf("Failed to register reminder-purge job: %v", purgeErr)
		}
	}

	jobs := scheduler.GetJobs()
	log.Printf("Registered %d jobs", len(jobs))
	for _, job := range jobs {
		log.Printf("- %s (cron: %s)", job["name"], job["cron_expr"])
	}
}

func generatePriceNotification(allPrices map[string][]pricetracker.Price, changes map[string][]pricetracker.PriceChange, _ error) string {
	loc, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		// Fallback to UTC+7 if timezone loading fails
		loc = time.FixedZone("UTC+7", 7*60*60)
	}
	now := time.Now().In(loc)
	message := "📊 *Price Update*\n"
	message += fmt.Sprintf("🕐 %s\n\n", now.Format("15:04 02/01/2006"))

	if len(allPrices) == 0 {
		message += "No price data available"
		return message
	}

	keywords := []string{"9999", "tròn trơn", "sjc", "bitcoin", "ethereum", "bnb"}

	goldSources := make(map[string][]pricetracker.Price)
	cryptoSources := make(map[string][]pricetracker.Price)

	for source, prices := range allPrices {
		if source == "CoinGecko" {
			cryptoSources[source] = prices
		} else {
			goldSources[source] = prices
		}
	}

	for source, prices := range goldSources {
		if len(prices) == 0 {
			message += fmt.Sprintf("*%s*\n• N/A\n\n", source)
			continue
		}

		message += fmt.Sprintf("*%s* (%d items)\n", source, len(prices))

		sourceChanges := make(map[string]pricetracker.PriceChange)
		if changes != nil {
			if sourceChangesList, exists := changes[source]; exists {
				for _, change := range sourceChangesList {
					sourceChanges[change.Price.Type] = change
				}
			}
		}

		count := len(prices)
		for i := 0; i < count; i++ {
			price := prices[i]
			for _, keyword := range keywords {
				if strings.Contains(strings.ToLower(price.Type), keyword) {
					change, hasChange := sourceChanges[price.Type]

					priceText := fmt.Sprintf("• %s %s: ", currencyMap[price.Currency], price.Currency)
					buyText := fmt.Sprintf("*%s*", formatPrice(price.BuyPrice))
					sellText := fmt.Sprintf("*%s*", formatPrice(price.SellPrice))

					if hasChange && (change.BuyChange != 0 || change.SellChange != 0) || !hasChange {
						if change.BuyChange > 0 {
							buyText += fmt.Sprintf(" 📈+%.1f%% ", change.BuyChange)
						} else if change.BuyChange < 0 {
							buyText += fmt.Sprintf(" 📉%.1f%% ", change.BuyChange)
						}

						if change.SellChange != 0 && change.SellChange != change.BuyChange {
							if change.SellChange > 0 {
								sellText += fmt.Sprintf("/📈+%.1f%% ", change.SellChange)
							} else {
								sellText += fmt.Sprintf("/📉%.1f%% ", change.SellChange)
							}
						}

					}
					priceText += fmt.Sprintf("%s - %s", buyText, sellText)

					message += fmt.Sprintf("%s - %s\n", priceText, formatType(price.Type))
					break
				}
			}
		}

		message += "\n"
	}

	for source, prices := range cryptoSources {
		if len(prices) == 0 {
			message += fmt.Sprintf("*%s*\n• N/A\n\n", source)
			continue
		}

		message += fmt.Sprintf("*%s* (%d items)\n", source, len(prices))

		sourceChanges := make(map[string]pricetracker.PriceChange)
		if changes != nil {
			if sourceChangesList, exists := changes[source]; exists {
				for _, change := range sourceChangesList {
					sourceChanges[change.Price.Type] = change
				}
			}
		}

		count := len(prices)
		for i := 0; i < count; i++ {
			price := prices[i]
			for _, keyword := range keywords {
				if strings.Contains(strings.ToLower(price.Type), keyword) {
					change, hasChange := sourceChanges[price.Type]

					priceText := fmt.Sprintf("• %s %s: ", currencyMap[price.Currency], price.Currency)
					buyText := fmt.Sprintf("*%s*", formatPrice(price.BuyPrice))

					if hasChange && (change.BuyChange != 0 || change.SellChange != 0) || !hasChange {
						if change.BuyChange > 0 {
							buyText += fmt.Sprintf(" 📈+%.1f%% ", change.BuyChange)
						} else if change.BuyChange < 0 {
							buyText += fmt.Sprintf(" 📉%.1f%% ", change.BuyChange)
						}

					}
					priceText += fmt.Sprintf("%s", buyText)

					message += fmt.Sprintf("%s - %s\n", priceText, formatType(price.Type))
					break
				}
			}
		}

		message += "\n"
	}

	message += "🤖 _Automated update_"
	return message
}

// generateGoldWatchNotification builds an alert listing only the gold prices
// that moved since the previous check. Returns the message and whether any
// tracked gold price actually changed (false → caller stays silent).
func generateGoldWatchNotification(changes map[string][]pricetracker.PriceChange) (string, bool) {
	loc, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		loc = time.FixedZone("UTC+7", 7*60*60)
	}
	now := time.Now().In(loc)

	keywords := []string{"9999", "tròn trơn", "sjc"}

	hasChange := false
	body := ""

	for source, sourceChanges := range changes {
		sourceLines := ""
		for _, change := range sourceChanges {
			if math.Abs(change.BuyChange) <= goldWatchChangeThreshold && math.Abs(change.SellChange) <= goldWatchChangeThreshold {
				continue
			}

			price := change.Price
			matched := false
			for _, keyword := range keywords {
				if strings.Contains(strings.ToLower(price.Type), keyword) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}

			hasChange = true

			buyText := fmt.Sprintf("*%s*", formatPrice(price.BuyPrice))
			if change.BuyChange > 0 {
				buyText += fmt.Sprintf(" 📈+%.1f%%", change.BuyChange)
			} else if change.BuyChange < 0 {
				buyText += fmt.Sprintf(" 📉%.1f%%", change.BuyChange)
			}

			sellText := fmt.Sprintf("*%s*", formatPrice(price.SellPrice))
			if change.SellChange > 0 {
				sellText += fmt.Sprintf(" 📈+%.1f%%", change.SellChange)
			} else if change.SellChange < 0 {
				sellText += fmt.Sprintf(" 📉%.1f%%", change.SellChange)
			}

			sourceLines += fmt.Sprintf("• %s %s: %s - %s - %s\n",
				currencyMap[price.Currency], price.Currency, buyText, sellText, formatType(price.Type))
		}

		if sourceLines != "" {
			body += fmt.Sprintf("*%s*\n%s\n", source, sourceLines)
		}
	}

	if !hasChange {
		return "", false
	}

	message := "🟡 *Gold Price Change*\n"
	message += fmt.Sprintf("🕐 %s\n\n", now.Format("15:04 02/01/2006"))
	message += body
	message += "🤖 _Gold watch (5m)_"
	return message, true
}

func formatType(typeStr string) string {
	regex := regexp.MustCompile(`\(([^)]+)\)`)
	return regex.ReplaceAllString(typeStr, "")
}

func generateLunarNotification(solar time.Time, lunarDay, lunarMonth, lunarYear int, leap bool, label string) string {
	monthLabel := fmt.Sprintf("tháng %d", lunarMonth)
	if leap {
		monthLabel += " (nhuận)"
	}

	message := "🌙 *Lịch Âm*\n"
	message += fmt.Sprintf("📅 Dương lịch: %s\n", solar.Format("02/01/2006"))
	message += fmt.Sprintf("🗓 Âm lịch: %d/%d/%d%s\n\n", lunarDay, lunarMonth, lunarYear, leapSuffix(leap))
	message += fmt.Sprintf("🔔 Nhắc nhở: *%s*\n", label)
	message += fmt.Sprintf("_Hôm nay là %s, %s._\n", describeToday(lunarDay), monthLabel)
	return message
}

func leapSuffix(leap bool) string {
	if leap {
		return " (nhuận)"
	}
	return ""
}

func describeToday(lunarDay int) string {
	switch lunarDay {
	case 1:
		return "mùng 1"
	case 14:
		return "14 âm lịch"
	case 15:
		return "rằm"
	case 13:
		return "13 âm lịch"
	case 29, 30:
		return fmt.Sprintf("%d âm lịch (ngày cuối tháng)", lunarDay)
	}
	return fmt.Sprintf("%d âm lịch", lunarDay)
}

func formatPrice(price float64) string {
	str := strconv.FormatFloat(price, 'f', 0, 64)
	parts := []string{}
	n := len(str)
	for i := n; i > 0; i -= 3 {
		start := i - 3
		if start < 0 {
			start = 0
		}
		parts = append([]string{str[start:i]}, parts...)
		if start == 0 {
			break
		}
	}
	return strings.Join(parts, ".")
}
