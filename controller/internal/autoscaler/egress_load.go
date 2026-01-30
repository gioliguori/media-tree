package autoscaler

import (
	"context"
	"fmt"
	"math"
	"strconv"

	"controller/internal/redis"
)

const (
	EgressJanusCPULimitPercent = 80.0
	EgressCPULimitPercent      = 80.0

	// ora qui ma magari potrebbero appartenere a spec del nodo in maniera da avere egress node diversi
	MaxViewersPerEgress  = 10 // valori di test
	MaxSessionsPerEgress = 5  // valori di test

	EgressSaturatedThreshold = 80.0
)

type EgressLoadCalculator struct {
	redis *redis.Client
}

func NewEgressLoadCalculator(redisClient *redis.Client) *EgressLoadCalculator {
	return &EgressLoadCalculator{
		redis: redisClient,
	}
}

// CalculateEgressLoad calcola il carico basandosi su CPU, Viewers e Sessioni
func (calc *EgressLoadCalculator) CalculateEgressLoad(ctx context.Context, treeID string, nodeID string) (float64, error) {
	// Carico CPU Janus Streaming
	cpuJanus, err := calc.redis.GetNodeCPUPercent(ctx, treeID, nodeID, "janusStreaming")
	if err != nil {
		cpuJanus = 0
	}

	// carico cpu egress nodejs
	cpuNodejs, err := calc.redis.GetNodeCPUPercent(ctx, treeID, nodeID, "nodejs")
	if err != nil {
		cpuNodejs = 0
	}

	loadEgress := (cpuNodejs / EgressCPULimitPercent) * 100.0
	loadCPU := (cpuJanus / EgressJanusCPULimitPercent) * 100.0

	// Carico basato sui Viewer e Sessioni
	key := fmt.Sprintf("metrics:%s:node:%s:janusStreaming", treeID, nodeID)

	metrics, err := calc.redis.HGetAll(ctx, key)
	if err != nil {
		return 100.0, fmt.Errorf("failed to get egress metrics: %w", err)
	}

	usedViewers, _ := strconv.Atoi(metrics["janusTotalViewers"])
	usedSessions, _ := strconv.Atoi(metrics["janusMountpointsActive"])

	loadViewers := (float64(usedViewers) / float64(MaxViewersPerEgress)) * 100.0
	loadSessions := (float64(usedSessions) / float64(MaxSessionsPerEgress)) * 100.0

	// Il carico totale è il massimo dei fattori
	// Se uno solo uno è saturo, il nodo è considerato carico
	maxLoad := math.Max(loadEgress, math.Max(loadCPU, math.Max(loadViewers, loadSessions)))

	return maxLoad, nil
}
