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

func (ns *NodeSelector) SelectInjection(ctx context.Context, sessionId string) (string, error) {

	// Chiediamo un injection con capacità residua
	nodeId, err := ns.redis.AcquireInjectionSlot(ctx, sessionId)
	if err != nil {
		return "", fmt.Errorf("failed to acquire injection slot: %w", err)
	}

	if nodeId == "FULL" {
		log.Printf("[NodeSelector] All injection nodes are at maximum capacity")
		return "", ErrScalingNeeded
	}

	log.Printf("[NodeSelector] Session %s assigned to injection %s", sessionId, nodeId)
	return nodeId, nil
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

// Relay

// SelectRelayForViewer implementa Hole-Filling e Deepening per la mesh.
// Ritorna: relayId, isNewRelayAdded, error
func (ns *NodeSelector) SelectRelayForViewer(ctx context.Context, sessionId string) (string, bool, error) {

	// Hole fitting
	// Lo script scorre la catena
	relayId, err := ns.redis.AcquireEdgeSlot(ctx, sessionId)
	if err != nil {
		return "", false, fmt.Errorf("lua acquisition failed: %w", err)
	}

	if relayId != "FULL" {
		log.Printf("[NodeSelector] Hole-Filling: Slot acquired on existing relay %s", relayId)
		return relayId, false, nil
	}

	//  Allungamento catena
	log.Printf("[NodeSelector] Chain full for session %s. Attempting deepening...", sessionId)

	currentChain, err := ns.redis.GetSessionChain(ctx, sessionId)
	if err != nil {
		return "", false, err
	}

	// Cerca nel pool un relay standalone con almeno 2 slot liberi (load <= 18)
	// Esclude i nodi già presenti nella catena per evitare cicli
	newRelayId, err := ns.redis.FindBestRelayForDeepening(ctx, currentChain)
	if err != nil {
		return "", false, fmt.Errorf("deepening failed: %w", err)
	}

	// Aggiunge il nuovo relay alla catena su Redis (+2 slot: 1 Deep Reserve + 1 Edge)
	if err := ns.redis.AddRelayToChain(ctx, sessionId, newRelayId); err != nil {
		return "", false, fmt.Errorf("failed to add relay to chain: %w", err)
	}

	log.Printf("[NodeSelector] Deepening: Added new relay %s to chain", newRelayId)
	return newRelayId, true, nil
}
