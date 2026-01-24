package redis

import (
	"context"
	"fmt"
	"log"
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
	indexKey := fmt.Sprintf("tree:%s:nodes", nodeInfo.TreeId)

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

	// salva HASH + aggiungi a entrambi gli index
	pipe := c.rdb.Pipeline()

	// Slva HASH nodo
	pipe.HSet(ctx, key, data)

	// Aggiungi a index tree
	pipe.SAdd(ctx, indexKey, nodeInfo.NodeId)

	// Aggiungi a index globale
	nodeRef := fmt.Sprintf("%s:%s", nodeInfo.TreeId, nodeInfo.NodeId)
	pipe.SAdd(ctx, "global:active_nodes", nodeRef)

	// Esegui atomicamente
	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("failed to save node provisioning: %w", err)
	}

	return nil
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

	// Internal network info (per comunicazione Docker tra nodi)
	dockerName := fmt.Sprintf("%s-%s", data.TreeId, data.NodeId)
	nodeInfo.InternalHost = dockerName

	// Porte API interne
	nodeInfo.InternalAPIPort = 7070
	nodeInfo.InternalRTPAudio = 5002
	nodeInfo.InternalRTPVideo = 5004

	// JanusHost dal nodeId
	switch nodeInfo.NodeType {
	case domain.NodeTypeInjection:
		nodeInfo.JanusHost = dockerName + "-janus-vr"
	case domain.NodeTypeEgress:
		nodeInfo.JanusHost = dockerName + "-janus-streaming"
	}

	return nodeInfo, nil
}

// GetActiveNodes ritorna tutti i nodi attivi
func (c *Client) GetActiveNodes(ctx context.Context) ([]string, error) {
	return c.rdb.SMembers(ctx, "global:active_nodes").Result()
}

// DeleteNodeProvisioning rimuove info provisioning da Redis
func (c *Client) DeleteNodeProvisioning(ctx context.Context, treeId, nodeId string) error {
	key := fmt.Sprintf("tree:%s:controller:node:%s", treeId, nodeId)
	indexKey := fmt.Sprintf("tree:%s:nodes", treeId)
	nodeRef := fmt.Sprintf("%s:%s", treeId, nodeId)

	// rimuovi HASH + rimuovi da entrambi gli index
	pipe := c.rdb.Pipeline()

	// Rimuovi da index globale
	pipe.SRem(ctx, "global:active_nodes", nodeRef)

	// Rimuovi da index tree
	pipe.SRem(ctx, indexKey, nodeId)

	// Rimuovi HASH nodo
	pipe.Del(ctx, key)

	// Esegui atomicamente
	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("failed to delete node provisioning: %w", err)
	}

	return nil
}

// GetAllProvisionedNodes ritorna tutti i nodi provisionati per un tree
func (c *Client) GetAllProvisionedNodes(ctx context.Context, treeId string) ([]*domain.NodeInfo, error) {
	indexKey := fmt.Sprintf("tree:%s:nodes", treeId)

	// ottieni tutti i nodeId
	nodeIds, err := c.rdb.SMembers(ctx, indexKey).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get node index: %w", err)
	}

	if len(nodeIds) == 0 {
		return []*domain.NodeInfo{}, nil
	}

	// PIPELINE per HGetAll
	pipe := c.rdb.Pipeline()
	cmds := make([]*redis.MapStringStringCmd, len(nodeIds))

	for i, nodeId := range nodeIds {
		key := fmt.Sprintf("tree:%s:controller:node:%s", treeId, nodeId)
		cmds[i] = pipe.HGetAll(ctx, key)
	}

	_, err = pipe.Exec(ctx)
	if err != nil && err != redis.Nil {
		return nil, fmt.Errorf("pipeline execution failed: %w", err)
	}

	// Scan risultati
	nodes := make([]*domain.NodeInfo, 0, len(nodeIds))

	for i, cmd := range cmds {
		var data NodeProvisioningData

		if err := cmd.Scan(&data); err != nil {
			log.Printf("[WARN] Error scanning node %s: %v", nodeIds[i], err)
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

		dockerName := fmt.Sprintf("%s-%s", data.TreeId, data.NodeId)
		nodeInfo.InternalHost = dockerName

		// Porte API interne
		nodeInfo.InternalAPIPort = 7070
		nodeInfo.InternalRTPAudio = 5002
		nodeInfo.InternalRTPVideo = 5004

		// Janus Host
		switch nodeInfo.NodeType {
		case domain.NodeTypeInjection:
			nodeInfo.JanusHost = dockerName + "-janus-vr"
		case domain.NodeTypeEgress:
			nodeInfo.JanusHost = dockerName + "-janus-streaming"
		}

		nodes = append(nodes, nodeInfo)
	}

	return nodes, nil
}
