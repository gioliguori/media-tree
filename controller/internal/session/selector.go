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
	// Round-robin counters
	mu               sync.Mutex
	treeCounter      int
	injectionCounter map[string]int         // treeId -> counter
	egressCounter    map[string]map[int]int // treeId -> layer
}

// NewNodeSelector crea nuovo NodeSelector
func NewNodeSelector(redisClient *redis.Client) *NodeSelector {
	return &NodeSelector{
		redis:             redisClient,
		loadCalcInjection: autoscaler.NewInjectionLoadCalculator(redisClient),
		loadCalcEgress:    autoscaler.NewEgressLoadCalculator(redisClient),
		injectionCounter:  make(map[string]int),
		egressCounter:     make(map[string]map[int]int),
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

	// Selezione  least-loaded CPU
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

		loadScore, err := ns.loadCalcInjection.CalculateInjectionLoad(ctx, treeId, injectionId)
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
func (ns *NodeSelector) SelectBestEgressForSession(ctx context.Context, treeId string, sessionId string) (string, error) {
	ns.mu.Lock()
	defer ns.mu.Unlock()

	egressNodeIDs, err := ns.redis.GetEgressPool(ctx, treeId)
	if err != nil || len(egressNodeIDs) == 0 {
		return "", fmt.Errorf("no egress available in pool")
	}

	bestCandidate := ""
	maxLoad := -1.0

	// Cerca prima tra i nodi che hanno già la sessione
	// Tra questi, prendi quello più carico che può ancora accettare viewer
	existingEgresses, _ := ns.redis.FindEgressServingSession(ctx, treeId, sessionId)
	for _, nodeId := range existingEgresses {
		if ns.CanAcceptViewer(ctx, treeId, nodeId) {
			load, _ := ns.loadCalcEgress.CalculateEgressLoad(ctx, treeId, nodeId)
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
	for _, nodeId := range egressNodeIDs {
		if ns.CanAcceptViewer(ctx, treeId, nodeId) {
			load, _ := ns.loadCalcEgress.CalculateEgressLoad(ctx, treeId, nodeId)
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

func (ns *NodeSelector) CanAcceptViewer(ctx context.Context, treeId string, nodeId string) bool {
	// Chiediamo il carico attuale del nodo
	load, err := ns.loadCalcEgress.CalculateEgressLoad(ctx, treeId, nodeId)
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

// Helpers
func (ns *NodeSelector) ResetCounters() {
	ns.mu.Lock()
	defer ns.mu.Unlock()

	ns.treeCounter = 0
	ns.injectionCounter = make(map[string]int)
	ns.egressCounter = make(map[string]map[int]int)
}
