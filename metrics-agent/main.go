package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/redis/go-redis/v9"
)

func main() {
	log.Println("Metrics Agent Starting")

	// Connessione Docker
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		log.Fatalf("Failed to connect to Docker: %v", err)
	}

	// Connessione Redis
	rdb := redis.NewClient(&redis.Options{
		Addr: "redis:6379",
	})

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		ctx := context.Background()
		// Lista container attivi
		containers, err := cli.ContainerList(ctx, container.ListOptions{})
		if err != nil {
			log.Printf("Error listing containers: %v", err)
			continue
		}

		for _, c := range containers {
			nodeId := c.Labels["media-mesh.nodeId"]
			cType := c.Labels["media-mesh.containerType"]

			if nodeId != "" {
				// Recupera stats non in streaming
				stats, err := cli.ContainerStats(ctx, c.ID, false)
				if err != nil {
					continue
				}

				var v container.StatsResponse
				if err := json.NewDecoder(stats.Body).Decode(&v); err != nil {
					stats.Body.Close()
					continue
				}
				stats.Body.Close()

				// Calcolo CPU%
				cpuDelta := float64(v.CPUStats.CPUUsage.TotalUsage - v.PreCPUStats.CPUUsage.TotalUsage)
				sysDelta := float64(v.CPUStats.SystemUsage - v.PreCPUStats.SystemUsage)
				cpuPercent := 0.0
				if sysDelta > 0.0 && cpuDelta > 0.0 {
					cpuPercent = (cpuDelta / sysDelta) * float64(v.CPUStats.OnlineCPUs) * 100.0
				}

				// Calcolo Memoria
				memMb := float64(v.MemoryStats.Usage) / 1024 / 1024

				prevNetKey := fmt.Sprintf("prev:net:%s:%s", nodeId, cType)
				prevNetStr, _ := rdb.Get(ctx, prevNetKey).Result()
				currentNet := v.Networks["eth0"].TxBytes

				var bandwidthMbps float64 = 0
				if prevNetStr != "" {
					var prevNet uint64
					fmt.Sscanf(prevNetStr, "%d", &prevNet)
					if currentNet >= prevNet {
						// Delta bit / secondi / 1.000.000 = Mbps
						bandwidthMbps = float64((currentNet-prevNet)*8) / 10.0 / 1000000.0
					}
				}
				rdb.Set(ctx, prevNetKey, currentNet, 30*time.Second)

				// Scrive su Redis: metrics:node:{nodeId}:{cType}
				key := fmt.Sprintf("metrics:node:%s:%s", nodeId, cType)
				metricsMap := map[string]any{
					"cpuPercent":      fmt.Sprintf("%.2f", cpuPercent),
					"memoryUsedMb":    fmt.Sprintf("%.2f", memMb),
					"bandwidthTxMbps": fmt.Sprintf("%.2f", bandwidthMbps),
					"containerId":     c.ID,
					"timestamp":       time.Now().Format(time.RFC3339),
				}

				rdb.HSet(ctx, key, metricsMap)
				rdb.Expire(ctx, key, 30*time.Second)
			}
		}
	}
}
