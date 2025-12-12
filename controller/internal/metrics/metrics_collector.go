package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container" // Nuovo import necessario
	"github.com/docker/docker/client"

	"controller/internal/domain"
	"controller/internal/redis"
)

// MetricsCollector raccoglie metriche Docker periodicamente
type MetricsCollector struct {
	dockerClient *client.Client
	redisClient  *redis.Client
	config       *MetricsCollectorConfig
	stopChan     chan struct{}
	running      bool
}

// NewMetricsCollector crea nuovo collector
func NewMetricsCollector(redisClient *redis.Client, config *MetricsCollectorConfig) (*MetricsCollector, error) {
	// Connetti a Docker daemon
	dockerClient, err := client.NewClientWithOpts(
		client.FromEnv,
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create Docker client: %w", err)
	}

	if config == nil {
		config = DefaultConfig()
	}

	return &MetricsCollector{
		dockerClient: dockerClient,
		redisClient:  redisClient,
		config:       config,
		stopChan:     make(chan struct{}),
		running:      false,
	}, nil
}

// Start avvia il collector in una goroutine
func (mc *MetricsCollector) Start(ctx context.Context) error {
	if !mc.config.Enabled {
		log.Println("[MetricsCollector] Disabled, not starting")
		return nil
	}

	if mc.running {
		return fmt.Errorf("collector already running")
	}

	mc.running = true
	log.Printf("[MetricsCollector] Starting with poll interval: %v", mc.config.PollInterval)

	// Goroutine per polling periodico
	go mc.collectLoop(ctx)

	return nil
}

// Stop ferma il collector
func (mc *MetricsCollector) Stop() error {
	if !mc.running {
		return nil
	}

	log.Println("[MetricsCollector] Stopping...")
	close(mc.stopChan)
	mc.running = false

	// Chiudi Docker client
	if mc.dockerClient != nil {
		return mc.dockerClient.Close()
	}

	return nil
}

// collectLoop loop principale di raccolta metriche
func (mc *MetricsCollector) collectLoop(ctx context.Context) {
	ticker := time.NewTicker(mc.config.PollInterval)
	defer ticker.Stop()

	// Prima raccolta immediata
	if err := mc.collectMetrics(ctx); err != nil {
		log.Printf("[MetricsCollector] Error during initial collection: %v", err)
	}

	for {
		select {
		case <-ticker.C:
			if err := mc.collectMetrics(ctx); err != nil {
				log.Printf("[MetricsCollector] Error collecting metrics: %v", err)
			}
		case <-mc.stopChan:
			log.Println("[MetricsCollector] Stopped")
			return
		case <-ctx.Done():
			log.Println("[MetricsCollector] Context cancelled")
			return
		}
	}
}

// collectMetrics raccoglie metriche per tutti i nodi attivi
func (mc *MetricsCollector) collectMetrics(ctx context.Context) error {
	startTime := time.Now()

	// 1. Ottieni lista di tutti i trees
	trees, err := mc.redisClient.Keys(ctx, "tree:*:metadata")
	if err != nil {
		return fmt.Errorf("failed to get trees: %w", err)
	}

	if len(trees) == 0 {
		// log.Println("[MetricsCollector] No active trees found") // Opzionale: commentato per ridurre spam nei log se vuoto
		return nil
	}

	totalNodes := 0
	totalContainers := 0

	// 2. Per ogni tree, raccogli metriche di tutti i nodi
	for _, treeKey := range trees {
		// Estrai treeId da "tree:{treeId}:metadata"
		treeID := extractTreeIDFromKey(treeKey)
		if treeID == "" {
			continue
		}

		// Ottieni tutti i nodi provisionati per questo tree
		nodes, err := mc.redisClient.GetAllProvisionedNodes(ctx, treeID)
		if err != nil {
			log.Printf("[MetricsCollector] Failed to get nodes for tree %s: %v", treeID, err)
			continue
		}

		totalNodes += len(nodes)

		// 3. Per ogni nodo, raccogli metriche dei suoi container
		for _, node := range nodes {
			containerCount, err := mc.collectNodeMetrics(ctx, treeID, node)
			if err != nil {
				log.Printf("[MetricsCollector] Failed to collect metrics for node %s: %v", node.NodeId, err)
				continue
			}
			totalContainers += containerCount
		}
	}

	duration := time.Since(startTime)
	// Logga solo se ci sono nodi, per evitare spam
	if totalNodes > 0 {
		log.Printf("[MetricsCollector] Collected metrics for %d nodes (%d containers) in %v",
			totalNodes, totalContainers, duration)
	}

	return nil
}

