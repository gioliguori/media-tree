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
	redis    *redis.Client
	loadCalc *autoscaler.InjectionLoadCalculator

	// Round-robin counters
	mu               sync.Mutex
	treeCounter      int
	injectionCounter map[string]int         // treeId -> counter
	egressCounter    map[string]map[int]int // treeId -> layer
}

// NewNodeSelector crea nuovo NodeSelector
func NewNodeSelector(redisClient *redis.Client) *NodeSelector {
	return &NodeSelector{
		redis:            redisClient,
		loadCalc:         autoscaler.NewInjectionLoadCalculator(redisClient),
		injectionCounter: make(map[string]int),
		egressCounter:    make(map[string]map[int]int),
	}
}

// SelectTree seleziona un tree round-robin
func (ns *NodeSelector) SelectTree(ctx context.Context) (string, error) {
	ns.mu.Lock()
	defer ns.mu.Unlock()

	// Get all active trees
	trees, err := ns.redis.GetAllTrees(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get trees: %w", err)
	}

	if len(trees) == 0 {
		return "", fmt.Errorf("no trees available")
	}

	// Round-robin selection
	selectedTree := trees[ns.treeCounter%len(trees)]
	ns.treeCounter++

	log.Printf("[NodeSelector] Selected tree: %s", selectedTree)
	return selectedTree, nil
}

// SelectInjection seleziona injection (least loadedd)
func (ns *NodeSelector) SelectInjection(ctx context.Context, treeId string) (string, error) {
	ns.mu.Lock()
	defer ns.mu.Unlock()

	// Get injection nodes for tree
	injectionNodes, err := ns.redis.GetInjectionNodes(ctx, treeId)
	if err != nil {
		return "", fmt.Errorf("failed to get injection nodes: %w", err)
	}

	if len(injectionNodes) == 0 {
		return "", fmt.Errorf("no injection nodes available in tree %s", treeId)
	}

	// solo injection con status=active (skip draining)
	activeInjections := ns.filterActiveInjections(ctx, treeId, injectionNodes)

	if len(activeInjections) == 0 {
		log.Printf("[NodeSelector] No active injections available in tree %s", treeId)
		return "", ErrNoInjectionAvailable
	}

	// SELEZIONE:  least-loaded CPU
	selectedInjection, err := ns.selectLeastLoadedInjection(ctx, treeId, activeInjections)
	if err != nil {
		return "", err // Propaga ErrScalingNeeded o altri errori
	}
	return selectedInjection, nil
}

// filterActiveInjections filtra solo injection con status=active
func (ns *NodeSelector) filterActiveInjections(
	ctx context.Context,
	treeId string,
	injections []string,
) []string {
	active := make([]string, 0, len(injections))

	for _, injectionId := range injections {
		status, err := ns.redis.GetNodeStatus(ctx, treeId, injectionId)
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
	treeId string,
	injections []string,
) (string, error) {

	minLoad := 999.0
	selectedInjection := ""
	metricsAvailableCount := 0
	loadScores := make(map[string]float64)

	for _, injectionId := range injections {
		// Usa InjectionLoadCalculator
		loadScore, err := ns.loadCalc.CalculateInjectionLoad(ctx, treeId, injectionId)
		if err != nil {
			log.Printf("[WARN] Failed to get load for %s: %v (assuming saturated)", injectionId, err)
			continue
		}

		metricsAvailableCount++
		loadScores[injectionId] = loadScore

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
	log.Printf("[NodeSelector] Load scores: %v", loadScores)
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
func (ns *NodeSelector) SelectBestEgressForSession(
	ctx context.Context,
	treeId string,
	sessionId string,
) (string, error) {
	ns.mu.Lock()
	defer ns.mu.Unlock()

	// Check egress esistenti (riuso)
	// lo facciamo già in ProvisionViewer, commento per il mom
	// existingEgress, err := ns.redis.FindEgressServingSession(ctx, treeId, sessionId)
	// if err == nil && len(existingEgress) > 0 {
	// 	for _, egressId := range existingEgress {
	// 		if ns.CanAcceptViewer(ctx, egressId) {
	// 			log.Printf("[NodeSelector] Reusing egress %s (multicast)", egressId)
	// 			return egressId, nil
	// 		}
	// 	}
	// }
	// Breadth-first Selection (Layer by Layer)
	for layer := 1; layer <= 10; layer++ {
		egresses, err := ns.redis.GetNodesAtLayer(ctx, treeId, "egress", layer)
		if err != nil || len(egresses) == 0 {
			continue
		}

		if ns.egressCounter[treeId] == nil {
			ns.egressCounter[treeId] = make(map[int]int)
		}

		startOffset := ns.egressCounter[treeId][layer]
		count := len(egresses)

		for i := 0; i < count; i++ {
			idx := (startOffset + i) % count
			candidate := egresses[idx]

			if ns.CanAcceptViewer(ctx, candidate) {
				ns.egressCounter[treeId][layer] = idx + 1
				log.Printf("[NodeSelector] Selected egress %s at layer %d", candidate, layer)
				return candidate, nil
			}
		}
	}

	return "", fmt.Errorf("no egress available in tree %s - scaling needed", treeId)
}

func (ns *NodeSelector) CanAcceptViewer(ctx context.Context, egressId string) bool {
	// Simulazione TEST:
	//if egressId == "egress-1" || egressId == "test-1-egress-1" {
	//	sessions, _ := ns.redis.GetNodeSessions(ctx, "test-1", egressId)
	//	if len(sessions) >= 1 {
	//		return false
	//	}
	//}
	return true
}

// Helpers
func (ns *NodeSelector) ResetCounters() {
	ns.mu.Lock()
	defer ns.mu.Unlock()

	ns.treeCounter = 0
	ns.injectionCounter = make(map[string]int)
	ns.egressCounter = make(map[string]map[int]int)
}
