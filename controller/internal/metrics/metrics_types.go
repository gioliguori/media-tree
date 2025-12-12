package metrics

import "time"

// ContainerMetrics rappresenta le metriche Docker di un singolo container
type ContainerMetrics struct {
	ContainerID   string  `json:"container_id"`
	ContainerName string  `json:"container_name"`
	CPUPercent    float64 `json:"cpu_percent"`
	MemoryUsedMB  float64 `json:"memory_used_mb"`
	MemoryLimitMB float64 `json:"memory_limit_mb"`
	MemoryPercent float64 `json:"memory_percent"`

	// Network metrics
	NetworkRxBytes   uint64 `json:"network_rx_bytes"`
	NetworkTxBytes   uint64 `json:"network_tx_bytes"`
	NetworkRxPackets uint64 `json:"network_rx_packets"`
	NetworkTxPackets uint64 `json:"network_tx_packets"`
	NetworkRxErrors  uint64 `json:"network_rx_errors"`
	NetworkTxErrors  uint64 `json:"network_tx_errors"`

	// Disk I/O
	BlockReadBytes  uint64 `json:"block_read_bytes"`
	BlockWriteBytes uint64 `json:"block_write_bytes"`

	// PIDs
	PIDsCurrent int `json:"pids_current"`
}

// NodeMetrics rappresenta le metriche aggregate per un nodo logico
type NodeMetrics struct {
	NodeID     string                       `json:"node_id"`
	NodeType   string                       `json:"node_type"`
	TreeID     string                       `json:"tree_id"`
	Timestamp  int64                        `json:"timestamp"`
	Containers map[string]*ContainerMetrics `json:"containers"`
}

// ContainerType identifica il tipo di container
type ContainerType string

const (
	ContainerTypeNodeJS         ContainerType = "nodejs"
	ContainerTypeJanusVideoRoom ContainerType = "janus_videoroom"
	ContainerTypeJanusStreaming ContainerType = "janus_streaming"
)

// MetricsCollectorConfig configurazione del collector
type MetricsCollectorConfig struct {
	PollInterval time.Duration // Intervallo polling (es. 10s)
	MetricsTTL   time.Duration // TTL metriche in Redis (es. 60s)
	Enabled      bool          // Enable/disable collector
}

// DefaultConfig ritorna configurazione di default
func DefaultConfig() *MetricsCollectorConfig {
	return &MetricsCollectorConfig{
		PollInterval: 10 * time.Second,
		MetricsTTL:   60 * time.Second,
		Enabled:      true,
	}
}
