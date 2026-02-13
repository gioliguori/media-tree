package session

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
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
	loadCalcRelay     *autoscaler.RelayLoadCalculator
	loadCalcEgress    *autoscaler.EgressLoadCalculator

	mu sync.Mutex
}

// NewNodeSelector crea nuovo NodeSelector
func NewNodeSelector(redisClient *redis.Client) *NodeSelector {
	return &NodeSelector{
		redis:             redisClient,
		loadCalcInjection: autoscaler.NewInjectionLoadCalculator(redisClient),
		loadCalcRelay:     autoscaler.NewRelayLoadCalculator(redisClient),
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

	status, _ := ns.redis.GetNodeStatus(ctx, nodeId)
	if status != "active" {
		ns.redis.ReleaseInjectionSlot(ctx, nodeId, sessionId)
		return "", ErrScalingNeeded
	}

	// Se il nodo scelto è saturo lo scartiamo
	if !ns.loadCalcInjection.IsNodeHealthy(ctx, nodeId) {
		ns.redis.ReleaseInjectionSlot(ctx, nodeId, sessionId)
		return "", ErrScalingNeeded
	}

	log.Printf("[NodeSelector] Session %s assigned to injection %s", sessionId, nodeId)
	return nodeId, nil
}

// SelectBestEgressForSession seleziona egress per viewer Fill-First
func (ns *NodeSelector) SelectBestEgressForSession(ctx context.Context, sessionId string) (string, error) {
	ns.mu.Lock()
	defer ns.mu.Unlock()

	// Cerca tra gli Egress che hanno già la sessione
	existing, _ := ns.redis.FindEgressServingSession(ctx, sessionId)
	bestExisting := ns.findMostLoadedAvailable(ctx, existing)
	if bestExisting != "" {
		log.Printf("[NodeSelector] Reusing egress %s (Fill-First)", bestExisting)
		return bestExisting, nil
	}

	// Se nessuno esistente ha spazio, cerca in tutto il pool
	pool, err := ns.redis.GetNodePool(ctx, "egress")
	if err != nil || len(pool) == 0 {
		return "", ErrScalingNeeded
	}

	bestNew := ns.findMostLoadedAvailable(ctx, pool)
	if bestNew != "" {
		log.Printf("[NodeSelector] Selected new egress %s from pool (Fill-First)", bestNew)
		return bestNew, nil
	}

	return "", ErrScalingNeeded
}

// findMostLoadedAvailable seleziona il nodo più carico (ma non saturo)
func (ns *NodeSelector) findMostLoadedAvailable(ctx context.Context, nodeIds []string) string {
	bestCandidate := ""
	maxViewers := -1

	for _, id := range nodeIds {

		status, _ := ns.redis.GetNodeStatus(ctx, id)
		if status != "active" {
			continue
		}

		// Se il nodo è saturo lo saltiamo
		if ns.loadCalcEgress.IsNodeSaturated(ctx, id) {
			continue
		}

		// Leggiamo il numero attuale di viewer
		key := fmt.Sprintf("metrics:node:%s:janusStreaming", id)
		val, _ := ns.redis.HGet(ctx, key, "janusTotalViewers")
		viewers, _ := strconv.Atoi(val)

		// Se questo nodo è più carico di quello trovato finora, diventa il nuovo candidato
		if viewers > maxViewers {
			maxViewers = viewers
			bestCandidate = id
		}
	}
	return bestCandidate
}

// CanAcceptViewer usato per decidere se riusare un Egress esistente
func (ns *NodeSelector) CanAcceptViewer(ctx context.Context, nodeId string) bool {
	status, _ := ns.redis.GetNodeStatus(ctx, nodeId)
	if status != "active" {
		return false
	}
	return !ns.loadCalcEgress.IsNodeSaturated(ctx, nodeId)
}

// SelectRelayForViewer implementa Hole-Filling e Deepening per la mesh
func (ns *NodeSelector) SelectRelayForViewer(ctx context.Context, sessionId string) (string, bool, error) {

	// Hole fitting
	// Lo script scorre la catena
	relayId, err := ns.redis.AcquireEdgeSlot(ctx, sessionId)
	if err == nil && relayId != "FULL" {

		status, _ := ns.redis.GetNodeStatus(ctx, relayId)
		hwLoad, _ := ns.loadCalcRelay.CalculateRelayLoad(ctx, relayId)

		if status == "active" && hwLoad < 100.0 {
			log.Printf("[NodeSelector] Hole-Filling: Slot acquired on %s", relayId)
			return relayId, false, nil
		}
		// Rollback dello slot se hardware saturo
		ns.redis.ReleaseEdgeSlot(ctx, sessionId, relayId)
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
	status, _ := ns.redis.GetNodeStatus(ctx, newRelayId)
	hwLoad, _ := ns.loadCalcRelay.CalculateRelayLoad(ctx, newRelayId)

	if status != "active" || hwLoad >= 100.0 {
		return "", false, ErrScalingNeeded
	}

	// Aggiunge il nuovo relay alla catena su Redis (+2 slot: 1 Deep Reserve + 1 Edge)
	if err := ns.redis.AddRelayToChain(ctx, sessionId, newRelayId); err != nil {
		return "", false, fmt.Errorf("failed to add relay to chain: %w", err)
	}

	log.Printf("[NodeSelector] Deepening: Added new relay %s to chain", newRelayId)
	return newRelayId, true, nil
}
