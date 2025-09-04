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

func registerJobs(scheduler *jobscheduler.SimpleScheduler, priceService *pricetracker.PriceTrackerService, teleService *telenoti.TeleNotiService, cfg *config.Config) {
	// scheduler.RegisterJob("price-fetch", 5*time.Minute, func() error {
	// 	log.Println("Fetching latest prices...")

	// 	allPrices, err := priceService.GetAllPrices()
	// 	if err != nil {
	// 		return fmt.Errorf("failed to fetch prices: %w", err)
	// 	}

	// 	totalPrices := 0
	// 	for source, prices := range allPrices {
	// 		totalPrices += len(prices)
	// 		log.Printf("Fetched %d prices from %s", len(prices), source)
	// 	}

	// 	log.Printf("Price fetch completed: %d total prices", totalPrices)
	// 	return nil
	// })

	if teleService != nil && cfg.Telegram.ChannelID != "" {
		// Register price notifications for 7am, 1pm, and 7pm
		times := []string{"07:00", "13:00", "19:00"}
		for _, timeSlot := range times {
			jobName := fmt.Sprintf("price-notification-%s", timeSlot)
			err := scheduler.RegisterDailyJob(jobName, timeSlot, func() error {
				log.Printf("Sending price notification at %s...", timeSlot)

				allPrices, err := priceService.GetAllPrices()
				message := generatePriceNotification(allPrices, err)

				err = teleService.SendToChannelWithMarkdown(cfg.Telegram.ChannelID, message)
				if err != nil {
					return fmt.Errorf("failed to send notification: %w", err)
				}

				log.Printf("Price notification at %s sent successfully", timeSlot)
				return nil
			})
			if err != nil {
				log.Printf("Failed to register notification job for %s: %v", timeSlot, err)
			}
		}
	}

	scheduler.RegisterJob("health-check", 1*time.Minute, func() error {
		log.Printf("Health check: %s is running", cfg.App.Name)
		return nil
	})

	jobs := scheduler.GetJobs()
	log.Printf("Registered %d jobs", len(jobs))
	for _, job := range jobs {
		if job["type"] == "daily" {
			log.Printf("- %s (daily at %s)", job["name"], job["daily_time"])
		} else {
			log.Printf("- %s (interval: %s)", job["name"], job["interval"])
		}
	}
}

func generatePriceNotification(allPrices map[string][]pricetracker.Price, err error) string {
	loc, _ := time.LoadLocation("Asia/Ho_Chi_Minh")
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

			count := len(prices)
			for i := 0; i < count; i++ {
				price := prices[i]
				for _, keyword := range keywords {
					if strings.Contains(strings.ToLower(price.Type), keyword) {
						message += fmt.Sprintf("• 💰%s: *%s* - *%s* - %s\n",
							price.Currency, formatPrice(price.BuyPrice), formatPrice(price.SellPrice), formatType(price.Type))
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
