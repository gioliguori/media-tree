package autoscaler

import (
	"context"
	"fmt"
	"log"
	"math"
	"strconv"
	"time"

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
)

type ProvisionerClient interface {
	ScaleUpInjection(ctx context.Context, treeId string) error
	DestroyNode(ctx context.Context, treeId, nodeId, nodeType string) error
}

type AutoscalerJob struct {
	redis       *redis.Client
	loadCalc    *InjectionLoadCalculator
	provisioner ProvisionerClient
	stopChan    chan struct{}
	running     bool
}

func NewAutoscalerJob(redisClient *redis.Client, provisioner ProvisionerClient) *AutoscalerJob {
	return &AutoscalerJob{
		redis:       redisClient,
		loadCalc:    NewInjectionLoadCalculator(redisClient),
		provisioner: provisioner,
		stopChan:    make(chan struct{}),
		running:     false,
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
			job.checkAllTrees(timeoutCtx)
			cancel()
		case <-job.stopChan:
			return
		case <-ctx.Done():
			return
		}
	}
}

func (job *AutoscalerJob) checkAllTrees(ctx context.Context) {
	trees, err := job.redis.GetAllTrees(ctx)
	if err != nil {
		log.Printf("[Autoscaler] Failed to get trees: %v", err)
		return
	}

	for _, treeID := range trees {
		// Chiama l'orchestratore per il singolo albero
		if err := job.manageTreeLifecycle(ctx, treeID); err != nil {
			log.Printf("[Autoscaler] Error managing tree %s: %v", treeID, err)
		}
	}
}

func (job *AutoscalerJob) manageTreeLifecycle(ctx context.Context, treeID string) error {

	// Gestione ingresso
	if err := job.manageIngressScaling(ctx, treeID); err != nil {
		return fmt.Errorf("ingress scaling error: %w", err)
	}

	// Gestione uscita (Relay/Egress)

	return nil
}

func (job *AutoscalerJob) manageIngressScaling(ctx context.Context, treeID string) error {
	// Lock Ingress
	lockKey := fmt.Sprintf("lock:scaling:tree:%s:ingress", treeID)

	if exists, _ := job.redis.Exists(ctx, lockKey); exists {
		return nil
	}

	// Recupera nodi
	injections, err := job.redis.GetInjectionNodes(ctx, treeID)
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

	for _, nodeID := range injections {
		status, _ := job.redis.GetNodeStatus(ctx, treeID, nodeID)
		if status == "destroying" {
			continue
		}
		actualSessions, _ := job.redis.GetNodeSessionCount(ctx, treeID, nodeID)
		nodeSessions[nodeID] = actualSessions

		if status == "draining" {
			drainingNodes = append(drainingNodes, nodeID)
		} else {
			activeNodes = append(activeNodes, nodeID)

			load, err := job.loadCalc.CalculateInjectionLoad(ctx, treeID, nodeID)
			if err != nil {
				log.Printf("[Autoscaler] Calc error for %s: %v", nodeID, err)
				continue
			}

			nodeLoads[nodeID] = load
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
		log.Printf("[Autoscaler] Tree: %s saturated (%d/%d active nodes).",
			treeID, saturatedCount, len(activeNodes))

		// Recupera un nodo Draining
		if len(drainingNodes) > 0 {
			candidate := job.findBestDrainingCandidate(drainingNodes, nodeSessions)
			if candidate != "" {
				log.Printf("[Autoscaler] Node %s from Draining to Active.", candidate)
				// Riattivalo
				if err := job.redis.SetNodeStatus(ctx, treeID, candidate, "active"); err != nil {
					return fmt.Errorf("failed to recover node: %w", err)
				}

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
			if err := job.provisioner.ScaleUpInjection(bgCtx, treeID); err != nil {
				log.Printf("[Autoscaler] ScaleUp Failed: %v", err)
				job.redis.Del(bgCtx, lockKey) // Rilascia lock in caso di errore
			}
		}()
	}

	// TODO: Scaling down

	// Pulizia Draining
	for _, drainingId := range drainingNodes {
		if nodeSessions[drainingId] == 0 {
			log.Printf("[Autoscaler] Draining node %s is empty. DESTROYING.", drainingId)
			job.provisioner.DestroyNode(ctx, treeID, drainingId, "injection")
		}
	}

	// Scaling down
	// Condizioni:
	//  Carico medio basso (< 5%)
	//  Numero nodi Attivi > Minimo Template

	if avgLoad < InjectionLowThreshold {

		// Leggi il minimo dal Redis Metadata (salvato alla creazione)
		minNodes := job.getMinNodesFromMetadata(ctx, treeID)

		if len(activeNodes) > minNodes {
			log.Printf("[Autoscaler] Low Load (%.2f%%). Active: %d > Min: %d. Seeking victim.", avgLoad, len(activeNodes), minNodes)

			victim := job.findBestScaleDownVictim(activeNodes, nodeSessions, nodeLoads)
			if victim != "" {
				if nodeSessions[victim] == 0 {
					// Vuoto -> Kill subito.
					log.Printf("[Autoscaler] Immediate Kill for empty node: %s", victim)
					job.provisioner.DestroyNode(ctx, treeID, victim, "injection")
				} else {
					// Pieno -> Draining.
					log.Printf("[Autoscaler] Setting node %s to DRAINING (Sessions: %d)", victim, nodeSessions[victim])
					job.redis.SetNodeStatus(ctx, treeID, victim, "draining")
				}
			}
		}
	}

	return nil
}

// findBestDrainingCandidate: Sceglie chi ha più sessioni
func (job *AutoscalerJob) findBestDrainingCandidate(candidates []string, sessions map[string]int64) string {
	best := ""
	var maxSess int64 = -1
	for _, id := range candidates {
		c := sessions[id]
		if c > maxSess {
			maxSess = c
			best = id
		}
	}
	return best
}

// findBestScaleDownVictim: Sceglie chi ha meno sessioni o zombie
func (job *AutoscalerJob) findBestScaleDownVictim(candidates []string, sessions map[string]int64, loads map[string]float64) string {
	best := ""
	var minScore float64 = math.MaxFloat64

	for _, id := range candidates {
		sessCount := float64(sessions[id])
		load := loads[id]

		score := sessCount

		// Se il carico è quasi zero, sottraiamo un valore enorme allo score.
		if load < ZombieLoadThreshold {
			score -= 1_000_000
		}

		if score < minScore {
			minScore = score
			best = id
		}
	}
	return best
}

// getMinNodesFromMetadata legge il valore salvato in Redis da Manager
func (job *AutoscalerJob) getMinNodesFromMetadata(ctx context.Context, treeID string) int {
	// Legge hash field "minInjectionNodes"
	valStr, err := job.redis.HGet(ctx, fmt.Sprintf("tree:%s:metadata", treeID), "minInjectionNodes")
	if err != nil {
		return 1 // Default se non trovato
	}

	val, err := strconv.Atoi(valStr)
	if err != nil || val < 1 {
		return 1
	}
	return val
}
