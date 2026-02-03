package session

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"

	"controller/internal/autoscaler"
	"controller/internal/redis"
)

// ErrNoInjectionAvailable viene ritornato quando non ci sono injection disponibili
var ErrNoInjectionAvailable = errors.New("no injection nodes available")
var ErrScalingNeeded = errors.New("all injection nodes saturated, scaling needed")

// NodeSelector gestisce la selezione automatica di nodi con round-robin
type NodeSelector struct {
	redis             *redis.Client
	loadCalcInjection *autoscaler.InjectionLoadCalculator
	loadCalcEgress    *autoscaler.EgressLoadCalculator

	mu               sync.Mutex
	injectionCounter int
	egressCounter    int
}

// NewNodeSelector crea nuovo NodeSelector
func NewNodeSelector(redisClient *redis.Client) *NodeSelector {
	return &NodeSelector{
		redis:             redisClient,
		loadCalcInjection: autoscaler.NewInjectionLoadCalculator(redisClient),
		loadCalcEgress:    autoscaler.NewEgressLoadCalculator(redisClient),
	}
}

// SelectInjection seleziona injection (least loadedd)
func (ns *NodeSelector) SelectInjection(ctx context.Context) (string, error) {
	ns.mu.Lock()
	defer ns.mu.Unlock()

	// Recupera tutti i nodi injection dal pool globale
	injectionNodes, err := ns.redis.GetNodePool(ctx, "injection")
	if err != nil {
		return "", fmt.Errorf("failed to get injection nodes: %w", err)
	}

	if len(injectionNodes) == 0 {
		return "", fmt.Errorf("no injection nodes available")
	}

	// solo injection con status=active (skip draining)
	activeInjections := ns.filterActiveInjections(ctx, injectionNodes)

	if len(activeInjections) == 0 {
		log.Printf("[NodeSelector] No active injections available")
		return "", ErrNoInjectionAvailable
	}

	// Selezione  least-loaded CPU
	selectedInjection, err := ns.selectLeastLoadedInjection(ctx, activeInjections)
	if err != nil {
		return "", err // Propaga ErrScalingNeeded o altri errori
	}
	return selectedInjection, nil
}

// filterActiveInjections filtra solo injection con status=active
func (ns *NodeSelector) filterActiveInjections(
	ctx context.Context,
	injections []string,
) []string {
	active := make([]string, 0, len(injections))

	for _, injectionId := range injections {
		status, err := ns.redis.GetNodeStatus(ctx, injectionId)
		if err != nil {
			log.Printf("[WARN] Failed to get status for %s:  %v (assuming inactive)", injectionId, err)
			status = "inactive" // Safe default
		}

		if status == "active" {
			active = append(active, injectionId)
		} else {
			log.Printf("[NodeSelector] Skipping %s (status=%s)", injectionId, status)
		}
	}

	return active
}

// selectLeastLoadedInjection seleziona injection con CPU minima
func (ns *NodeSelector) selectLeastLoadedInjection(
	ctx context.Context,
	injections []string,
) (string, error) {

	minLoad := 999.0
	selectedInjection := ""
	metricsAvailableCount := 0
	for _, injectionId := range injections {

		loadScore, err := ns.loadCalcInjection.CalculateInjectionLoad(ctx, injectionId)
		if err != nil {
			log.Printf("[WARN] Failed to get load for %s: %v (assuming saturated)", injectionId, err)
			continue
		}

		metricsAvailableCount++

		// Filtra solo injection sotto threshold
		if loadScore < autoscaler.InjectionSaturatedThreshold {
			if loadScore < minLoad {
				minLoad = loadScore
				selectedInjection = injectionId
			}
		} else {
			log.Printf("[NodeSelector] %s saturated (load=%.2f%% >= %.2f%%), skipping",
				injectionId, loadScore, autoscaler.InjectionSaturatedThreshold)
		}
	}

	// Fallback: nessuna metrica disponibile
	if metricsAvailableCount == 0 {
		log.Printf("[WARN] No metrics available, using first injection:  %s", injections[0])
		return injections[0], nil
	}
	// Se tutti saturi -> scaling needed
	if selectedInjection == "" {
		log.Printf("[NodeSelector] All injections saturated (threshold: %.2f%%), scaling needed",
			autoscaler.InjectionSaturatedThreshold)
		return "", ErrScalingNeeded
	}

	log.Printf("[NodeSelector] Selected %s (load %.2f%%)", selectedInjection, minLoad)

	return selectedInjection, nil
}

// SelectBestEgressForSession seleziona egress per viewer
func (ns *NodeSelector) SelectBestEgressForSession(ctx context.Context, sessionId string) (string, error) {
	ns.mu.Lock()
	defer ns.mu.Unlock()

	// Recupera pool egress globale
	egressNodeIds, err := ns.redis.GetNodePool(ctx, "egress")
	if err != nil || len(egressNodeIds) == 0 {
		return "", fmt.Errorf("no egress available in pool")
	}

	bestCandidate := ""
	maxLoad := -1.0

	// Cerca prima tra i nodi che hanno già la sessione
	// Tra questi, prendi quello più carico che può ancora accettare viewer
	existingEgresses, _ := ns.redis.FindEgressServingSession(ctx, sessionId)
	for _, nodeId := range existingEgresses {
		if ns.CanAcceptViewer(ctx, nodeId) {
			// Strategia Fill-First: tra quelli che hanno già lo stream, prendi il più carico
			load, _ := ns.loadCalcEgress.CalculateEgressLoad(ctx, nodeId)
			if load > maxLoad {
				maxLoad = load
				bestCandidate = nodeId
			}
		}
	}

	if bestCandidate != "" {
		log.Printf("[NodeSelector] Reusing egress %s (Fill-First Load: %.2f%%)", bestCandidate, maxLoad)
		return bestCandidate, nil
	}

	// Se nessun nodo esistente può ospitare il viewer, cerchiamo nel pool
	// Strategia Fill-First: prendi il più carico tra quelli non saturi
	for _, nodeId := range egressNodeIds {
		if ns.CanAcceptViewer(ctx, nodeId) {
			load, _ := ns.loadCalcEgress.CalculateEgressLoad(ctx, nodeId)
			if load > maxLoad {
				maxLoad = load
				bestCandidate = nodeId
			}
		}
	}

	if bestCandidate != "" {
		log.Printf("[NodeSelector] Selected egress %s (Fill-First New, Load: %.2f%%)", bestCandidate, maxLoad)
		return bestCandidate, nil
	}

	return "", fmt.Errorf("all egress nodes are saturated")
}

func (ns *NodeSelector) CanAcceptViewer(ctx context.Context, nodeId string) bool {
	// Chiediamo il carico attuale del nodo
	load, err := ns.loadCalcEgress.CalculateEgressLoad(ctx, nodeId)
	if err != nil {
		// assumiamo che il nodo sia disponibile
		log.Printf("[NodeSelector] Warning: could not calculate load for %s: %v", nodeId, err)
		return true
	}

	// Confrontiamo con la soglia definita nell'autoscaler
	if load >= autoscaler.EgressSaturatedThreshold {
		log.Printf("[NodeSelector] Node %s is saturated (Load: %.2f%%), rejecting viewer", nodeId, load)
		return false
	}
	return true
}

// ResetCounters pulisce lo stato del selettore
func (ns *NodeSelector) ResetCounters() {
	ns.mu.Lock()
	defer ns.mu.Unlock()
	ns.injectionCounter = 0
	ns.egressCounter = 0
}
