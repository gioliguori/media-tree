package autoscaler

import (
	"context"
	"math"

	"controller/internal/redis"
)

const (
	InjectionCpuThreshold = 80.0
)

// InjectionPoolReport fornisce i dati aggregati per lo scaling
type InjectionPoolReport struct {
	TotalAvailableSlots int     // Somma degli slot liberi
	AvgHardwareLoad     float64 // Media carico della coppia Injection + Root
	TotalNodes          int
}

type InjectionLoadCalculator struct {
	redis     *redis.Client
	relayCalc *RelayLoadCalculator
}

func NewInjectionLoadCalculator(redisClient *redis.Client) *InjectionLoadCalculator {
	return &InjectionLoadCalculator{
		redis:     redisClient,
		relayCalc: NewRelayLoadCalculator(redisClient),
	}
}

// GetPoolReport analizza tutti i nodi injection nel sistema
func (calc *InjectionLoadCalculator) GetPoolReport(ctx context.Context) (*InjectionPoolReport, error) {
	// Recupera tutti i nodi dal pool injection
	nodeIds, err := calc.redis.GetNodePool(ctx, "injection")
	if err != nil {
		return nil, err
	}

	report := &InjectionPoolReport{
		TotalNodes: len(nodeIds),
	}
	var totalHardwareLoad float64

	for _, id := range nodeIds {

		status, _ := calc.redis.GetNodeStatus(ctx, id)
		if status != "active" {
			continue
		}
		report.TotalNodes++

		// Info Provisioning (MaxSlots)
		nodeInfo, err := calc.redis.GetNodeProvisioning(ctx, id)
		if err != nil {
			continue
		}

		// Carico Logico (UsedSlots)
		score, _ := calc.redis.ZScore(ctx, "pool:injection:load", id)
		usedSlots := int(score)

		// Carico Fisico della Coppia
		cpuNode, _ := calc.redis.GetNodeCPUPercent(ctx, id, "nodejs")
		cpuJanus, _ := calc.redis.GetNodeCPUPercent(ctx, id, "janusVideoroom")

		// Normalizziamo il carico
		injHardware := math.Max(cpuNode, cpuJanus) / InjectionCpuThreshold * 100.0

		// Salute del RelayRoot associato
		var rootHardware float64
		children, _ := calc.redis.GetNodeChildren(ctx, id)
		if len(children) > 0 {
			// Passiamo il primo figlio
			rootHardware, _ = calc.relayCalc.CalculateRelayLoad(ctx, children[0])
		}

		maxHardware := math.Max(injHardware, rootHardware)
		totalHardwareLoad += maxHardware

		//  Calcolo Slot Effettivi
		// Se la coppia è fisicamente satura (maxHardware >= 100), ignoriamo i suoi slot liberi
		if maxHardware < 100.0 {
			available := nodeInfo.MaxSlots - usedSlots
			if available > 0 {
				report.TotalAvailableSlots += available
			}
		}
	}

	if report.TotalNodes > 0 {
		report.AvgHardwareLoad = totalHardwareLoad / float64(report.TotalNodes)
	}

	return report, nil
}

func (calc *InjectionLoadCalculator) IsNodeHealthy(ctx context.Context, injectionId string) bool {

	cpuNode, _ := calc.redis.GetNodeCPUPercent(ctx, injectionId, "nodejs")
	cpuJanus, _ := calc.redis.GetNodeCPUPercent(ctx, injectionId, "janusVideoroom")
	injHardware := math.Max(cpuNode, cpuJanus) / InjectionCpuThreshold * 100.0

	var rootHardware float64
	children, _ := calc.redis.GetNodeChildren(ctx, injectionId)
	if len(children) > 0 {
		rootHardware, _ = calc.relayCalc.CalculateRelayLoad(ctx, children[0])
	}

	// Se il carico hardware della coppia è >= 100, il nodo non è sano per nuove sessioni
	return math.Max(injHardware, rootHardware) < 100.0
}
