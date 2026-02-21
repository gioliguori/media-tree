package main

// import (
// 	"context"
// 	"controller/internal/config"
// 	"controller/internal/domain"
// 	"controller/internal/provisioner"
// 	"controller/internal/redis"
// 	"controller/internal/tree"
// 	"log"
// )

// func main() {
// 	log.Println("--- STARTING K8S RELAY TEST ---")

// 	cfg, _ := config.Load()
// 	redisClient := redis.NewClient(cfg)

// 	k8sProv, err := provisioner.NewK8sProvisioner(redisClient)
// 	if err != nil {
// 		log.Fatalf("Provisioner init failed: %v", err)
// 	}

// 	nodeManager := tree.NewTreeManager(redisClient, k8sProv)
// 	ctx := context.Background()

// 	// Lanciamo un Relay
// 	log.Println("[Test] Creating relay-standalone-1...")
// 	_, err = nodeManager.CreateNode(ctx, domain.NodeTypeRelay, "standalone")
// 	if err != nil {
// 		log.Fatalf("Test failed: %v", err)
// 	}

// 	log.Println("[Test] SUCCESS. Relay pod created and registered in Redis.")
// 	select {} // Mantieni il controller acceso
// }
