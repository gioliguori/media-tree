package autoscaler

import (
	"context"
	"controller/internal/redis"
	"fmt"
	"strconv"
)

const (
	EgressJanusCpuThreshold = 80.0
	EgressMaxViewers        = 10 // Valore di test
)

type EgressPoolReport struct {
	TotalNodes          int
	SaturatedNodesCount int
	TotalViewers        int
	TotalFreeSlots      int
	AvgHardwareLoad     float64
}

type EgressLoadCalculator struct {
	redis *redis.Client
}

func NewEgressLoadCalculator(redisClient *redis.Client) *EgressLoadCalculator {
	return &EgressLoadCalculator{redis: redisClient}
}

func (calc *EgressLoadCalculator) GetPoolReport(ctx context.Context) (*EgressPoolReport, error) {
	nodeIds, err := calc.redis.GetNodePool(ctx, "egress")
	if err != nil {
		return nil, err
	}

	report := &EgressPoolReport{TotalNodes: len(nodeIds)}
	var totalHardwareLoad float64

	for _, id := range nodeIds {
		status, _ := calc.redis.GetNodeStatus(ctx, id)
		if status != "active" {
			continue
		}
		// Hardware Load
		cpuJanus, _ := calc.redis.GetNodeCPUPercent(ctx, id, "janusStreaming")
		hwLoad := (cpuJanus / EgressJanusCpuThreshold) * 100.0
		totalHardwareLoad += hwLoad

		// Logic Load
		key := fmt.Sprintf("metrics:node:%s:janusStreaming", id)
		metrics, _ := calc.redis.HGetAll(ctx, key)
		viewers, _ := strconv.Atoi(metrics["janusTotalViewers"])
		report.TotalViewers += viewers

		// Un nodo è saturo (soglia di allerta) se ha viewers >= 80% o CPU > 80%
		if viewers >= (EgressMaxViewers*0.8) || hwLoad >= 100.0 {
			report.SaturatedNodesCount++
		}
		if hwLoad < 100.0 {
			free := EgressMaxViewers - viewers
			if free > 0 {
				report.TotalFreeSlots += free
			}
		}
	}

	if report.TotalNodes > 0 {
		report.AvgHardwareLoad = totalHardwareLoad / float64(report.TotalNodes)
	}

	return report, nil
}

func (calc *EgressLoadCalculator) IsNodeSaturated(ctx context.Context, nodeId string) bool {
	// Check Hardware
	cpuJanus, _ := calc.redis.GetNodeCPUPercent(ctx, nodeId, "janusStreaming")
	if (cpuJanus / EgressJanusCpuThreshold * 100.0) >= 100.0 {
		return true
	}

	// Check Slots (limite fisico)
	key := fmt.Sprintf("metrics:node:%s:janusStreaming", nodeId)
	val, _ := calc.redis.HGet(ctx, key, "janusTotalViewers")
	viewers, _ := strconv.Atoi(val)

	return viewers >= EgressMaxViewers
}
