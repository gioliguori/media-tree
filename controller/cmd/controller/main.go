package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"controller/internal/api"
	"controller/internal/autoscaler"
	"controller/internal/config"
	"controller/internal/metrics"
	"controller/internal/provisioner"
	"controller/internal/redis"
	"controller/internal/session"
	"controller/internal/tree"
)

func main() {
	log.Println("Starting Media Tree Controller...")

	// Config e Database
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	log.Printf("Connecting to Redis at %s:%d...", cfg.RedisHost, cfg.RedisPort)
	redisClient := redis.NewClient(cfg)
	defer redisClient.Close()

	pingCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel() // cleanup
	if err := redisClient.Ping(pingCtx); err != nil {
		log.Fatalf("Failed to connect to Redis: %v", err)
	}
	log.Println("Connected to Redis successfully!")

	// Creazione entità

	// Provisioner (Docker)
	dockerProvisioner, err := provisioner.NewDockerProvisioner(cfg.DockerNetwork, redisClient)
	if err != nil {
		log.Fatalf("Failed to create Docker Provisioner: %v", err)
	}

	// Tree Manager
	treeManager := tree.NewTreeManager(redisClient, dockerProvisioner)

	// Session Manager
	sessionManager := session.NewSessionManager(redisClient)

	log.Println("Core Managers Initialized")

	// Albero di Default
	ctx := context.Background()
	defaultTreeID := "default-tree"
	if exists, _ := redisClient.TreeExists(ctx, defaultTreeID); !exists {
		log.Printf("Bootstrapping default tree: %s...", defaultTreeID)
		if _, err := treeManager.CreateTree(ctx, defaultTreeID, "minimal"); err != nil {
			log.Printf("Warning: Failed to create default tree: %v", err)
		} else {
			log.Println("Default tree created successfully")
		}
	} else {
		log.Println("Default tree already exists")
	}

	// Avvio background jobs

	// Metrics Collector
	metricsConfig := metrics.DefaultConfig()
	metricsCollector, err := metrics.NewMetricsCollector(redisClient, metricsConfig)
	if err != nil {
		log.Printf("Metrics collector disabled: %v", err)
	} else {
		metricsCollector.Start(context.Background())
		defer metricsCollector.Stop()
		log.Println("MetricsCollector Started")
	}

	// Session Cleanup
	sessionManager.StartCleanupJob(ctx)
	log.Println("Session cleanup job started")

	// Autoscaler Job
	autoscalerJob := autoscaler.NewAutoscalerJob(redisClient, treeManager)
	if err := autoscalerJob.Start(ctx); err != nil {
		log.Fatalf("Failed to start autoscaler: %v", err)
	}
	log.Println("Autoscaler job started")

	// Api Server
	server := api.NewServer(cfg, redisClient, treeManager, sessionManager)

	go func() {
		if err := server.Start(); err != nil {
			log.Fatalf("Failed to start API server: %v", err)
		}
	}()

	// Shutdown
	// Channel OS
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	// Si blocca qui in attesa di shutdown
	<-quit
	log.Println("Shutting down")
	// Shutdown con timeout
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	autoscalerJob.Stop()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("Server shutdown failed: %v", err)
	}

	log.Println("Controller stopped")
}
