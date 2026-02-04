package autoscaler

import (
	"context"
	"fmt"
	"math"

	"controller/internal/redis"
)

const (
	InjectionCPULimitPercent    = 80.0
	JanusCPULimitPercent        = 80.0
	InjectionSaturatedThreshold = 10.0 // 10 per test (80)
)

type InjectionLoadCalculator struct {
	redis     *redis.Client
	relayCalc *RelayRootLoadCalculator
}

func NewInjectionLoadCalculator(redisClient *redis.Client) *InjectionLoadCalculator {
	return &InjectionLoadCalculator{
		redis:     redisClient,
		relayCalc: NewRelayRootLoadCalculator(redisClient),
	}
}

// CalculateInjectionLoad calcola il carico composto
// Injection Load = MAX(CPU Nodejs, CPU Janus, Carico Relay Figlio)
func (calc *InjectionLoadCalculator) CalculateInjectionLoad(
	ctx context.Context,
	injectionId string,
) (float64, error) {

	cpuInjection, err := calc.redis.GetNodeCPUPercent(ctx, injectionId, "nodejs")
	if err != nil {
		return 100.0, fmt.Errorf("failed to get injection CPU: %w", err)
	}

	cpuJanus, err := calc.redis.GetNodeCPUPercent(ctx, injectionId, "janus")
	if err != nil {
		return 100.0, fmt.Errorf("failed to get janus CPU: %w", err)
	}

	relayRootId, err := calc.getRelayRootForInjection(ctx, injectionId)
	if err != nil {
		return 100.0, fmt.Errorf("failed to find relay-root: %w", err)
	}

	relayRootLoad, err := calc.relayCalc.CalculateRelayRootLoad(ctx, relayRootId)
	if err != nil {
		return 100.0, fmt.Errorf("failed to calculate relay-root load: %w", err)
	}

	loadInjection := (cpuInjection / InjectionCPULimitPercent) * 100.0
	loadJanus := (cpuJanus / JanusCPULimitPercent) * 100.0

	maxLoad := math.Max(
		math.Max(loadInjection, loadJanus),
		relayRootLoad,
	)

	return maxLoad, nil
}

// getRelayRootForInjection recupera l'Id del nodo figlio diretto (Relay Root)
func (calc *InjectionLoadCalculator) getRelayRootForInjection(
	ctx context.Context,
	injectionId string,
) (string, error) {
	children, err := calc.redis.GetNodeChildren(ctx, injectionId)
	if err != nil {
		return "", err
	}
	if len(children) == 0 {
		return "", fmt.Errorf("no relay-root found")
	}
	return children[0], nil
}
