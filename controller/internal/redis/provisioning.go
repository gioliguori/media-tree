package redis

import (
	"context"
	"fmt"
	"time"

	"controller/internal/domain"

	"github.com/redis/go-redis/v9"
)

// NodeProvisioningData è quello che il controller salva in Redis
type NodeProvisioningData struct {
	NodeId   string `json:"nodeId" redis:"nodeId"`
	NodeType string `json:"nodeType" redis:"nodeType"`
	TreeId   string `json:"treeId" redis:"treeId"`
	Layer    int    `json:"layer" redis:"layer"`

	// Docker
	ContainerId      string `json:"containerId" redis:"containerId"`
	JanusContainerId string `json:"janusContainerId,omitempty" redis:"janusContainerId"`

	// Port mappings
	ExternalAPIPort int `json:"externalAPIPort" redis:"externalAPIPort"`
	JanusHTTPPort   int `json:"janusHTTPPort,omitempty" redis:"janusHTTPPort"`
	JanusWSPort     int `json:"janusWSPort,omitempty" redis:"janusWSPort"`
	WebRTCPortStart int `json:"webrtcPortStart,omitempty" redis:"webrtcPortStart"`
	WebRTCPortEnd   int `json:"webrtcPortEnd,omitempty" redis:"webrtcPortEnd"`

	// Metadata
	CreatedBy string `json:"createdBy" redis:"createdBy"`
	CreatedAt int64  `json:"createdAt" redis:"createdAt"`
}

// SaveNodeProvisioning salva info provisioning in Redis
func (c *Client) SaveNodeProvisioning(ctx context.Context, nodeInfo *domain.NodeInfo) error {
	key := fmt.Sprintf("tree:%s:controller:node:%s", nodeInfo.TreeId, nodeInfo.NodeId)

	data := NodeProvisioningData{
		NodeId:           nodeInfo.NodeId,
		NodeType:         string(nodeInfo.NodeType),
		TreeId:           nodeInfo.TreeId,
		Layer:            nodeInfo.Layer,
		ContainerId:      nodeInfo.ContainerId,
		JanusContainerId: nodeInfo.JanusContainerId,
		ExternalAPIPort:  nodeInfo.ExternalAPIPort,
		JanusHTTPPort:    nodeInfo.JanusHTTPPort,
		JanusWSPort:      nodeInfo.JanusWSPort,
		WebRTCPortStart:  nodeInfo.WebRTCPortStart,
		WebRTCPortEnd:    nodeInfo.WebRTCPortEnd,
		CreatedBy:        "controller",
		CreatedAt:        time.Now().Unix(),
	}

	// HSet accetta direttamente la struct grazie ai tag redis
	// Non serve json.Marshal
	return c.rdb.HSet(ctx, key, data).Err()
}

// GetNodeProvisioning legge info provisioning da Redis
func (c *Client) GetNodeProvisioning(ctx context.Context, treeId, nodeId string) (*domain.NodeInfo, error) {
	key := fmt.Sprintf("tree:%s:controller:node:%s", treeId, nodeId)

	// Leggi tutto l'hash
	cmd := c.rdb.HGetAll(ctx, key)
	result, err := cmd.Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get provisioning: %w", err)
	}

	// Se la mappa è vuota, la chiave non esiste
	if len(result) == 0 {
		return nil, fmt.Errorf("node provisioning not found: %s", nodeId)
	}

	var data NodeProvisioningData
	// Scan riempie la struct convertendo le stringhe Redis nei tipi corretti
	if err := cmd.Scan(&data); err != nil {
		return nil, fmt.Errorf("failed to scan data: %w", err)
	}

	// Converti a NodeInfo
	nodeInfo := &domain.NodeInfo{
		NodeId:           data.NodeId,
		NodeType:         domain.NodeType(data.NodeType),
		TreeId:           data.TreeId,
		Layer:            data.Layer,
		ContainerId:      data.ContainerId,
		JanusContainerId: data.JanusContainerId,
		ExternalAPIPort:  data.ExternalAPIPort,
		JanusHTTPPort:    data.JanusHTTPPort,
		JanusWSPort:      data.JanusWSPort,
		WebRTCPortStart:  data.WebRTCPortStart,
		WebRTCPortEnd:    data.WebRTCPortEnd,
		ExternalHost:     "localhost",
	}

	// JanusHost dal nodeId
	switch nodeInfo.NodeType {
	case domain.NodeTypeInjection:
		nodeInfo.JanusHost = data.NodeId + "-janus-vr"
	case domain.NodeTypeEgress:
		nodeInfo.JanusHost = data.NodeId + "-janus-streaming"
	}

	return nodeInfo, nil
}

// DeleteNodeProvisioning rimuove info provisioning da Redis
func (c *Client) DeleteNodeProvisioning(ctx context.Context, treeId, nodeId string) error {
	key := fmt.Sprintf("tree:%s:controller:node:%s", treeId, nodeId)
	return c.rdb.Del(ctx, key).Err()
}

// GetAllProvisionedNodes ritorna tutti i nodi provisionati per un tree
func (c *Client) GetAllProvisionedNodes(ctx context.Context, treeId string) ([]*domain.NodeInfo, error) {
	pattern := fmt.Sprintf("tree:%s:controller:node:*", treeId)

	// KEYS è lento
	// sostituire KEYS con un SET Redis dedicato
	keys, err := c.rdb.Keys(ctx, pattern).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get keys: %w", err)
	}

	if len(keys) == 0 {
		return []*domain.NodeInfo{}, nil
	}

	// INIZIO PIPELINE
	pipe := c.rdb.Pipeline()

	// Accodiamo tutti i comandi HGetAll nel tubo
	cmds := make([]*redis.MapStringStringCmd, len(keys))
	for i, key := range keys {
		cmds[i] = pipe.HGetAll(ctx, key)
	}

	// Eseguiamo tutto
	_, err = pipe.Exec(ctx)
	if err != nil && err != redis.Nil {
		return nil, fmt.Errorf("pipeline execution failed: %w", err)
	}
	// --- FINE PIPELINE ---

	nodes := make([]*domain.NodeInfo, 0, len(keys))

	for i, cmd := range cmds {
		var data NodeProvisioningData

		// Scan lavora sul risultato locale, non chiama più Redis
		if err := cmd.Scan(&data); err != nil {
			fmt.Printf("Error scanning key %s: %v\n", keys[i], err)
			continue
		}

		if data.NodeId == "" {
			continue
		}

		// Mapping
		nodeInfo := &domain.NodeInfo{
			NodeId:           data.NodeId,
			NodeType:         domain.NodeType(data.NodeType),
			TreeId:           data.TreeId,
			Layer:            data.Layer,
			ContainerId:      data.ContainerId,
			JanusContainerId: data.JanusContainerId,
			ExternalAPIPort:  data.ExternalAPIPort,
			JanusHTTPPort:    data.JanusHTTPPort,
			JanusWSPort:      data.JanusWSPort,
			WebRTCPortStart:  data.WebRTCPortStart,
			WebRTCPortEnd:    data.WebRTCPortEnd,
			ExternalHost:     "localhost",
		}

		// Janus Host
		switch nodeInfo.NodeType {
		case domain.NodeTypeInjection:
			nodeInfo.JanusHost = data.NodeId + "-janus-vr"
		case domain.NodeTypeEgress:
			nodeInfo.JanusHost = data.NodeId + "-janus-streaming"
		}

		nodes = append(nodes, nodeInfo)
	}

	return nodes, nil
}
