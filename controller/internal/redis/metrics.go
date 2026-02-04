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

// GetNodeMetrics legge metriche per un nodo (tutti container)
func (c *Client) GetNodeMetrics(ctx context.Context, nodeId string) (map[string]map[string]string, error) {
	pattern := fmt.Sprintf("metrics:node:%s:*", nodeId)
	keys, err := c.rdb.Keys(ctx, pattern).Result()
	if err != nil {
		return nil, err
	}

	if len(keys) == 0 {
		return nil, fmt.Errorf("no metrics found for node %s", nodeId)
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
func (c *Client) GetComponentMetrics(
	ctx context.Context,
	nodeId string,
	component string,
) (map[string]string, error) {
	key := fmt.Sprintf("metrics:node:%s:%s", nodeId, component)

	result, err := c.rdb.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get metrics for %s/%s:  %w", nodeId, component, err)
	}

	return result, nil
}

// GetNodeCPUPercent legge CPU percentage per un componente
// Ritorna 100.0 se metriche non disponibili (assume saturo)
func (c *Client) GetNodeCPUPercent(
	ctx context.Context,
	nodeId string,
	component string,
) (float64, error) {
	metrics, err := c.GetComponentMetrics(ctx, nodeId, component)
	if err != nil {
		return 0, fmt.Errorf("failed to get metrics:  %w", err)
	}

	cpuStr, ok := metrics["cpuPercent"]
	if !ok || cpuStr == "" {
		return 0, fmt.Errorf("cpuPercent field missing for %s/%s", nodeId, component)
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
	nodeId string,
	component string,
) (float64, error) {
	metrics, err := c.GetComponentMetrics(ctx, nodeId, component)
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

// GetNodeTotalViewers legge il totale dei viewer su un egress
func (c *Client) GetNodeTotalViewers(ctx context.Context, nodeId string) (int, error) {
	key := fmt.Sprintf("metrics:node:%s:janusStreaming", nodeId)
	val, err := c.rdb.HGet(ctx, key, "janusTotalViewers").Result()
	if err != nil {
		if err.Error() == "redis:nil" {
			return 0, nil
		}
		return 0, err
	}
	return strconv.Atoi(val)
}
