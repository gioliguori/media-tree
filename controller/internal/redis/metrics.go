package redis

import (
	"context"
	"fmt"
	"strconv"
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
	pattern := fmt.Sprintf("metrics:%s:node:%s:*", treeID, nodeID)
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

// GetComponentMetrics legge metriche per un componente specifico
// pattern: metrics:{treeId}:node:{nodeId}:{component}
// Components: "nodejs", "janusVideoroom", "janusStreaming", "application"
func (c *Client) GetComponentMetrics(
	ctx context.Context,
	treeID string,
	nodeID string,
	component string,
) (map[string]string, error) {
	key := fmt.Sprintf("metrics:%s:node:%s:%s", treeID, nodeID, component)

	result, err := c.rdb.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get metrics for %s/%s:  %w", nodeID, component, err)
	}

	return result, nil
}

// GetNodeCPUPercent legge CPU percentage per un componente
// Ritorna 100.0 se metriche non disponibili (assume saturo)
func (c *Client) GetNodeCPUPercent(
	ctx context.Context,
	treeID string,
	nodeID string,
	component string,
) (float64, error) {
	metrics, err := c.GetComponentMetrics(ctx, treeID, nodeID, component)
	if err != nil {
		return 0, fmt.Errorf("failed to get metrics:  %w", err)
	}

	cpuStr, ok := metrics["cpuPercent"]
	if !ok || cpuStr == "" {
		return 0, fmt.Errorf("cpuPercent field missing for %s/%s", nodeID, component)
	}

	cpu, err := strconv.ParseFloat(cpuStr, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid cpuPercent value '%s': %w", cpuStr, err)
	}

	return cpu, nil
}

// GetNodeBandwidthTxMbps legge bandwidth TX in Mbps
// Ritorna 999.0 se metriche non disponibili
func (c *Client) GetNodeBandwidthTxMbps(
	ctx context.Context,
	treeID string,
	nodeID string,
	component string,
) (float64, error) {
	metrics, err := c.GetComponentMetrics(ctx, treeID, nodeID, component)
	if err != nil {
		return 0, fmt.Errorf("failed to get metrics: %w", err)
	}

	bwStr, ok := metrics["bandwidthTxMbps"]
	if !ok || bwStr == "" {
		return 0, fmt.Errorf("bandwidthTxMbps field missing")

	}

	bw, err := strconv.ParseFloat(bwStr, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid bandwidthTxMbps value '%s': %w", bwStr, err)
	}

	return bw, nil
}
