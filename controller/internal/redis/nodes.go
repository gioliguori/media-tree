package redis

import (
	"context"
	"fmt"
	"log"
	"strings"
)

// NodeData rappresenta quello che BaseNode salva in Redis
type NodeData struct {
	NodeId    string `redis:"nodeId"`
	NodeType  string `redis:"type"`
	TreeId    string `redis:"treeId"`
	Host      string `redis:"host"`
	Port      int    `redis:"port"`
	AudioPort int    `redis:"audioPort"`
	VideoPort int    `redis:"videoPort"`
	Layer     int    `redis:"layer"`
	Status    string `redis:"status"`
	Created   int64  `redis:"created"`
}

// ============================================
// POOL LAYER-BASED (NUOVO)
// ============================================

// AddNodeToPool aggiunge nodo a pool layer-based
// Struttura: tree:{treeId}:pool: layer:{layer}:{nodeType}
func (c *Client) AddNodeToPool(
	ctx context.Context,
	treeId string,
	nodeType string,
	layer int,
	nodeId string,
) error {
	// Nuova struttura: pool:layer:{X}:{type}
	key := fmt.Sprintf("tree:%s:pool:layer:%d:%s", treeId, layer, nodeType)

	if err := c.rdb.SAdd(ctx, key, nodeId).Err(); err != nil {
		return fmt.Errorf("failed to add node to pool:  %w", err)
	}

	log.Printf("[Redis] Added %s to pool %s", nodeId, key)
	return nil
}

// GetNodesAtLayer recupera tutti i nodi di un tipo a layer specifico
// Struttura: tree:{treeId}:pool:layer:{layer}:{nodeType}
func (c *Client) GetNodesAtLayer(
	ctx context.Context,
	treeId string,
	nodeType string,
	layer int,
) ([]string, error) {
	key := fmt.Sprintf("tree:%s:pool:layer:%d:%s", treeId, layer, nodeType)

	nodeIds, err := c.rdb.SMembers(ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get nodes at layer %d: %w", layer, err)
	}

	return nodeIds, nil
}

// GetAllInjectionNodes recupera tutti injection nodes di un tree
func (c *Client) GetAllInjectionNodes(ctx context.Context, treeId string) ([]string, error) {
	key := fmt.Sprintf("tree:%s:pool:layer:0: injection", treeId)
	return c.rdb.SMembers(ctx, key).Result()
}

// RemoveNodeFromPool rimuove nodo da tutti i pool
func (c *Client) RemoveNodeFromPool(
	ctx context.Context,
	treeId string,
	nodeId string,
) error {
	// Pattern: tree:{treeId}: pool:layer: *
	pattern := fmt.Sprintf("tree:%s:pool:layer:*", treeId)
	keys, err := c.Keys(ctx, pattern)
	if err != nil {
		return fmt.Errorf("failed to find pool keys: %w", err)
	}

	// Rimuovi da tutti i pool trovati
	for _, key := range keys {
		c.rdb.SRem(ctx, key, nodeId)
	}

	log.Printf("[Redis] Removed %s from all pools in tree %s", nodeId, treeId)
	return nil
}

// LEGACY (DEPRECATO - Mantenuto per compatibilità)

// GetNode legge un nodo da Redis (compatibilità con BaseNode. js)
// Chiave: tree:{treeId}: node:{nodeId}
func (c *Client) GetNode(ctx context.Context, treeId, nodeId string) (*NodeData, error) {
	key := fmt.Sprintf("tree:%s:node:%s", treeId, nodeId)

	var node NodeData

	if err := c.rdb.HGetAll(ctx, key).Scan(&node); err != nil {
		return nil, fmt.Errorf("failed to get node %s: %w", nodeId, err)
	}

	if node.NodeId == "" {
		return nil, fmt.Errorf("node not found: %s", nodeId)
	}

	return &node, nil
}

// GetTreeNodes legge tutti i nodi di un tree per tipo (DEPRECATO)
// Usa GetNodesAtLayer invece
func (c *Client) GetTreeNodes(ctx context.Context, treeId, nodeType string) ([]string, error) {
	// Fallback: cerca in tutti i layer
	var allNodes []string

	for layer := 0; layer <= 5; layer++ {
		nodes, err := c.GetNodesAtLayer(ctx, treeId, nodeType, layer)
		if err != nil {
			continue
		}
		allNodes = append(allNodes, nodes...)
	}

	return allNodes, nil
}

