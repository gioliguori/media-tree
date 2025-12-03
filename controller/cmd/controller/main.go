package main

import (
	"context"
	"log"
	"time"

	"controller/internal/config"
	"controller/internal/redis"
)

func main() {
	log.Println("Starting Media Tree Controller...")

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	log.Printf("Connecting to Redis at %s:%d...", cfg.RedisHost, cfg.RedisPort)

	redisClient := redis.NewClient(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := redisClient.Ping(ctx); err != nil {
		log.Fatalf("Failed to connect to Redis: %v", err)
	}

	log.Println("Connected to Redis successfully!")

	select {}
}
