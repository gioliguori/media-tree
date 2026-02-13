package autoscaler

import (
	"context"
	"fmt"
	"math"

	"controller/internal/redis"
)

const (
	RelayCpuThreshold        = 80.0
	RelayMaxQueueThresholdMs = 200.0
)

// StandalonePoolReport fornisce i dati aggregati per decidere lo scaling
type StandalonePoolReport struct {
	TotalNodes        int
	SpareNodes        int     // Quanti hanno Score == 0
	NodesForDeepening int     // Quanti hanno spazio per un nuovo salto (almeno 2 slot liberi)
	AvgHardwareLoad   float64 // Media carico fisico (CPU/Code) del pool
}

type RelayLoadCalculator struct {
	redis *redis.Client
}

func NewRelayLoadCalculator(redisClient *redis.Client) *RelayLoadCalculator {
	return &RelayLoadCalculator{redis: redisClient}
}

// determina la salute hardware di un Relay
func (calc *RelayLoadCalculator) CalculateRelayLoad(ctx context.Context, relayId string) (float64, error) {

	// CPU del processo Nodejs/C
	cpuRelay, err := calc.redis.GetNodeCPUPercent(ctx, relayId, "nodejs")
	if err != nil {
		cpuRelay = 0
	}

	// Latenza Code GStreamer
	queueKey := fmt.Sprintf("metrics:node:%s:gstreamer", relayId)
	metrics, _ := calc.redis.HGetAll(ctx, queueKey)

	var queueAudio, queueVideo float64
	fmt.Sscanf(metrics["maxAudioQueueMs"], "%f", &queueAudio)
	fmt.Sscanf(metrics["maxVideoQueueMs"], "%f", &queueVideo)
	maxQueue := math.Max(queueAudio, queueVideo)

	loadCPU := (cpuRelay / RelayCpuThreshold) * 100.0
	loadQueue := (maxQueue / RelayMaxQueueThresholdMs) * 100.0

	// Il carico fisico è il peggiore dei due fattori
	return math.Max(loadCPU, loadQueue), nil
}

// GetStandalonePoolReport analizza il pool dei relay per decidere lo scaling
func (calc *RelayLoadCalculator) GetStandalonePoolReport(ctx context.Context) (*StandalonePoolReport, error) {

	// Prendiamo tutti i relay dal ZSET dei carichi
	relays, err := calc.redis.GetRedisClient().ZRangeWithScores(ctx, "pool:relay:load", 0, -1).Result()
	if err != nil {
		return nil, err
	}

	report := &StandalonePoolReport{}
	var totalHardwareLoad float64

	for _, r := range relays {
		nodeId := r.Member.(string)
		status, _ := calc.redis.GetNodeStatus(ctx, nodeId)
		if status != "active" {
			continue
		}
		score := r.Score

		// Recuperiamo il ruolo
		nodeInfo, err := calc.redis.GetNodeProvisioning(ctx, nodeId)
		if err != nil || nodeInfo.Role != "standalone" {
			continue
		}
		report.TotalNodes++

		// Analisi degli slot
		// Un nodo è 'Spare' se è completamente vuoto
		if score == 0 {
			report.SpareNodes++
		}

		// Un nodo è buono per il Deepening se ha almeno 2 slot liberi
		if (float64(nodeInfo.MaxSlots) - score) >= 2 {
			report.NodesForDeepening++
		}

		// Analisi hardware
		hwLoad, _ := calc.CalculateRelayLoad(ctx, nodeId)
		totalHardwareLoad += hwLoad

	}

	if report.TotalNodes > 0 {
		report.AvgHardwareLoad = totalHardwareLoad / float64(report.TotalNodes)
	}

	return report, nil
}
