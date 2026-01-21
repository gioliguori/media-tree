package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"controller/internal/domain"
	"controller/internal/redis"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

// MetricsCollector raccoglie metriche Docker periodicamente
type MetricsCollector struct {
	dockerClient *client.Client
	redisClient  *redis.Client
	httpClient   *http.Client
	config       *MetricsCollectorConfig
	stopChan     chan struct{}
	running      bool

	//  Salva valori precedenti per calcolo CPU
	mu             sync.Mutex
	previousCPU    map[string]uint64 // containerID -> TotalUsage
	previousSystem map[string]uint64 // containerID -> SystemUsage

}

// metricsResult rappresenta il risultato della raccolta per un singolo nodo
type metricsResult struct {
	nodeRef        string
	containerCount int
	err            error
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
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		config:         config,
		stopChan:       make(chan struct{}),
		running:        false,
		previousCPU:    make(map[string]uint64),
		previousSystem: make(map[string]uint64),
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

	// Ottieni lista di tutti i nodi
	activeNodes, err := mc.redisClient.GetActiveNodes(ctx)
	if err != nil {
		return fmt.Errorf("failed to get active nodes: %w", err)
	}

	if len(activeNodes) == 0 {
		return nil // Nessun nodo attivo
	}

	// Map mutex
	var activeContainersMu sync.Mutex
	activeContainerIDs := make(map[string]bool)

	// Channel per risultati
	resultsCh := make(chan metricsResult, len(activeNodes))

	// Semaforo per concorrenza
	semaphore := make(chan struct{}, mc.config.MaxWorkers)

	// WaitGroup per sincronizzazione
	var wg sync.WaitGroup

	// Launch worker per ogni nodo
	for _, nodeRef := range activeNodes {
		wg.Add(1)

		go func(nodeRef string) {
			defer wg.Done()

			// Acquire semaphore
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			// Parse "tree-1:injection-1" -> ["tree-1", "injection-1"]
			parts := strings.Split(nodeRef, ":")
			if len(parts) != 2 {
				resultsCh <- metricsResult{
					nodeRef: nodeRef,
					err:     fmt.Errorf("invalid node ref format"),
				}
				return
			}
			treeID, nodeID := parts[0], parts[1]

			// Get node info
			node, err := mc.redisClient.GetNodeProvisioning(ctx, treeID, nodeID)
			if err != nil {
				resultsCh <- metricsResult{
					nodeRef: nodeRef,
					err:     fmt.Errorf("failed to get node info: %w", err),
				}
				return
			}

			activeContainersMu.Lock()
			if node.ContainerId != "" {
				activeContainerIDs[node.ContainerId] = true
			}
			if node.JanusContainerId != "" {
				activeContainerIDs[node.JanusContainerId] = true
			}
			activeContainersMu.Unlock()

			// Collect metrics con timeout per nodo
			containerCount, err := mc.collectNodeMetrics(ctx, treeID, node)
			resultsCh <- metricsResult{
				nodeRef:        nodeRef,
				containerCount: containerCount,
				err:            err,
			}

		}(nodeRef)
	}

	// Goroutine per chiudere channel quando tutto è fatto
	go func() {
		wg.Wait()
		close(resultsCh)
	}()

	// Raccogli risultati
	totalContainers := 0
	errorCount := 0

	for result := range resultsCh {
		if result.err != nil {
			log.Printf("[MetricsCollector] Error for %s: %v", result.nodeRef, result.err)
			errorCount++
		} else {
			totalContainers += result.containerCount
		}
	}

	duration := time.Since(startTime)
	if totalContainers > 0 || errorCount > 0 {
		log.Printf("[MetricsCollector] Collected metrics for %d nodes (%d containers, %d errors) in %v",
			len(activeNodes), totalContainers, errorCount, duration)
	}

	mc.cleanupStaleCPUCache(activeContainerIDs)

	return nil
}

