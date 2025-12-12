package redis

import (
	"context"
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
	// Dobbiamo cercare in tutti i tree (non efficiente, ma per ora va bene)
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
