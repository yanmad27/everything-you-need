package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"everything-you-need/m/services/config"
	jobscheduler "everything-you-need/m/services/job-scheduler"
	pricetracker "everything-you-need/m/services/price-tracker"
	telenoti "everything-you-need/m/services/tele-noti"
	usermanagement "everything-you-need/m/services/user-management"
)

func main() {
	cfg := config.ConfigMod.Resolve()

	log.Printf("=== %s ===\n", cfg.App.Name)
	log.Printf("Environment: %s\n", cfg.App.Environment)
	log.Printf("\n")

	priceService := pricetracker.PriceTrackerServiceMod.Resolve()
	teleService := telenoti.TeleNotiServiceMod.Resolve()

	scheduler := jobscheduler.NewJobScheduler()

	registerJobs(scheduler, priceService, teleService, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	scheduler.Start(ctx)

	log.Printf("\n🚀 Job scheduler started. Press Ctrl+C to stop.")

	// Start HTTP server if user management is enabled
	var httpServer *http.Server
	if cfg.UserManagement.Enabled {
		userService := usermanagement.UserManagementServiceMod.Resolve()

		// Create default admin user if needed
		err := userService.CreateDefaultAdmin(
			cfg.UserManagement.DefaultAdminUsername,
			cfg.UserManagement.DefaultAdminEmail,
			cfg.UserManagement.DefaultAdminPassword,
		)
		if err != nil {
			log.Printf("Warning: Failed to create default admin: %v", err)
		} else {
			log.Printf("✅ Default admin user initialized (username: %s)", cfg.UserManagement.DefaultAdminUsername)
		}

		// Create JWT manager
		jwtManager := usermanagement.NewJWTManager(
			cfg.UserManagement.JWTSecret,
			time.Duration(cfg.UserManagement.JWTExpirationHours)*time.Hour,
		)

		// Create HTTP handler
		mux := http.NewServeMux()
		userHandler := usermanagement.NewUserHandler(userService, jwtManager)
		userHandler.RegisterRoutes(mux)

		// Create HTTP server
		httpServer = &http.Server{
			Addr:    fmt.Sprintf(":%s", cfg.UserManagement.HTTPPort),
			Handler: mux,
		}

		// Start HTTP server in a goroutine
		go func() {
			log.Printf("🌐 HTTP server starting on port %s", cfg.UserManagement.HTTPPort)
			if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Printf("HTTP server error: %v", err)
			}
		}()
	}

	<-sigChan
	log.Printf("\n📴 Shutting down...")

	// Shutdown HTTP server if it was started
	if httpServer != nil {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			log.Printf("HTTP server shutdown error: %v", err)
		}
	}

	scheduler.Stop()
	cancel()

	log.Printf("✅ Shutdown complete")
}