// collectNodeMetrics raccoglie metriche per un singolo nodo
func (mc *MetricsCollector) collectNodeMetrics(ctx context.Context, treeID string, node *domain.NodeInfo) (int, error) {
	// Timeout specifico per nodo (10 secondi)
	nodeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	nodeMetrics := &NodeMetrics{
		NodeID:     node.NodeId,
		NodeType:   string(node.NodeType),
		TreeID:     treeID,
		Timestamp:  time.Now().Unix(),
		Containers: make(map[string]*ContainerMetrics),
	}

	containerCount := 0

	// Docker stats Node.js
	if node.ContainerId != "" {
		metrics, err := mc.getContainerMetrics(nodeCtx, node.ContainerId)
		if err != nil {
			log.Printf("[WARN] Skip container %s: %v", node.ContainerId, err)
		} else {
			nodeMetrics.Containers[string(ContainerTypeNodeJS)] = metrics
			containerCount++
		}
	}

	// Docker stats Janus + metriche Janus da Node.js
	if node.JanusContainerId != "" {
		// Get Docker stats
		containerMetrics, err := mc.getContainerMetrics(nodeCtx, node.JanusContainerId)
		if err != nil {
			log.Printf("[WARN] Skip Janus %s: %v", node.JanusContainerId, err)
		} else {
			// Get Janus metrics from Node.js endpoint
			janusMetrics, err := mc.getJanusMetricsFromNode(nodeCtx, node)
			if err != nil {
				log.Printf("[WARN] Skip Janus metrics for %s: %v", node.NodeId, err)
			} else {
				containerMetrics.JanusMetrics = janusMetrics
			}

			janusType := ContainerTypeJanusVideoroom
			if node.NodeType == "egress" {
				janusType = ContainerTypeJanusStreaming
			}
			nodeMetrics.Containers[string(janusType)] = containerMetrics
			containerCount++
		}
	}

	// Application metrics da Redis
	if node.NodeType == "relay" {
		appMetrics := mc.getApplicationMetricsFromRedis(nodeCtx, treeID, node)
		nodeMetrics.ApplicationMetrics = appMetrics
	}

	// GStreamer metrics da Relay /metrics endpoint
	if node.NodeType == "relay" {
		gstreamerMetrics, err := mc.getGStreamerMetricsFromNode(nodeCtx, node)
		if err != nil {
			log.Printf("[WARN] Skip GStreamer metrics for %s:  %v", node.NodeId, err)
		} else {
			nodeMetrics.GStreamerMetrics = gstreamerMetrics
		}
	}

	// Calcola bandwidth (relay)
	if node.NodeType == "relay" && nodeMetrics.ApplicationMetrics != nil {
		if containerMetrics, ok := nodeMetrics.Containers[string(ContainerTypeNodeJS)]; ok {
			bandwidthMbps := mc.calculateBandwidth(nodeCtx, node, containerMetrics.NetworkTxBytes)
			nodeMetrics.ApplicationMetrics.BandwidthTxMbps = bandwidthMbps
		}
	}

	// Salva
	if containerCount > 0 || nodeMetrics.ApplicationMetrics != nil || nodeMetrics.GStreamerMetrics != nil {
		if err := mc.saveNodeMetrics(nodeCtx, nodeMetrics); err != nil {
			return containerCount, fmt.Errorf("failed to save metrics: %w", err)
		}
	}

	return containerCount, nil
}

