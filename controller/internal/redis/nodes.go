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
	Role      string `redis:"role"`
	Host      string `redis:"host"`
	Port      int    `redis:"port"`
	AudioPort int    `redis:"audioPort"`
	VideoPort int    `redis:"videoPort"`
	Status    string `redis:"status"`
	Created   int64  `redis:"created"`
}

// Pool

// AddNodeToPool
// struttura: pool:{nodeType}
func (c *Client) AddNodeToPool(
	ctx context.Context,
	nodeType string,
	nodeId string,
) error {
	key := fmt.Sprintf("pool:%s", nodeType)

	if err := c.rdb.SAdd(ctx, key, nodeId).Err(); err != nil {
		return fmt.Errorf("failed to add node to pool:  %w", err)
	}

	log.Printf("[Redis] Added %s to pool %s", nodeId, key)
	return nil
}

// RemoveNodeFromPool rimuove nodo da tutti i pool
func (c *Client) RemoveNodeFromPool(
	ctx context.Context,
	nodeType string,
	nodeId string,
) error {
	key := fmt.Sprintf("pool:%s", nodeType)
	return c.rdb.SRem(ctx, key, nodeId).Err()
}

func (c *Client) GetNode(ctx context.Context, nodeId string) (*NodeData, error) {
	key := fmt.Sprintf("node:%s", nodeId)

	var node NodeData

	if err := c.rdb.HGetAll(ctx, key).Scan(&node); err != nil {
		return nil, fmt.Errorf("failed to get node %s: %w", nodeId, err)
	}

	if node.NodeId == "" {
		return nil, fmt.Errorf("node not found: %s", nodeId)
	}

	return &node, nil
}

func (c *Client) GetNodePool(ctx context.Context, poolName string) ([]string, error) {
	key := fmt.Sprintf("pool:%s", poolName)
	return c.rdb.SMembers(ctx, key).Result()
}

// Cleanup Nodo

// ForceDeleteNode rimuove completamente un nodo da Redis
func (c *Client) ForceDeleteNode(
	ctx context.Context,
	nodeId string,
	nodeType string,
) error {
	log.Printf("[Redis] Force deleting node %s", nodeId)

	// Rimuovi dalla lista globale
	c.rdb.SRem(ctx, "global:active_nodes", nodeId)

	// Rimuovi da pool
	c.RemoveNodeFromPool(ctx, nodeType, nodeId)

	keysToDelete := []string{
		fmt.Sprintf("node:%s:provisioning", nodeId),
		fmt.Sprintf("node:%s", nodeId),
		fmt.Sprintf("node:%s:children", nodeId),
		fmt.Sprintf("node:%s:parents", nodeId),
		fmt.Sprintf("node:%s:sessions", nodeId),
		// Chiavi metriche note
		fmt.Sprintf("metrics:node:%s:application", nodeId),
		fmt.Sprintf("metrics:node:%s:nodejs", nodeId),
		fmt.Sprintf("metrics:node:%s:janus", nodeId),
		fmt.Sprintf("metrics:node:%s:janusVideoroom", nodeId),
		fmt.Sprintf("metrics:node:%s:janusStreaming", nodeId),
		fmt.Sprintf("metrics:node:%s:gstreamer", nodeId),
	}

	// chiavi dinamiche (stanze o mountpoint)
	dynamicPattern := fmt.Sprintf("metrics:node:%s:*", nodeId)
	dynamicKeys, _ := c.rdb.Keys(ctx, dynamicPattern).Result()
	keysToDelete = append(keysToDelete, dynamicKeys...)

	if len(keysToDelete) > 0 {
		return c.rdb.Del(ctx, keysToDelete...).Err()
	}

	log.Printf("[Redis] Node %s force deleted successfully", nodeId)
	return nil
}

// SetNodeStatus imposta lo stato operativo del nodo (active, draining)
func (c *Client) SetNodeStatus(ctx context.Context, nodeId string, status string) error {
	// Aggiorna stato locale del nodo
	key := fmt.Sprintf("node:%s", nodeId)
	if err := c.rdb.HSet(ctx, key, "status", status).Err(); err != nil {
		return fmt.Errorf("failed to update node status hash: %w", err)
	}

	// Sincronizza lista globale
	globalKey := "global:active_nodes"

	switch status {
	case "destroying":
		// Rimuovi dalla lista globale
		if err := c.rdb.SRem(ctx, globalKey, nodeId).Err(); err != nil {
			log.Printf("[WARN] Failed to remove %s from global list: %v", nodeId, err)
		}

	case "active":
		// Aggiungi alla lista globale
		if err := c.rdb.SAdd(ctx, globalKey, nodeId).Err(); err != nil {
			log.Printf("[WARN] Failed to add %s to global list: %v", nodeId, err)
		}

	default:
	}

	return nil
}

// GetNodeStatus legge lo stato. Default: "active"
func (c *Client) GetNodeStatus(ctx context.Context, nodeId string) (string, error) {
	key := fmt.Sprintf("node:%s", nodeId)

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
func (c *Client) GetNodeSessionCount(ctx context.Context, nodeId string) (int64, error) {
	key := fmt.Sprintf("node:%s:sessions", nodeId)
	return c.rdb.SCard(ctx, key).Result()
}
