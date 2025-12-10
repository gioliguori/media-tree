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
	injectionCounter map[string]int // treeId -> counter
	egressCounter    map[string]int // treeId -> counter
}

// NewNodeSelector crea nuovo NodeSelector
func NewNodeSelector(redisClient *redis.Client) *NodeSelector {
	return &NodeSelector{
		redis:            redisClient,
		injectionCounter: make(map[string]int),
		egressCounter:    make(map[string]int),
	}
}

// SelectTree seleziona un tree con round-robin
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

	log.Printf("[NodeSelector] Selected tree: %s (counter=%d, total_trees=%d)", selectedTree, ns.treeCounter, len(trees))
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

	log.Printf("[NodeSelector] Selected injection: %s (tree=%s, counter=%d, total_injections=%d)",
		selectedInjection, treeId, counter+1, len(injectionNodes))
	return selectedInjection, nil
}

// SelectEgress seleziona N egress nodes con round-robin
func (ns *NodeSelector) SelectEgress(ctx context.Context, treeId string, count int) ([]string, error) {
	ns.mu.Lock()
	defer ns.mu.Unlock()

	// Get egress nodes for tree
	egressNodes, err := ns.redis.GetEgressNodes(ctx, treeId)
	if err != nil {
		return nil, fmt.Errorf("failed to get egress nodes: %w", err)
	}

	if len(egressNodes) == 0 {
		return nil, fmt.Errorf("no egress nodes available in tree %s", treeId)
	}

	// Se richiesti più egress di quelli disponibili, usa tutti quelli disponibili
	if count > len(egressNodes) {
		log.Printf("[WARN] Requested %d egress but only %d available in tree %s, using all", count, len(egressNodes), treeId)
		count = len(egressNodes)
	}

	// Get counter for this tree
	counter := ns.egressCounter[treeId]

	// Round-robin selection
	selectedEgress := make([]string, 0, count)
	for i := 0; i < count; i++ {
		egress := egressNodes[(counter+i)%len(egressNodes)]
		selectedEgress = append(selectedEgress, egress)
	}

	// Update counter
	ns.egressCounter[treeId] = counter + count

	log.Printf("[NodeSelector] Selected %d egress: %v (tree=%s, counter=%d, total_egress=%d)",
		count, selectedEgress, treeId, counter+count, len(egressNodes))
	return selectedEgress, nil
}

// ResetCounters resetta tutti i counter (per testing)
func (ns *NodeSelector) ResetCounters() {
	ns.mu.Lock()
	defer ns.mu.Unlock()

	ns.treeCounter = 0
	ns.injectionCounter = make(map[string]int)
	ns.egressCounter = make(map[string]int)

	log.Printf("[NodeSelector] All counters reset")
}
