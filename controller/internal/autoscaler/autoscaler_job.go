package autoscaler

import (
	"context"
	"fmt"
	"log"
	"math"
	"time"

	"controller/internal/domain"
	"controller/internal/redis"
)

const (
	AutoscalerPollInterval = 30 * time.Second

	// TIMERS
	ScalingCooldown = 60 * time.Second // Pausa dopo Docker Provisioning
	RecoverCooldown = 10 * time.Second // Pausa dopo Draining->Active

	// SOGLIE
	InjectionLowThreshold = 5.0 // Se carico medio < 5%, proviamo a spegnere
	ZombieLoadThreshold   = 1.0 // Se nodo < 1% CPU -> considerato "Zombie"
	EgressLowThreshold    = 20.0

	MinGlobalInjections = 1
	MinGlobalEgresses   = 1
)

type ProvisionerClient interface {
	ScaleUp(ctx context.Context, nodeType domain.NodeType) error
	DestroyNode(ctx context.Context, nodeId, nodeType string) error
}

type AutoscalerJob struct {
	redis             *redis.Client
	loadCalcInjection *InjectionLoadCalculator
	loadCalcEgress    *EgressLoadCalculator
	provisioner       ProvisionerClient
	stopChan          chan struct{}
	running           bool
}

func NewAutoscalerJob(redisClient *redis.Client, provisioner ProvisionerClient) *AutoscalerJob {
	return &AutoscalerJob{
		redis:             redisClient,
		loadCalcInjection: NewInjectionLoadCalculator(redisClient),
		loadCalcEgress:    NewEgressLoadCalculator(redisClient),
		provisioner:       provisioner,
		stopChan:          make(chan struct{}),
		running:           false,
	}
}

func (job *AutoscalerJob) Start(ctx context.Context) error {
	if job.running {
		return fmt.Errorf("autoscaler already running")
	}
	job.running = true
	log.Printf("[Autoscaler] Starting Orchestrator loop (interval: %v)", AutoscalerPollInterval)
	go job.orchestratorLoop(ctx)
	return nil
}

func (job *AutoscalerJob) Stop() error {
	if !job.running {
		return nil
	}
	close(job.stopChan)
	job.running = false
	return nil
}

// orchestratorLoop: Il loop principale che scansiona gli alberi
func (job *AutoscalerJob) orchestratorLoop(ctx context.Context) {
	ticker := time.NewTicker(AutoscalerPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			timeoutCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
			job.manageGlobalScaling(timeoutCtx)
			cancel()
		case <-job.stopChan:
			return
		case <-ctx.Done():
			return
		}
	}
}

// manageGlobalScaling gestisce lo scaling dell'intera mesh
func (job *AutoscalerJob) manageGlobalScaling(ctx context.Context) {
	// Gestione Ingresso (Injection Pool)
	if err := job.manageInjectionScaling(ctx); err != nil {
		log.Printf("[Autoscaler] Injection scaling error: %v", err)
	}

	// Gestione Uscita (Egress Pool)
	if err := job.manageEgressScaling(ctx); err != nil {
		log.Printf("[Autoscaler] Egress scaling error: %v", err)
	}
}

