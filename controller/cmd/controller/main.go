package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"controller/internal/api"
	"controller/internal/config"
	"controller/internal/metrics"
	"controller/internal/redis"
	"controller/internal/session"
)

func main() {
	log.Println("Starting Media Tree Controller...")

	// Config redis
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	log.Printf("Connecting to Redis at %s:%d...", cfg.RedisHost, cfg.RedisPort)
	redisClient := redis.NewClient(cfg)

	defer func() {
		log.Println("Closing Redis connection...")
		redisClient.Close()
	}()
	// Timer 10 sec
	pingCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel() // cleanup

	if err := redisClient.Ping(pingCtx); err != nil {
		log.Fatalf("Failed to connect to Redis: %v", err)
	}

	log.Println("Connected to Redis successfully!")

	metricsConfig := metrics.DefaultConfig()
	metricsCollector, err := metrics.NewMetricsCollector(redisClient, metricsConfig)
	if err != nil {
		log.Printf("Metrics collector disabled: %v", err)
	} else {
		metricsCollector.Start(context.Background())
		defer metricsCollector.Stop()
		log.Println("MetricsCollector Started")
	}

	// Start session cleanup job
	sessionManager := session.NewSessionManager(redisClient)

	ctx := context.Background()
	sessionManager.StartCleanupJob(ctx)
	log.Println("Session cleanup job started")

	// Start API server
	server := api.NewServer(cfg, redisClient)

	// Go routine perchè server.Start() è bloccante
	go func() {
		if err := server.Start(); err != nil {
			log.Fatalf("Failed to start API server: %v", err)
		}
	}()

	// Channel OS
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// Si blocca qui in attesa di shutdown
	<-quit

	log.Println("Shutting down")

	// Shutdown con timeout
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("Server shutdown failed: %v", err)
	}

	log.Println("Controller stopped")
}
