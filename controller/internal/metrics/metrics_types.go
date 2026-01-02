package metrics

import "time"

// ContainerMetrics rappresenta le metriche Docker di un singolo container
type ContainerMetrics struct {
	ContainerID   string  `json:"containerId"`
	ContainerName string  `json:"containerName"`
	CPUPercent    float64 `json:"cpuPercent"`
	MemoryUsedMB  float64 `json:"memoryUsedMb"`
	MemoryLimitMB float64 `json:"memoryLimitMb"`
	MemoryPercent float64 `json:"memoryPercent"`

	// Network metrics
	NetworkRxBytes   uint64 `json:"networkRxBytes"`
	NetworkTxBytes   uint64 `json:"networkTxBytes"`
	NetworkRxPackets uint64 `json:"networkRxPackets"`
	NetworkTxPackets uint64 `json:"networkTxPackets"`
	NetworkRxErrors  uint64 `json:"networkRxErrors"`
	NetworkTxErrors  uint64 `json:"networkTxErrors"`

	// Disk I/O
	BlockReadBytes  uint64 `json:"blockReadBytes"`
	BlockWriteBytes uint64 `json:"blockWriteBytes"`

	// PIDs
	PIDsCurrent int `json:"pidsCurrent"`

	JanusMetrics *JanusMetrics `json:"janus,omitempty"`
}

// ApplicationMetrics
type ApplicationMetrics struct {
	ActiveSessions    int     `json:"activeSessions"`              // Injection/Egress
	SessionsForwarded int     `json:"sessionsForwarded,omitempty"` // Relay
	TotalRoutes       int     `json:"totalRoutes,omitempty"`       // Relay
	BandwidthTxMbps   float64 `json:"bandwidthTxMbps,omitempty"`   // Relay (calcolato)
}

// JanusMetrics - Metriche da Janus API
type JanusMetrics struct {
	// VideoRoom (injection)
	RoomsActive int          `json:"roomsActive,omitempty"` // Numero room attive
	Rooms       []RoomMetric `json:"rooms,omitempty"`       // Dettaglio room

	// Streaming (egress)
	MountpointsActive int                `json:"mountpointsActive,omitempty"` // Numero mountpoint attivi
	Mountpoints       []MountpointMetric `json:"mountpoints,omitempty"`       // Dettaglio mountpoint
}

type MountpointMetric struct {
	MountpointId int    `json:"mountpointId"`
	Description  string `json:"description,omitempty"`
	Viewers      int    `json:"viewers"`
	Enabled      bool   `json:"enabled"`
	AgeMs        int64  `json:"ageMs"`
}

type RoomMetric struct {
	RoomId         int    `json:"roomId"`
	Description    string `json:"description,omitempty"`
	HasPublisher   bool   `json:"hasPublisher"`
	LastActivityAt int64  `json:"lastActivityAt"`
}

type NodeMetricsResponse struct {
	NodeID    string        `json:"nodeId"`
	NodeType  string        `json:"nodeType"`
	TreeID    string        `json:"treeId"`
	Timestamp int64         `json:"timestamp"`
	Janus     *JanusMetrics `json:"janus,omitempty"`
}

// NodeMetrics rappresenta le metriche aggregate per un nodo logico
type NodeMetrics struct {
	NodeID             string                       `json:"nodeId"`
	NodeType           string                       `json:"nodeType"`
	TreeID             string                       `json:"treeId"`
	Timestamp          int64                        `json:"timestamp"`
	Containers         map[string]*ContainerMetrics `json:"containers"`
	ApplicationMetrics *ApplicationMetrics          `json:"application,omitempty"`
}

// ContainerType identifica il tipo di container
type ContainerType string

const (
	ContainerTypeNodeJS         ContainerType = "nodejs"
	ContainerTypeJanusVideoroom ContainerType = "janusVideoroom"
	ContainerTypeJanusStreaming ContainerType = "janusStreaming"
)

// MetricsCollectorConfig configurazione del collector
type MetricsCollectorConfig struct {
	PollInterval time.Duration // Intervallo polling
	MetricsTTL   time.Duration // TTL metriche in Redis
	MaxWorkers   int           // Max goroutine concorrenti
	Enabled      bool          // Enable/disable collector
}

// DefaultConfig ritorna configurazione di default
func DefaultConfig() *MetricsCollectorConfig {
	pollInterval := 10 * time.Second
	return &MetricsCollectorConfig{
		PollInterval: pollInterval,
		MetricsTTL:   pollInterval * 3,
		MaxWorkers:   50,
		Enabled:      true,
	}
}