func (job *AutoscalerJob) manageInjectionScaling(ctx context.Context) error {
	// Lock Ingress
	lockKey := "lock:scaling:global:injection"

	if exists, _ := job.redis.Exists(ctx, lockKey); exists {
		return nil
	}

	// Recupera nodi
	injections, err := job.redis.GetNodePool(ctx, "injection")
	if err != nil || len(injections) == 0 {
		return nil
	}

	// Analisi Carico
	var activeNodes []string
	var drainingNodes []string
	var saturatedCount int
	var totalClusterLoad float64

	nodeSessions := make(map[string]int64)
	nodeLoads := make(map[string]float64)

	for _, nodeId := range injections {
		status, _ := job.redis.GetNodeStatus(ctx, nodeId)
		if status == "destroying" {
			continue
		}
		actualSessions, _ := job.redis.GetNodeSessionCount(ctx, nodeId)
		nodeSessions[nodeId] = actualSessions

		if status == "draining" {
			drainingNodes = append(drainingNodes, nodeId)
		} else {
			activeNodes = append(activeNodes, nodeId)

			load, err := job.loadCalcInjection.CalculateInjectionLoad(ctx, nodeId)
			if err != nil {
				log.Printf("[Autoscaler] Calc error for %s: %v", nodeId, err)
				continue
			}

			nodeLoads[nodeId] = load
			totalClusterLoad += load

			if load >= InjectionSaturatedThreshold {
				saturatedCount++
			}
		}
	}

	// Calcolo Media Carico Cluster
	avgLoad := 0.0
	if len(activeNodes) > 0 {
		avgLoad = totalClusterLoad / float64(len(activeNodes))
	}

	// Scaling up se tutti i nodi attivi sono saturi
	if len(activeNodes) > 0 && saturatedCount == len(activeNodes) {
		log.Printf("[Autoscaler] Injection Pool saturated (%d/%d).", saturatedCount, len(activeNodes))

		// Recupera un nodo Draining
		if len(drainingNodes) > 0 {
			candidate := job.findBestDrainingCandidate(drainingNodes, nodeSessions)
			if candidate != "" {
				log.Printf("[Autoscaler] Node %s from Draining to Active.", candidate)
				// Riattivalo
				job.redis.SetNodeStatus(ctx, candidate, "active")

				job.redis.SetNX(ctx, lockKey, "recovered", RecoverCooldown)
				return nil
			}
		}

		// Provisioning
		log.Printf("[Autoscaler-Ingress] No draining nodes. Scaling Up via Provisioner.")

		acquired, err := job.redis.SetNX(ctx, lockKey, "scaling_up", ScalingCooldown)
		if err != nil {
			return err
		}
		if !acquired {
			return nil
		}

		go func() {
			bgCtx := context.Background()
			if err := job.provisioner.ScaleUp(bgCtx, domain.NodeTypeInjection); err != nil {
				log.Printf("[Autoscaler] ScaleUp Failed: %v", err)
				job.redis.Del(bgCtx, lockKey) // Rilascia lock in caso di errore
			}
		}()
	}

	// TODO: Scaling down

	// Pulizia Draining
	for _, drainingId := range drainingNodes {
		if nodeSessions[drainingId] == 0 {
			log.Printf("[Autoscaler] Draining node %s is empty. Destroying.", drainingId)
			job.provisioner.DestroyNode(ctx, drainingId, "injection")
		}
	}

	// Scaling down
	// Condizioni:
	//  Carico medio basso (< 5%)
	//  Numero nodi Attivi > Minimo Template

	if avgLoad < InjectionLowThreshold && len(activeNodes) > MinGlobalInjections {
		log.Printf("[Autoscaler] Low Load (%.2f%%). Active: %d > Min: %d. Seeking victim.", avgLoad, len(activeNodes), MinGlobalInjections)

		victim := job.findBestScaleDownVictim(activeNodes, nodeSessions, nodeLoads)
		if victim != "" {
			// forse race condition....
			if nodeSessions[victim] == 0 {
				// Vuoto -> Kill subito.
				log.Printf("[Autoscaler] Immediate Kill for empty node: %s", victim)
				job.provisioner.DestroyNode(ctx, victim, "injection")
			} else {
				// Pieno -> Draining.
				log.Printf("[Autoscaler] Setting node %s to DRAINING (Sessions: %d)", victim, nodeSessions[victim])
				job.redis.SetNodeStatus(ctx, victim, "draining")
			}
		}
	}
	return nil
}