// getContainerMetrics ottiene metriche Docker per un singolo container
func (mc *MetricsCollector) getContainerMetrics(ctx context.Context, containerID string) (*ContainerMetrics, error) {
	statsCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	// una sola chiamata (stream=false)
	stats, err := mc.dockerClient.ContainerStats(statsCtx, containerID, false)
	if err != nil {
		if strings.Contains(err.Error(), "No such container") ||
			strings.Contains(err.Error(), "is not running") {
			return nil, fmt.Errorf("container %s not available: %w", containerID, err)
		}
		return nil, fmt.Errorf("failed to get container stats: %w", err)
	}
	defer stats.Body.Close()

	var v container.StatsResponse
	if err := json.NewDecoder(stats.Body).Decode(&v); err != nil {
		return nil, fmt.Errorf("failed to decode stats: %w", err)
	}

	containerInfo, err := mc.dockerClient.ContainerInspect(ctx, containerID)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect container: %w", err)
	}

	metrics := &ContainerMetrics{
		ContainerID:   containerID,
		ContainerName: strings.TrimPrefix(containerInfo.Name, "/"),
	}

	// leggi cpu limit del container
	cpuLimit := mc.getContainerCPULimit(containerInfo)
	if strings.Contains(containerInfo.Name, "janus-vr") {
		log.Printf("[DEBUG] Container %s:  cpuLimit=%.2f, NanoCPUs=%d",
			containerInfo.Name, cpuLimit, containerInfo.HostConfig.NanoCPUs)
	}

	mc.mu.Lock()
	previousCPU, hasPrev := mc.previousCPU[containerID]
	previousSystem, _ := mc.previousSystem[containerID]

	currentCPU := v.CPUStats.CPUUsage.TotalUsage
	currentSystem := v.CPUStats.SystemUsage

	// Salva valori correnti per prossimo ciclo
	mc.previousCPU[containerID] = currentCPU
	mc.previousSystem[containerID] = currentSystem
	mc.mu.Unlock()

	// Calcola CPU% usando valori del ciclo precedente
	if hasPrev && currentSystem > previousSystem && currentCPU > previousCPU {
		cpuDelta := float64(currentCPU - previousCPU)
		systemDelta := float64(currentSystem - previousSystem)

		numCPUs := float64(v.CPUStats.OnlineCPUs)
		if numCPUs == 0 {
			// Fallback:  prova PercpuUsage
			numCPUs = float64(len(v.CPUStats.CPUUsage.PercpuUsage))
		}
		if numCPUs == 0 {
			if cpuLimit > 0 {
				numCPUs = cpuLimit
			} else {
				numCPUs = 1.0 // Default:  1 CPU se nessun limit
			}
		}

		// grezzo (rispetto a tutte le CPU)
		cpuPercentRaw := (cpuDelta / systemDelta) * numCPUs * 100.0

		//if strings.Contains(containerInfo.Name, "janus-vr") {
		//	log.Printf("[DEBUG CPU] %s:  cpuDelta=%d, systemDelta=%d, numCPUs=%.0f, cpuPercentRaw=%.2f, cpuLimit=%.2f",
		//		containerInfo.Name,
		//		uint64(cpuDelta),
		//		uint64(systemDelta),
		//		numCPUs,
		//		cpuPercentRaw,
		//		cpuLimit)
		//}
		// Se container ha CPU limit, normalizza rispetto al limit
		if cpuLimit > 0 {
			// cpuPercentRaw rappresenta già "N CPU usate * 100"
			// Dividi semplicemente per il limit
			cpuPercentOfLimit := cpuPercentRaw / cpuLimit

			if strings.Contains(containerInfo.Name, "janus-vr") {
				log.Printf("[DEBUG CPU] %s: cpuPercentOfLimit=%.2f (capped at 100)",
					containerInfo.Name, cpuPercentOfLimit)
			}

			if cpuPercentOfLimit > 100.0 {
				cpuPercentOfLimit = 100.0
			}

			metrics.CPUPercent = cpuPercentOfLimit
		} else {
			// Nessun limit:  usa valore grezzo
			metrics.CPUPercent = cpuPercentRaw
		}
	} else {
		// Primo ciclo, nessun dato precedente
		metrics.CPUPercent = 0.0
	}

	// Memory
	metrics.MemoryUsedMB = float64(v.MemoryStats.Usage) / 1024 / 1024
	// metrics.MemoryLimitMB = float64(v.MemoryStats.Limit) / 1024 / 1024
	if v.MemoryStats.Limit > 0 {
		metrics.MemoryPercent = (float64(v.MemoryStats.Usage) / float64(v.MemoryStats.Limit)) * 100
	}

	// Network
	for _, netStats := range v.Networks {
		// metrics.NetworkRxBytes += netStats.RxBytes
		metrics.NetworkTxBytes += netStats.TxBytes
		// metrics.NetworkRxPackets += netStats.RxPackets
		// metrics.NetworkTxPackets += netStats.TxPackets
		// metrics.NetworkRxErrors += netStats.RxErrors
		// metrics.NetworkTxErrors += netStats.TxErrors
	}

	// Block I/O
	// for _, bioEntry := range v.BlkioStats.IoServiceBytesRecursive {
	// 	switch strings.ToLower(bioEntry.Op) {
	// 	case "read":
	// 		metrics.BlockReadBytes += bioEntry.Value
	// 	case "write":
	// 		metrics.BlockWriteBytes += bioEntry.Value
	// 	}
	// }

	// PIDs
	// metrics.PIDsCurrent = int(v.PidsStats.Current)

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