// collectNodeMetrics raccoglie metriche per un singolo nodo
func (mc *MetricsCollector) collectNodeMetrics(ctx context.Context, treeID string, node *domain.NodeInfo) (int, error) {
	nodeMetrics := &NodeMetrics{
		NodeID:     node.NodeId,
		NodeType:   string(node.NodeType),
		TreeID:     treeID,
		Timestamp:  time.Now().Unix(),
		Containers: make(map[string]*ContainerMetrics),
	}

	containerCount := 0

	// 1. Raccogli metriche del container Node.js principale
	if node.ContainerId != "" {
		metrics, err := mc.getContainerMetrics(ctx, node.ContainerId)
		if err != nil {
			log.Printf("[MetricsCollector] Failed to get metrics for container %s: %v", node.ContainerId, err)
		} else {
			nodeMetrics.Containers[string(ContainerTypeNodeJS)] = metrics
			containerCount++
		}
	}

	// 2. Se ha Janus, raccogli anche le sue metriche
	if node.JanusContainerId != "" {
		metrics, err := mc.getContainerMetrics(ctx, node.JanusContainerId)
		if err != nil {
			log.Printf("[MetricsCollector] Failed to get metrics for Janus %s: %v", node.JanusContainerId, err)
		} else {
			// Determina tipo Janus dal nodeType
			janusType := ContainerTypeJanusVideoRoom
			if node.NodeType == "egress" {
				janusType = ContainerTypeJanusStreaming
			}
			nodeMetrics.Containers[string(janusType)] = metrics
			containerCount++
		}
	}

	// 3. Salva metriche in Redis
	if err := mc.saveNodeMetrics(ctx, nodeMetrics); err != nil {
		return containerCount, fmt.Errorf("failed to save metrics: %w", err)
	}

	return containerCount, nil
}

// getContainerMetrics ottiene metriche Docker per un singolo container
func (mc *MetricsCollector) getContainerMetrics(ctx context.Context, containerID string) (*ContainerMetrics, error) {
	// Ottieni stats da Docker (stream=false per single snapshot)
	stats, err := mc.dockerClient.ContainerStats(ctx, containerID, false)
	if err != nil {
		return nil, fmt.Errorf("failed to get container stats: %w", err)
	}
	defer stats.Body.Close()

	// Decode JSON response usando il NUOVO tipo container.StatsResponse
	var v container.StatsResponse
	if err := json.NewDecoder(stats.Body).Decode(&v); err != nil {
		return nil, fmt.Errorf("failed to decode stats: %w", err)
	}

	// Ottieni info container per il nome
	containerInfo, err := mc.dockerClient.ContainerInspect(ctx, containerID)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect container: %w", err)
	}

	// Calcola metriche
	metrics := &ContainerMetrics{
		ContainerID:   containerID,
		ContainerName: strings.TrimPrefix(containerInfo.Name, "/"), // Rimuove slash iniziale
	}

	// CPU
	metrics.CPUPercent = calculateCPUPercent(&v)

	// Memory
	metrics.MemoryUsedMB = float64(v.MemoryStats.Usage) / 1024 / 1024
	metrics.MemoryLimitMB = float64(v.MemoryStats.Limit) / 1024 / 1024
	if v.MemoryStats.Limit > 0 {
		metrics.MemoryPercent = (float64(v.MemoryStats.Usage) / float64(v.MemoryStats.Limit)) * 100
	}

	// Network (somma tutte le interfacce)
	for _, netStats := range v.Networks {
		metrics.NetworkRxBytes += netStats.RxBytes
		metrics.NetworkTxBytes += netStats.TxBytes
		metrics.NetworkRxPackets += netStats.RxPackets
		metrics.NetworkTxPackets += netStats.TxPackets
		metrics.NetworkRxErrors += netStats.RxErrors
		metrics.NetworkTxErrors += netStats.TxErrors
	}

	// Block I/O
	for _, bioEntry := range v.BlkioStats.IoServiceBytesRecursive {
		switch strings.ToLower(bioEntry.Op) {
		case "read":
			metrics.BlockReadBytes += bioEntry.Value
		case "write":
			metrics.BlockWriteBytes += bioEntry.Value
		}
	}

	// PIDs
	metrics.PIDsCurrent = int(v.PidsStats.Current)

	return metrics, nil
}