// GetAllTreeNodes legge TUTTI i nodi di un tree
func (c *Client) GetAllTreeNodes(ctx context.Context, treeId string) ([]*NodeData, error) {
	var allNodes []*NodeData
	skipped := 0

	// Cerca pattern tree:{treeId}:node: *
	pattern := fmt.Sprintf("tree:%s:node:*", treeId)
	keys, err := c.Keys(ctx, pattern)
	if err != nil {
		return nil, fmt.Errorf("failed to list nodes: %w", err)
	}

	for _, key := range keys {
		// Estrai nodeId da key
		nodeId := extractNodeIdFromKey(key)
		if nodeId == "" {
			continue
		}

		node, err := c.GetNode(ctx, treeId, nodeId)
		if err != nil {
			log.Printf("[WARN] Failed to get node %s: %v", nodeId, err)
			skipped++
			continue
		}
		allNodes = append(allNodes, node)
	}

	if skipped > 0 {
		log.Printf("[WARN] Skipped %d invalid nodes in tree %s", skipped, treeId)
	}

	return allNodes, nil
}

// Helper:  estrae nodeId da "tree:{treeId}:node:{nodeId}"
func extractNodeIdFromKey(key string) string {
	parts := strings.Split(key, ":")
	if len(parts) >= 4 && parts[2] == "node" {
		return parts[3]
	}
	return ""
}

// ============================================
// CLEANUP NODO
// ============================================

// ForceDeleteNode rimuove completamente un nodo da Redis
func (c *Client) ForceDeleteNode(
	ctx context.Context,
	treeId string,
	nodeId string,
	nodeType string,
) error {
	log.Printf("[Redis] Force deleting node %s from tree %s", nodeId, treeId)

	// 1. Rimuovi da pool layer-based
	if err := c.RemoveNodeFromPool(ctx, treeId, nodeId); err != nil {
		log.Printf("[WARN] Failed to remove from pool: %v", err)
	}

	// 2. Rimuovi provisioning info (salvato da Provisioner)
	provisioningKey := fmt.Sprintf("tree:%s:controller:node:%s", treeId, nodeId)
	if err := c.rdb.Del(ctx, provisioningKey).Err(); err != nil {
		log.Printf("[WARN] Failed to delete provisioning info: %v", err)
	}

	// 3. Rimuovi runtime info (salvato da BaseNode.js)
	runtimeKey := fmt.Sprintf("tree:%s:node:%s", treeId, nodeId)
	if err := c.rdb.Del(ctx, runtimeKey).Err(); err != nil {
		log.Printf("[WARN] Failed to delete runtime info: %v", err)
	}

	// 4. Rimuovi children (se è injection o relay-root)
	childrenKey := fmt.Sprintf("tree:%s:children:%s", treeId, nodeId)
	c.rdb.Del(ctx, childrenKey)

	// 5. Rimuovi parents
	parentsKey := fmt.Sprintf("tree:%s:parents:%s", treeId, nodeId)
	c.rdb.Del(ctx, parentsKey)

	log.Printf("[Redis] Node %s force deleted successfully", nodeId)
	return nil
}

// GetInjectionNodes recupera tutti i nodi injection di un tree
// Wrapper per compatibilità con codice esistente
func (c *Client) GetInjectionNodes(ctx context.Context, treeId string) ([]string, error) {
	return c.GetAllInjectionNodes(ctx, treeId)
}

// GetRelayNodes recupera tutti i nodi relay di un tree (tutti i layer)
func (c *Client) GetRelayNodes(ctx context.Context, treeId string) ([]string, error) {
	var allRelays []string

	// Cerca in tutti i layer (0-10)
	for layer := 0; layer <= 10; layer++ {
		relays, err := c.GetNodesAtLayer(ctx, treeId, "relay", layer)
		if err != nil {
			continue
		}
		allRelays = append(allRelays, relays...)
	}

	return allRelays, nil
}

// GetEgressNodes recupera tutti i nodi egress di un tree (tutti i layer)
func (c *Client) GetEgressNodes(ctx context.Context, treeId string) ([]string, error) {
	var allEgress []string

	// Cerca in tutti i layer (1-10)
	for layer := 1; layer <= 10; layer++ {
		egress, err := c.GetNodesAtLayer(ctx, treeId, "egress", layer)
		if err != nil {
			continue
		}
		allEgress = append(allEgress, egress...)
	}

	return allEgress, nil
}
