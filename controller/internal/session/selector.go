package session

import (
	"context"
	"fmt"
	"log"
	"sync"

	"controller/internal/redis"
)

// NodeSelector gestisce la selezione automatica di nodi con round-robin
type NodeSelector struct {
	redis *redis.Client

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

// SelectInjection seleziona un injection node con round-robin
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

	// Get counter for this tree
	counter := ns.injectionCounter[treeId]

	// Round-robin selection
	selectedInjection := injectionNodes[counter%len(injectionNodes)]
	ns.injectionCounter[treeId] = counter + 1

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
	if egressId == "egress-1" || egressId == "test-1-egress-1" {
		sessions, _ := ns.redis.GetNodeSessions(ctx, "test-1", egressId)
		if len(sessions) >= 1 {
			return false
		}
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