// calculateCPUPercent calcola la percentuale CPU
func calculateCPUPercent(stats *container.StatsResponse) float64 {
	// Calcolo standard Docker stats
	cpuDelta := float64(stats.CPUStats.CPUUsage.TotalUsage - stats.PreCPUStats.CPUUsage.TotalUsage)
	systemDelta := float64(stats.CPUStats.SystemUsage - stats.PreCPUStats.SystemUsage)

	if systemDelta > 0.0 && cpuDelta > 0.0 {
		cpuPercent := (cpuDelta / systemDelta) * float64(len(stats.CPUStats.CPUUsage.PercpuUsage)) * 100.0
		return cpuPercent
	}

	return 0.0
}

// saveNodeMetrics salva metriche nodo in Redis usando HASH
func (mc *MetricsCollector) saveNodeMetrics(ctx context.Context, metrics *NodeMetrics) error {
	// Salva metriche per ogni container come HASH separato
	for containerType, containerMetrics := range metrics.Containers {
		// Chiave: metrics:tree:{treeId}:node:{nodeId}:{containerType}
		key := fmt.Sprintf("metrics:tree:%s:node:%s:%s", metrics.TreeID, metrics.NodeID, containerType)

		// Converti struct a map per HSET
		metricsMap := map[string]interface{}{
			"container_id":       containerMetrics.ContainerID,
			"container_name":     containerMetrics.ContainerName,
			"cpu_percent":        fmt.Sprintf("%.2f", containerMetrics.CPUPercent),
			"memory_used_mb":     fmt.Sprintf("%.2f", containerMetrics.MemoryUsedMB),
			"memory_limit_mb":    fmt.Sprintf("%.2f", containerMetrics.MemoryLimitMB),
			"memory_percent":     fmt.Sprintf("%.2f", containerMetrics.MemoryPercent),
			"network_rx_bytes":   fmt.Sprintf("%d", containerMetrics.NetworkRxBytes),
			"network_tx_bytes":   fmt.Sprintf("%d", containerMetrics.NetworkTxBytes),
			"network_rx_packets": fmt.Sprintf("%d", containerMetrics.NetworkRxPackets),
			"network_tx_packets": fmt.Sprintf("%d", containerMetrics.NetworkTxPackets),
			"network_rx_errors":  fmt.Sprintf("%d", containerMetrics.NetworkRxErrors),
			"network_tx_errors":  fmt.Sprintf("%d", containerMetrics.NetworkTxErrors),
			"block_read_bytes":   fmt.Sprintf("%d", containerMetrics.BlockReadBytes),
			"block_write_bytes":  fmt.Sprintf("%d", containerMetrics.BlockWriteBytes),
			"pids_current":       fmt.Sprintf("%d", containerMetrics.PIDsCurrent),
			"timestamp":          fmt.Sprintf("%d", metrics.Timestamp),
		}

		// Salva HASH in Redis
		if err := mc.redisClient.HMSet(ctx, key, metricsMap); err != nil {
			return fmt.Errorf("failed to save container %s metrics: %w", containerType, err)
		}

		// Imposta TTL sulla chiave
		if err := mc.redisClient.Expire(ctx, key, mc.config.MetricsTTL); err != nil {
			log.Printf("[MetricsCollector] Warning: failed to set TTL for %s: %v", key, err)
		}
	}

	return nil
}

// extractTreeIDFromKey estrae treeID da chiave Redis
func extractTreeIDFromKey(key string) string {
	// key = "tree:{treeId}:metadata"
	if len(key) < 6 {
		return ""
	}

	// Trova secondo ":"
	firstColon := 5 // dopo "tree:"
	secondColon := -1
	for i := firstColon; i < len(key); i++ {
		if key[i] == ':' {
			secondColon = i
			break
		}
	}

	if secondColon == -1 {
		return ""
	}

	return key[firstColon:secondColon]
}
