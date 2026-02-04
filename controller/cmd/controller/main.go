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

	// Node Manager
	nodeManager := tree.NewTreeManager(redisClient, dockerProvisioner)

	// Session Manager
	sessionManager := session.NewSessionManager(redisClient)

	log.Println("Core Managers Initialized")

	// Bootstrap
	ctx := context.Background()
	activeNodes, _ := redisClient.GetActiveNodes(ctx)

	if len(activeNodes) == 0 {
		log.Println("[Main] Mesh is empty. Bootstrapping minimum nodes...")
		if err := nodeManager.Bootstrap(ctx); err != nil {
			log.Printf("[WARN] Bootstrap failed: %v", err)
		}
		if err := dockerProvisioner.CreateAgent(ctx); err != nil {
			log.Printf("Failed to start metrics agent: %v", err)
		}
	} else {
		log.Printf("[Main] System already has %d active nodes", len(activeNodes))
	}

	// Avvio background jobs

	// Session Cleanup
	sessionManager.StartCleanupJob(ctx)
	log.Println("Session cleanup job started")

	// Autoscaler Job
	autoscalerJob := autoscaler.NewAutoscalerJob(redisClient, nodeManager)
	if err := autoscalerJob.Start(ctx); err != nil {
		log.Fatalf("Failed to start autoscaler: %v", err)
	}
	log.Println("Autoscaler job started")

	// Api Server
	server := api.NewServer(cfg, redisClient, nodeManager, sessionManager)

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
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer shutdownCancel()

	autoscalerJob.Stop()

	// Distruggi tutti i nodi fisici della mesh prima di uscire
	nodeManager.DestroyAllNodes(shutdownCtx)

	// Ferma il server API
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("Server shutdown error: %v", err)
	}
	log.Println("Controller stopped")
}