// saveNodeMetrics salva metriche nodo in Redis
func (mc *MetricsCollector) saveNodeMetrics(ctx context.Context, metrics *NodeMetrics) error {
	timestamp := time.Unix(metrics.Timestamp, 0)
	timestampReadable := timestamp.Format(time.RFC3339)

	pipe := mc.redisClient.Pipeline()

	// Container metrics con metriche Janus
	for containerType, containerMetrics := range metrics.Containers {
		key := fmt.Sprintf("metrics:%s:node:%s:%s", metrics.TreeID, metrics.NodeID, containerType)

		metricsMap := map[string]any{
			"containerId":   containerMetrics.ContainerID,
			"containerName": containerMetrics.ContainerName,
			"cpuPercent":    fmt.Sprintf("%.2f", containerMetrics.CPUPercent),
			"memoryUsedMb":  fmt.Sprintf("%.2f", containerMetrics.MemoryUsedMB),
			// "memoryLimitMb":    fmt.Sprintf("%.2f", containerMetrics.MemoryLimitMB),
			"memoryPercent": fmt.Sprintf("%.2f", containerMetrics.MemoryPercent),
			// "networkRxBytes":   fmt.Sprintf("%d", containerMetrics.NetworkRxBytes),
			"networkTxBytes": fmt.Sprintf("%d", containerMetrics.NetworkTxBytes),
			// "networkRxPackets": fmt.Sprintf("%d", containerMetrics.NetworkRxPackets),
			// "networkTxPackets": fmt.Sprintf("%d", containerMetrics.NetworkTxPackets),
			// "networkRxErrors":  fmt.Sprintf("%d", containerMetrics.NetworkRxErrors),
			// "networkTxErrors":  fmt.Sprintf("%d", containerMetrics.NetworkTxErrors),
			// "blockReadBytes":   fmt.Sprintf("%d", containerMetrics.BlockReadBytes),
			// "blockWriteBytes":  fmt.Sprintf("%d", containerMetrics.BlockWriteBytes),
			// "pidsCurrent":      fmt.Sprintf("%d", containerMetrics.PIDsCurrent),
			"timestamp": timestampReadable,
		}

		if containerMetrics.JanusMetrics != nil {
			metricsMap["janusRoomsActive"] = fmt.Sprintf("%d", containerMetrics.JanusMetrics.RoomsActive)
			metricsMap["janusMountpointsActive"] = fmt.Sprintf("%d", containerMetrics.JanusMetrics.MountpointsActive)
		}

		pipe.HMSet(ctx, key, metricsMap)
		pipe.Expire(ctx, key, mc.config.MetricsTTL)

		// Mountpoint details (egress)
		if containerMetrics.JanusMetrics != nil && len(containerMetrics.JanusMetrics.Mountpoints) > 0 {
			for _, mp := range containerMetrics.JanusMetrics.Mountpoints {
				mpKey := fmt.Sprintf("metrics:%s:node:%s:mountpoint:%d",
					metrics.TreeID, metrics.NodeID, mp.MountpointId)

				mpMap := map[string]any{
					"mountpointId": fmt.Sprintf("%d", mp.MountpointId),
					"description":  mp.Description,
					"viewers":      fmt.Sprintf("%d", mp.Viewers),
					"enabled":      fmt.Sprintf("%t", mp.Enabled),
					"ageMs":        fmt.Sprintf("%d", mp.AgeMs),
					"timestamp":    timestampReadable,
				}

				pipe.HMSet(ctx, mpKey, mpMap)
				pipe.Expire(ctx, mpKey, mc.config.MetricsTTL)
			}
		}

		// Room details (injection)
		if containerMetrics.JanusMetrics != nil && len(containerMetrics.JanusMetrics.Rooms) > 0 {
			for _, room := range containerMetrics.JanusMetrics.Rooms {
				roomKey := fmt.Sprintf("metrics:%s:node:%s:room:%d",
					metrics.TreeID, metrics.NodeID, room.RoomId)

				roomMap := map[string]any{
					"roomId":         fmt.Sprintf("%d", room.RoomId),
					"description":    room.Description,
					"hasPublisher":   fmt.Sprintf("%t", room.HasPublisher),
					"lastActivityAt": fmt.Sprintf("%d", room.LastActivityAt),
					"timestamp":      timestampReadable,
				}

				pipe.HMSet(ctx, roomKey, roomMap)
				pipe.Expire(ctx, roomKey, mc.config.MetricsTTL)
			}
		}
	}

	// GStreamer metrics (relay)
	if metrics.GStreamerMetrics != nil {
		key := fmt.Sprintf("metrics:%s:node:%s:gstreamer", metrics.TreeID, metrics.NodeID)

		gstreamerMap := map[string]any{
			"maxAudioQueueMs": fmt.Sprintf("%.2f", metrics.GStreamerMetrics.MaxAudioQueueMs),
			"maxVideoQueueMs": fmt.Sprintf("%.2f", metrics.GStreamerMetrics.MaxVideoQueueMs),
			"sessionCount":    fmt.Sprintf("%d", metrics.GStreamerMetrics.SessionCount),
			"timestamp":       timestampReadable,
		}

		pipe.HMSet(ctx, key, gstreamerMap)
		pipe.Expire(ctx, key, mc.config.MetricsTTL)
	}

	// Application metrics
	if metrics.ApplicationMetrics != nil && metrics.NodeType == "relay" {
		key := fmt.Sprintf("metrics:%s:node:%s:application", metrics.TreeID, metrics.NodeID)

		appMap := map[string]interface{}{
			"timestamp":         timestampReadable,
			"sessionsForwarded": fmt.Sprintf("%d", metrics.ApplicationMetrics.SessionsForwarded),
			"totalRoutes":       fmt.Sprintf("%d", metrics.ApplicationMetrics.TotalRoutes),
			"bandwidthTxMbps":   fmt.Sprintf("%.2f", metrics.ApplicationMetrics.BandwidthTxMbps),
		}

		pipe.HMSet(ctx, key, appMap)
		pipe.Expire(ctx, key, mc.config.MetricsTTL)
	}

	if metrics.NodeType == "injection" {
		if err := mc.updateInactiveSessions(ctx, metrics); err != nil {
			log.Printf("[MetricsCollector] Failed to update inactive sessions: %v", err)
		}
	}

	// Esegui tutto
	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("pipeline exec failed: %w", err)
	}

	return nil
}

