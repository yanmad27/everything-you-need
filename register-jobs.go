package main

import (
	"everything-you-need/m/services/config"
	jobscheduler "everything-you-need/m/services/job-scheduler"
	pricetracker "everything-you-need/m/services/price-tracker"
	telenoti "everything-you-need/m/services/tele-noti"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func registerJobs(scheduler *jobscheduler.JobScheduler, priceService *pricetracker.PriceTrackerService, teleService *telenoti.TeleNotiService, cfg *config.Config) {
	historyService := pricetracker.NewPriceHistoryService()
	now := time.Now().In(time.FixedZone("UTC+7", 7*60*60))

	if teleService != nil && cfg.Telegram.ChannelID != "" {
		jobName := "price-notification"
		err := scheduler.RegisterCronJob(jobName, "0 11,12,13,14,15,16 * * *", func() error {
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
				errorMessage := fmt.Sprintf("❌ *Price Fetch Error*\n🕐 %s\n\nFailed to fetch gold prices after %d attempts.\nError: %v\n\n🤖 _Automated error report_",
					time.Now().In(time.FixedZone("UTC+7", 7*60*60)).Format("15:04 02/01/2006"),
					maxFetchRetries,
					lastFetchErr)

				// Try to send error notification (without retry to avoid infinite loops)
				if sendErr := teleService.SendToChannelWithMarkdown(cfg.Telegram.ChannelID, errorMessage); sendErr != nil {
					log.Printf("Failed to send error notification: %v", sendErr)
				}

				return fmt.Errorf("failed to fetch prices after %d attempts: %w", maxFetchRetries, lastFetchErr)
			}

			lastHistory, err := historyService.GetLastPriceHistory()
			if err != nil {
				log.Printf("Warning: failed to get price history: %v", err)
			}
			var changes map[string][]pricetracker.PriceChange
			if lastHistory != nil {
				changes = historyService.ComparePrices(allPrices, lastHistory.Prices)
			}

			message := generatePriceNotification(allPrices, changes, nil)

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

	jobs := scheduler.GetJobs()
	log.Printf("Registered %d jobs", len(jobs))
	for _, job := range jobs {
		log.Printf("- %s (cron: %s)", job["name"], job["cron_expr"])
	}
}

func generatePriceNotification(allPrices map[string][]pricetracker.Price, changes map[string][]pricetracker.PriceChange, err error) string {
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

	keywords := []string{"9999", "tròn trơn", "sjc"}
	for source, prices := range allPrices {
		if len(prices) > 0 {
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

						priceText := fmt.Sprintf("• 💰%s: ", price.Currency)
						buyText := fmt.Sprintf("*%s*", formatPrice(price.BuyPrice))
						sellText := fmt.Sprintf("*%s*", formatPrice(price.SellPrice))

						if hasChange && (change.BuyChange != 0 || change.SellChange != 0) {

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
	}

	if err != nil {
		message += fmt.Sprintf("\nError: %s\n", err.Error())
	}

	message += "🤖 _Automated update_"
	return message
}

func formatType(typeStr string) string {
	regex := regexp.MustCompile(`\(([^)]+)\)`)
	return regex.ReplaceAllString(typeStr, "")
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
