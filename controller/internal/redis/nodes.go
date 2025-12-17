package redis

import (
	"context"
	"fmt"
	"log"
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

// Pool

// AddNodeToPool
// Struttura: tree:{treeId}:pool:layer:{layer}:{nodeType}
func (c *Client) AddNodeToPool(
	ctx context.Context,
	treeId string,
	nodeType string,
	layer int,
	nodeId string,
) error {
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

// RemoveNodeFromPool rimuove nodo da tutti i pool
func (c *Client) RemoveNodeFromPool(
	ctx context.Context,
	treeId string,
	nodeId string,
) error {
	// Pattern: tree:{treeId}:pool:layer:*
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

// GetNode legge info salvate al momento della registrazione
// Chiave: tree:{treeId}:node:{nodeId}
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

// GetInjectionNodes recupera tutti i nodi injection di un tree
func (c *Client) GetInjectionNodes(ctx context.Context, treeId string) ([]string, error) {
	key := fmt.Sprintf("tree:%s:pool:layer:0:injection", treeId)
	return c.rdb.SMembers(ctx, key).Result()
}

// Cleanup Nodo

// ForceDeleteNode rimuove completamente un nodo da Redis
func (c *Client) ForceDeleteNode(
	ctx context.Context,
	treeId string,
	nodeId string,
	nodeType string,
) error {
	log.Printf("[Redis] Force deleting node %s from tree %s", nodeId, treeId)

	// Rimuovi da pool
	if err := c.RemoveNodeFromPool(ctx, treeId, nodeId); err != nil {
		log.Printf("[WARN] Failed to remove from pool: %v", err)
	}

	// Rimuovi provisioning info (salvato da Provisioner)
	provisioningKey := fmt.Sprintf("tree:%s:controller:node:%s", treeId, nodeId)
	if err := c.rdb.Del(ctx, provisioningKey).Err(); err != nil {
		log.Printf("[WARN] Failed to delete provisioning info: %v", err)
	}

	// Rimuovi runtime info (salvato da BaseNode.js)
	runtimeKey := fmt.Sprintf("tree:%s:node:%s", treeId, nodeId)
	if err := c.rdb.Del(ctx, runtimeKey).Err(); err != nil {
		log.Printf("[WARN] Failed to delete runtime info: %v", err)
	}

	// Rimuovi children
	childrenKey := fmt.Sprintf("tree:%s:children:%s", treeId, nodeId)
	c.rdb.Del(ctx, childrenKey)

	// Rimuovi parents
	parentsKey := fmt.Sprintf("tree:%s:parents:%s", treeId, nodeId)
	c.rdb.Del(ctx, parentsKey)

	log.Printf("[Redis] Node %s force deleted successfully", nodeId)
	return nil
}
