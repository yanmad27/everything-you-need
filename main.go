package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"everything-you-need/m/services/config"
	jobscheduler "everything-you-need/m/services/job-scheduler"
	pricetracker "everything-you-need/m/services/price-tracker"
	telenoti "everything-you-need/m/services/tele-noti"
)

func main() {
	cfg := config.ConfigMod.Resolve()

	fmt.Printf("=== %s ===\n", cfg.App.Name)
	fmt.Printf("Environment: %s\n", cfg.App.Environment)

	priceService := pricetracker.PriceTrackerServiceMod.Resolve()
	teleService := telenoti.TeleNotiServiceMod.Resolve()

	scheduler := jobscheduler.NewSimpleScheduler()

	registerJobs(scheduler, priceService, teleService, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	scheduler.Start(ctx)

	log.Printf("\n🚀 Job scheduler started. Press Ctrl+C to stop.")

	<-sigChan
	log.Printf("\n📴 Shutting down...")

	scheduler.Stop()
	cancel()

	log.Printf("✅ Shutdown complete")
}