func (job *AutoscalerJob) manageEgressScaling(ctx context.Context) error {
	// Lock per l'uscita
	lockKey := "lock:scaling:global:egress"

	if exists, _ := job.redis.Exists(ctx, lockKey); exists {
		return nil
	}

	// Recupera i nodi dal pool
	egressNodeIds, err := job.redis.GetNodePool(ctx, "egress")
	if err != nil || len(egressNodeIds) == 0 {
		return nil
	}

	var activeNodes []string
	var drainingNodes []string
	var saturatedCount int
	var totalClusterLoad float64

	nodeViewers := make(map[string]int64)
	nodeLoads := make(map[string]float64)

	for _, nodeId := range egressNodeIds {
		status, _ := job.redis.GetNodeStatus(ctx, nodeId)
		if status == "destroying" {
			continue
		}

		// Recuperiamo i viewer totali dal MetricsCollector
		actualViewers, _ := job.redis.GetNodeTotalViewers(ctx, nodeId)
		nodeViewers[nodeId] = int64(actualViewers)

		if status == "draining" {
			drainingNodes = append(drainingNodes, nodeId)
		} else {
			activeNodes = append(activeNodes, nodeId)

			// Calcolo carico composto (CPU + Slot)
			load, err := job.loadCalcEgress.CalculateEgressLoad(ctx, nodeId)
			if err != nil {
				log.Printf("[Autoscaler-Egress] Calc error for %s: %v", nodeId, err)
				continue
			}

			nodeLoads[nodeId] = load
			totalClusterLoad += load

			if load >= EgressSaturatedThreshold {
				saturatedCount++
			}
		}
	}

	avgLoad := 0.0
	if len(activeNodes) > 0 {
		avgLoad = totalClusterLoad / float64(len(activeNodes))
	}

	// Scale up
	if len(activeNodes) > 0 && saturatedCount == len(activeNodes) {
		log.Printf("[Autoscaler] Egress Pool saturated (%d/%d). Scaling Up.", saturatedCount, len(activeNodes))

		// Se abbiamo un nodo in Draining, proviamo a riattivarlo prima di crearne uno nuovo
		if len(drainingNodes) > 0 {
			candidate := job.findBestDrainingCandidate(drainingNodes, nodeViewers)
			if candidate != "" {
				log.Printf("[Autoscaler-Egress] Recovering node %s from Draining", candidate)
				job.redis.SetNodeStatus(ctx, candidate, "active")
				job.redis.SetNX(ctx, lockKey, "recovered", RecoverCooldown)
				return nil
			}
		}

		// Provisioning di un nuovo nodo
		log.Printf("[Autoscaler-Egress] Scaling Up via Provisioner.")
		acquired, err := job.redis.SetNX(ctx, lockKey, "scaling_up", ScalingCooldown)
		if err != nil || !acquired {
			return err
		}

		go func() {
			bgCtx := context.Background()
			if err := job.provisioner.ScaleUp(bgCtx, domain.NodeTypeEgress); err != nil {
				log.Printf("[Autoscaler-Egress] ScaleUp Failed: %v", err)
				job.redis.Del(bgCtx, lockKey)
			}
		}()
		return nil
	}

	// Scale down
	if avgLoad < EgressLowThreshold && len(activeNodes) > MinGlobalEgresses {
		victim := job.findBestScaleDownVictim(activeNodes, nodeViewers, nodeLoads)
		// stesso problema. di riga 220
		if victim != "" {
			if nodeViewers[victim] == 0 {
				job.provisioner.DestroyNode(ctx, victim, "egress")
			} else {
				job.redis.SetNodeStatus(ctx, victim, "draining")
			}
		}
	}

	// Cleanup Draining
	for _, drainingId := range drainingNodes {
		if nodeViewers[drainingId] == 0 {
			job.provisioner.DestroyNode(ctx, drainingId, "egress")
		}
	}

	return nil
}

// findBestDrainingCandidate seleziona il nodo con più carico
func (job *AutoscalerJob) findBestDrainingCandidate(candidates []string, scores map[string]int64) string {
	best := ""
	var maxScore int64 = -1
	for _, id := range candidates {
		if s := scores[id]; s > maxScore {
			maxScore = s
			best = id
		}
	}
	return best
}

// findBestScaleDownVictim seleziona il nodo con meno carico o "zombie" per spegnerlo
func (job *AutoscalerJob) findBestScaleDownVictim(candidates []string, scores map[string]int64, loads map[string]float64) string {
	best := ""
	var minScore float64 = math.MaxFloat64
	for _, id := range candidates {
		score := float64(scores[id])
		// Penalità se il nodo è quasi inutilizzato (Zombie)
		if loads[id] < ZombieLoadThreshold {
			score -= 1000000
		}
		if score < minScore {
			minScore = score
			best = id
		}
	}
	return best
}