// getApplicationMetricsFromRedis - Legge dati application da Redis
func (mc *MetricsCollector) getApplicationMetricsFromRedis(ctx context.Context, treeID string, node *domain.NodeInfo) *ApplicationMetrics {
	appMetrics := &ApplicationMetrics{}

	// Leggi sessioni da Redis
	sessions, err := mc.redisClient.GetNodeSessions(ctx, treeID, node.NodeId)
	if err != nil {
		sessions = []string{}
	}

	switch node.NodeType {
	case "injection", "egress":
		appMetrics.ActiveSessions = len(sessions)

	case "relay":
		appMetrics.SessionsForwarded = len(sessions)

		// Calcola totalRoutes
		totalRoutes := 0
		for _, sessionID := range sessions {
			routes, err := mc.redisClient.GetRoutes(ctx, treeID, sessionID, node.NodeId)
			if err == nil {
				totalRoutes += len(routes)
			}
		}
		appMetrics.TotalRoutes = totalRoutes
	}

	return appMetrics
}

// getJanusMetricsFromNode - Chiama /metrics del Node.js
func (mc *MetricsCollector) getJanusMetricsFromNode(ctx context.Context, node *domain.NodeInfo) (*JanusMetrics, error) {
	url := fmt.Sprintf("http://%s:%d/metrics", node.InternalHost, node.InternalAPIPort)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := mc.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var metricsResp NodeMetricsResponse
	if err := json.NewDecoder(resp.Body).Decode(&metricsResp); err != nil {
		return nil, fmt.Errorf("decode failed: %w", err)
	}

	return metricsResp.Janus, nil
}

