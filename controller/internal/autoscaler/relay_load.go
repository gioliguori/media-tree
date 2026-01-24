package autoscaler

import (
	"context"
	"fmt"
	"math"

	"controller/internal/redis"
)

const (
	RelayRootCPULimitPercent     = 80.0
	RelayRootQueueLatencyLimitMs = 200.0
)

type RelayRootLoadCalculator struct {
	redis *redis.Client
}

func NewRelayRootLoadCalculator(redisClient *redis.Client) *RelayRootLoadCalculator {
	return &RelayRootLoadCalculator{
		redis: redisClient,
	}
}

// CalculateRelayRootLoad determina il carico di un nodo Relay
// Worst Case: il carico è il massimo tra CPU e Latenza Code
func (calc *RelayRootLoadCalculator) CalculateRelayRootLoad(
	ctx context.Context,
	treeID string,
	relayRootID string,
) (float64, error) {

	cpuRelayRoot, err := calc.redis.GetNodeCPUPercent(ctx, treeID, relayRootID, "nodejs")
	if err != nil {
		return 100.0, fmt.Errorf("failed to get relay-root CPU: %w", err)
	}

	maxQueueLatency, err := calc.getQueueLatency(ctx, treeID, relayRootID)
	if err != nil {
		maxQueueLatency = 0.0
	}

	loadCPU := (cpuRelayRoot / RelayRootCPULimitPercent) * 100.0
	loadQueue := (maxQueueLatency / RelayRootQueueLatencyLimitMs) * 100.0

	totalLoad := math.Max(loadCPU, loadQueue)

	if totalLoad < 0 {
		totalLoad = 0
	}

	return totalLoad, nil
}

// getQueueLatency legge le metriche specifiche di GStreamer da Redis
func (calc *RelayRootLoadCalculator) getQueueLatency(
	ctx context.Context,
	treeID string,
	relayRootID string,
) (float64, error) {
	key := fmt.Sprintf("metrics:%s:node:%s:gstreamer", treeID, relayRootID)

	audioQueueStr, err := calc.redis.HGet(ctx, key, "maxAudioQueueMs")
	if err != nil {
		return 0, err
	}

	videoQueueStr, err := calc.redis.HGet(ctx, key, "maxVideoQueueMs")
	if err != nil {
		return 0, err
	}

	var audioQueue, videoQueue float64
	fmt.Sscanf(audioQueueStr, "%f", &audioQueue)
	fmt.Sscanf(videoQueueStr, "%f", &videoQueue)

	return math.Max(audioQueue, videoQueue), nil
}
