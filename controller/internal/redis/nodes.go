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

	// Rimuovi dalla lista globale
	globalKey := "global:active_nodes"
	member := fmt.Sprintf("%s:%s", treeId, nodeId)
	c.rdb.SRem(ctx, globalKey, member)

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

// SetNodeStatus imposta lo stato operativo del nodo (active, draining)
func (c *Client) SetNodeStatus(ctx context.Context, treeId string, nodeId string, status string) error {
	// Aggiorna stato locale del nodo
	nodeKey := fmt.Sprintf("tree:%s:node:%s", treeId, nodeId)
	if err := c.rdb.HSet(ctx, nodeKey, "status", status).Err(); err != nil {
		return fmt.Errorf("failed to update node status hash: %w", err)
	}

	// Sincronizza lista globale
	globalKey := "global:active_nodes"
	member := fmt.Sprintf("%s:%s", treeId, nodeId)

	switch status {
	case "destroying":
		// Rimuovi dalla lista globale
		if err := c.rdb.SRem(ctx, globalKey, member).Err(); err != nil {
			log.Printf("[WARN] Failed to remove %s from global list: %v", member, err)
		}

	case "active":
		// Aggiungi alla lista globale
		if err := c.rdb.SAdd(ctx, globalKey, member).Err(); err != nil {
			log.Printf("[WARN] Failed to add %s to global list: %v", member, err)
		}

	default:
	}

	return nil
}

// GetNodeStatus legge lo stato. Default: "active"
func (c *Client) GetNodeStatus(ctx context.Context, treeId string, nodeId string) (string, error) {
	key := fmt.Sprintf("tree:%s:node:%s", treeId, nodeId)

	status, err := c.rdb.HGet(ctx, key, "status").Result()
	if err != nil {
		if err.Error() == "redis:nil" {
			return "active", nil
		}
		return "", err
	}
	return status, nil
}

// GetNodeSessionCount conta le sessioni attive su un nodo
func (c *Client) GetNodeSessionCount(ctx context.Context, treeId string, nodeId string) (int64, error) {
	key := fmt.Sprintf("tree:%s:node:%s:sessions", treeId, nodeId)
	return c.rdb.SCard(ctx, key).Result()
}

// GetAllTreeNodes restituisce i nodi associati a un albero
func (c *Client) GetAllTreeNodes(ctx context.Context, treeId string) ([]string, error) {
	key := fmt.Sprintf("tree:%s:nodes", treeId)
	return c.rdb.SMembers(ctx, key).Result()
}
