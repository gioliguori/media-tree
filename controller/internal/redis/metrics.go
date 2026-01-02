package redis

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// SetWithTTL salva una chiave con TTL
func (c *Client) SetWithTTL(ctx context.Context, key string, value string, ttl time.Duration) error {
	return c.rdb.Set(ctx, key, value, ttl).Err()
}

// Expire imposta TTL su una chiave esistente
func (c *Client) Expire(ctx context.Context, key string, ttl time.Duration) error {
	return c.rdb.Expire(ctx, key, ttl).Err()
}

// Get legge valore di una chiave
func (c *Client) Get(ctx context.Context, key string) (string, error) {
	return c.rdb.Get(ctx, key).Result()
}

// GetNodeProvisioningByContainerID trova nodeInfo dal containerID
// Utile per il metrics collector
func (c *Client) GetNodeProvisioningByContainerID(ctx context.Context, containerID string) (*NodeProvisioningData, error) {
	// Pattern: tree:*:controller:node:*
	// Dobbiamo cercare in tutti i tree (non efficiente, per ora ok)
	pattern := "tree:*:controller:node:*"
	keys, err := c.Keys(ctx, pattern)
	if err != nil {
		return nil, err
	}

	// Cerca tra tutte le chiavi quella con questo containerID
	for _, key := range keys {
		var data NodeProvisioningData
		if err := c.rdb.HGetAll(ctx, key).Scan(&data); err != nil {
			continue
		}

		// Controlla se questo nodo ha il container che cerchiamo
		if data.ContainerId == containerID || data.JanusContainerId == containerID {
			return &data, nil
		}
	}

	return nil, nil // Non trovato
}

// GetNodeMetrics legge metriche per un nodo (tutti container)
func (c *Client) GetNodeMetrics(ctx context.Context, treeID, nodeID string) (map[string]map[string]string, error) {
	pattern := fmt.Sprintf("metrics:%s: node:%s: *", treeID, nodeID)
	keys, err := c.rdb.Keys(ctx, pattern).Result()
	if err != nil {
		return nil, err
	}

	if len(keys) == 0 {
		return nil, fmt.Errorf("no metrics found for node %s", nodeID)
	}

	result := make(map[string]map[string]string)
	for _, key := range keys {
		parts := strings.Split(key, ":")
		containerType := parts[len(parts)-1]

		data, err := c.rdb.HGetAll(ctx, key).Result()
		if err != nil {
			continue
		}
		result[containerType] = data
	}

	return result, nil
}
