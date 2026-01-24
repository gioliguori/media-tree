package autoscaler

import (
	"context"
	"fmt"
	"log"
	"time"

	"controller/internal/redis"
)

const (
	AutoscalerPollInterval = 30 * time.Second

	// TIMERS
	ScalingCooldown = 60 * time.Second // Pausa dopo Docker Provisioning
	RecoverCooldown = 10 * time.Second // Pausa dopo Draining->Active
)

type ProvisionerClient interface {
	ScaleUpInjection(ctx context.Context, treeId string) error
	// DestroyNode servira' in futuro per lo scaling down
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
			timeoutCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
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

	for _, nodeID := range injections {
		status, _ := job.redis.GetNodeStatus(ctx, treeID, nodeID)

		if status == "draining" {
			drainingNodes = append(drainingNodes, nodeID)
		} else {
			activeNodes = append(activeNodes, nodeID)

			load, err := job.loadCalc.CalculateInjectionLoad(ctx, treeID, nodeID)
			if err != nil {
				log.Printf("[Autoscaler] Calc error for %s: %v", nodeID, err)
				continue
			}
			if load >= InjectionSaturatedThreshold {
				saturatedCount++
			}
		}
	}

	// Scaling up se tutti i nodi attivi sono saturi
	if len(activeNodes) > 0 && saturatedCount == len(activeNodes) {
		log.Printf("[Autoscaler] Tree: %s saturated (%d/%d active nodes).",
			treeID, saturatedCount, len(activeNodes))

		// Recupera un nodo Draining
		if len(drainingNodes) > 0 {
			candidate := job.findBestDrainingCandidate(ctx, treeID, drainingNodes)
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

	return nil
}

// findBestDrainingCandidate: Trova il nodo draining con più sessioni
func (job *AutoscalerJob) findBestDrainingCandidate(ctx context.Context, treeID string, candidates []string) string {
	best := ""
	var maxSess int64 = -1
	for _, id := range candidates {
		// Se GetNodeSessionCount fallisce, assumiamo 0
		c, err := job.redis.GetNodeSessionCount(ctx, treeID, id)
		if err != nil {
			c = 0
		}

		if c > maxSess {
			maxSess = c
			best = id
		}
	}
	return best
}
