package main

import (
	"encoding/json"
	"fmt"
	"log"

	"everything-you-need/m/services/price-tracker"
	"everything-you-need/m/services/tele-noti"
)

func main() {
	// Load configuration
	config, err := LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}
	
	fmt.Printf("=== %s ===\n", config.App.Name)
	fmt.Printf("Environment: %s\n", config.App.Environment)
	
	// Initialize price service
	priceService := pricetracker.NewPriceService()
	
	// Add price sources based on configuration
	if config.PriceSources.Doji.Enabled {
		if config.PriceSources.Doji.APIKey == "" {
			log.Println("Warning: Doji is enabled but no API key provided")
		} else {
			dojiSource := pricetracker.NewDojiSource(config.PriceSources.Doji.APIKey)
			priceService.AddSource(dojiSource)
			fmt.Println("✓ Added Doji source for gold prices")
		}
	}
	
	// Fetch and display prices
	allPrices, err := priceService.GetAllPrices()
	if err != nil {
		log.Fatalf("Failed to get prices: %v", err)
	}
	
	if len(allPrices) == 0 {
		fmt.Println("No price sources configured or available")
		return
	}
	
	for source, prices := range allPrices {
		fmt.Printf("\n--- Prices from %s ---\n", source)
		for _, price := range prices {
			fmt.Printf("Type: %s, Buy: %.0f, Sell: %.0f %s\n",
				price.Type, price.BuyPrice, price.SellPrice, price.Currency)
		}
	}
	
	// Send Telegram notifications if configured
	if config.Telegram.Enabled {
		if config.Telegram.BotToken == "" || config.Telegram.ChannelID == "" {
			log.Println("Warning: Telegram is enabled but bot_token or channel_id is missing")
		} else {
			fmt.Println("\n=== Sending Telegram Notification ===")
			
			teleService := telenoti.NewTeleNotiService(config.Telegram.BotToken)
			
			pricesJSON, _ := json.MarshalIndent(allPrices, "", "  ")
			message := fmt.Sprintf("🔔 Price Update from %s\n```json\n%s\n```", config.App.Name, string(pricesJSON))
			
			err = teleService.SendToChannelWithMarkdown(config.Telegram.ChannelID, message)
			if err != nil {
				log.Printf("Failed to send notification: %v", err)
			} else {
				fmt.Println("✓ Notification sent successfully!")
			}
		}
	} else {
		fmt.Println("\n📱 Telegram notifications disabled in configuration")
	}
}