// getGStreamerMetricsFromNode - Chiama /metrics del Relay Node. js
func (mc *MetricsCollector) getGStreamerMetricsFromNode(ctx context.Context, node *domain.NodeInfo) (*GStreamerMetrics, error) {
	url := fmt.Sprintf("http://%s:%d/metrics", node.InternalHost, node.InternalAPIPort)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := mc.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var metricsResp struct {
		GStreamer *GStreamerMetrics `json:"gstreamer"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&metricsResp); err != nil {
		return nil, fmt.Errorf("decode failed: %w", err)
	}

	return metricsResp.GStreamer, nil
}

// calculateBandwidth - Calcola Mbps da delta TX
func (mc *MetricsCollector) calculateBandwidth(ctx context.Context, node *domain.NodeInfo, currentTxBytes uint64) float64 {
	prevKey := fmt.Sprintf("metrics:%s:node:%s:prevTx", node.TreeId, node.NodeId)

	prevTxStr, err := mc.redisClient.Get(ctx, prevKey)
	if err != nil {
		// Prima volta, salva current
		mc.redisClient.SetWithTTL(ctx, prevKey, fmt.Sprintf("%d", currentTxBytes), mc.config.MetricsTTL)
		return 0.0
	}

	var prevTxBytes uint64
	fmt.Sscanf(prevTxStr, "%d", &prevTxBytes)

	// Check overflow/reset
	if currentTxBytes < prevTxBytes {
		mc.redisClient.SetWithTTL(ctx, prevKey, fmt.Sprintf("%d", currentTxBytes), mc.config.MetricsTTL)
		return 0.0
	}

	// Calcola delta
	bytesDelta := currentTxBytes - prevTxBytes
	intervalSec := mc.config.PollInterval.Seconds()
	bitrate := float64(bytesDelta*8) / intervalSec / 1_000_000 // Mbps

	// Salva current come prev
	mc.redisClient.SetWithTTL(ctx, prevKey, fmt.Sprintf("%d", currentTxBytes), mc.config.MetricsTTL)

	return bitrate
}

func (mc *MetricsCollector) updateInactiveSessions(ctx context.Context, metrics *NodeMetrics) error {
	sortedSetKey := fmt.Sprintf("inactive_sessions:%s", metrics.TreeID)

	// Get Janus metrics
	var janusMetrics *JanusMetrics
	for containerType, containerMetrics := range metrics.Containers {
		if containerType == string(ContainerTypeJanusVideoroom) {
			janusMetrics = containerMetrics.JanusMetrics
			break
		}
	}

	if janusMetrics == nil || len(janusMetrics.Rooms) == 0 {
		return nil
	}

	for _, room := range janusMetrics.Rooms {
		if room.SessionId == "" {
			continue
		}

		entryKey := fmt.Sprintf("%s:%s", metrics.TreeID, room.SessionId)

		if !room.HasPublisher {
			// inactive: Add to sorted set
			score := float64(room.LastActivityAt)

			err := mc.redisClient.ZAdd(ctx, sortedSetKey, score, entryKey)

			if err != nil {
				log.Printf("[MetricsCollector] Failed to add %s to sorted set: %v", entryKey, err)
			}

		} else {
			// active: Remove from sorted set
			err := mc.redisClient.ZRem(ctx, sortedSetKey, entryKey)

			if err != nil {
				log.Printf("[MetricsCollector] Failed to remove %s from sorted set: %v", entryKey, err)
			}
		}
	}

	return nil
}

func (mc *MetricsCollector) getContainerCPULimit(info types.ContainerJSON) float64 {
	// NanoCPU (1 CPU = 1e9 nanoCPU)
	if info.HostConfig.NanoCPUs > 0 {
		return float64(info.HostConfig.NanoCPUs) / 1e9
	}

	// CpuQuota e CpuPeriod (fallback)
	if info.HostConfig.CPUQuota > 0 && info.HostConfig.CPUPeriod > 0 {
		return float64(info.HostConfig.CPUQuota) / float64(info.HostConfig.CPUPeriod)
	}

	// Nessun limit
	return 0.0
}

func (mc *MetricsCollector) cleanupStaleCPUCache(activeContainerIDs map[string]bool) {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	staleCount := 0

	// Controlla ogni entry nella map
	for containerID := range mc.previousCPU {
		// Se containerID non è nella lista degli attivi, rimuovi
		if !activeContainerIDs[containerID] {
			delete(mc.previousCPU, containerID)
			delete(mc.previousSystem, containerID)
			staleCount++
		}
	}

	if staleCount > 0 {
		log.Printf("[MetricsCollector] Cleaned %d stale CPU cache entries", staleCount)
	}
}
