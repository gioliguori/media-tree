package tree

import (
	"time"

	"controller/internal/domain"
)

type PoolStatus struct {
	TotalNodes     int       `json:"totalNodes"`
	InjectionNodes int       `json:"injectionNodes"`
	RelayNodes     int       `json:"relayNodes"`
	EgressNodes    int       `json:"egressNodes"`
	ActiveSessions int       `json:"activeSessions"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

// NodeSummary fornisce un riassunto dello stato di un singolo nodo nel pool
type NodeSummary struct {
	NodeId    string          `json:"nodeId"`
	NodeType  domain.NodeType `json:"nodeType"`
	Status    string          `json:"status"` // active, draining, destroying
	SlotsUsed int             `json:"slotsUsed"`
	SlotsMax  int             `json:"slotsMax"`
	CreatedAt time.Time       `json:"createdAt"`
}
