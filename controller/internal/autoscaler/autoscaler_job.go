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
	AutoscalerPollInterval = 20 * time.Second
	ScalingCooldown        = 90 * time.Second // Tempo di attesa tra un'azione e l'altra
	NodeMinLifeTime        = 2 * time.Minute
	MinActiveInjections    = 1
	MinActiveRelays        = 1
	MinActiveEgresses      = 1
	MinFreeSlotsInjection  = 2
	MinFreeSlotsEgress     = 5
)

type ProvisionerClient interface {
	ScaleUp(ctx context.Context, nodeType domain.NodeType) error
	DestroyNode(ctx context.Context, nodeId, nodeType string) error
}

type AutoscalerJob struct {
	redis         *redis.Client
	injectionCalc *InjectionLoadCalculator
	relayCalc     *RelayLoadCalculator
	egressCalc    *EgressLoadCalculator
	provisioner   ProvisionerClient
	stopChan      chan struct{}
	running       bool
}

func NewAutoscalerJob(redisClient *redis.Client, provisioner ProvisionerClient) *AutoscalerJob {
	return &AutoscalerJob{
		redis:         redisClient,
		injectionCalc: NewInjectionLoadCalculator(redisClient),
		relayCalc:     NewRelayLoadCalculator(redisClient),
		egressCalc:    NewEgressLoadCalculator(redisClient),
		provisioner:   provisioner,
		stopChan:      make(chan struct{}),
	}
}

func (job *AutoscalerJob) Start(ctx context.Context) error {
	if job.running {
		return fmt.Errorf("autoscaler already running")
	}
	job.running = true
	log.Printf("[Autoscaler] Starting loop (Interval: %v)", AutoscalerPollInterval)
	go job.orchestratorLoop(ctx)
	return nil
}

func (job *AutoscalerJob) Stop() {
	if job.running {
		close(job.stopChan)
		job.running = false
	}
}

func (job *AutoscalerJob) orchestratorLoop(ctx context.Context) {
	ticker := time.NewTicker(AutoscalerPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			tickCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
			job.runTick(tickCtx)
			cancel()
		case <-job.stopChan:
			return
		case <-ctx.Done():
			return
		}
	}
}

func (job *AutoscalerJob) runTick(ctx context.Context) {

	return

	job.manageInjectionPool(ctx)

	job.manageRelayPool(ctx)

	job.manageEgressPool(ctx)

	job.cleanupDrainingNodes(ctx)
}

// Injection
func (job *AutoscalerJob) manageInjectionPool(ctx context.Context) {
	report, err := job.injectionCalc.GetPoolReport(ctx)
	if err != nil {
		return
	}

	// Regola: Scale up se posti totali < MinFreeSlotsInjection o hardware sopra soglia
	if report.TotalNodes < MinActiveInjections || report.TotalAvailableSlots < MinFreeSlotsInjection || report.AvgHardwareLoad >= 100.0 {
		// Tenta di riattivare un nodo esistente in draining
		if job.tryReactivateNode(ctx, "injection") {
			log.Printf("[Autoscaler-injection] Reactivated node from draining instead of scaling up")
			return
		}

		// Se non ci sono nodi da riattivare, procedi con lo Scale up
		if job.tryAcquireLock(ctx, "injection") {
			log.Printf("[Autoscaler-injection] Scaling UP (Slots: %d, HW Load: %.2f)", report.TotalAvailableSlots, report.AvgHardwareLoad)
			go job.provisioner.ScaleUp(context.Background(), domain.NodeTypeInjection)
		}
	}

	// Scale Down: Se abbiamo troppa capacità e l'hardware è scarico (<20%)
	// modificare variabile harcoded

	if report.TotalNodes > MinActiveInjections && report.AvgHardwareLoad < 20.0 && report.TotalAvailableSlots > 12 {
		job.markVictimForDraining(ctx, "injection")
	}
}

// Relay
func (job *AutoscalerJob) manageRelayPool(ctx context.Context) {
	report, err := job.relayCalc.GetStandalonePoolReport(ctx)
	if err != nil || report.TotalNodes == 0 {
		return
	}

	// Regola: Vogliamo sempre almeno 1 nodo spare e due slot per nuove sessioni
	if report.SpareNodes < 1 || report.NodesForDeepening < 1 || report.AvgHardwareLoad >= 100.0 {

		if job.tryReactivateNode(ctx, "relay") {
			log.Printf("[Autoscaler-relay] Reactivated relay from draining")
			return
		}

		if job.tryAcquireLock(ctx, "relay") {
			log.Printf("[Autoscaler-relay] Scaling UP (Spare: %d, Deepening: %d, HW: %.2f)",
				report.SpareNodes, report.NodesForDeepening, report.AvgHardwareLoad)
			go job.provisioner.ScaleUp(context.Background(), domain.NodeTypeRelay)
		}
	}

	// Scale Down: Se abbiamo più di 1 nodo completamente vuoto
	if report.TotalNodes > MinActiveRelays && report.SpareNodes > 1 {
		job.markVictimForDraining(ctx, "relay")
	}
}

// Egress
func (job *AutoscalerJob) manageEgressPool(ctx context.Context) {
	report, err := job.egressCalc.GetPoolReport(ctx)
	if err != nil {
		return
	}

	// Regola: Scala up solo se tutti i nodi sono saturi, ci sono meno di 5 posto o hardware saturo
	if report.TotalFreeSlots < MinFreeSlotsEgress || report.SaturatedNodesCount >= report.TotalNodes || report.AvgHardwareLoad >= 100.0 {
		if job.tryReactivateNode(ctx, "egress") {
			log.Printf("[Autoscaler-egress] Reactivated egress from draining")
			return
		}
		if job.tryAcquireLock(ctx, "egress") {
			log.Printf("[Autoscaler-egress] All %d nodes are saturated. Scaling UP.", report.TotalNodes)
			go job.provisioner.ScaleUp(context.Background(), domain.NodeTypeEgress)
		}
	}

	// Scale Down
	// modificare variabile harcoded
	if report.TotalNodes > MinActiveEgresses && report.TotalFreeSlots > 15 {
		job.markVictimForDraining(ctx, "egress")
	}
}

// tryReactivateNode cerca il nodo in draining con più carico per recuperarlo
func (job *AutoscalerJob) tryReactivateNode(ctx context.Context, nodeType string) bool {
	nodeIds, _ := job.redis.GetNodePool(ctx, nodeType)

	var bestCandidate string
	maxLoad := -1

	for _, id := range nodeIds {
		status, _ := job.redis.GetNodeStatus(ctx, id)
		if status == "draining" {
			load, _ := job.getNodeLoad(ctx, id, nodeType)
			if load > maxLoad {
				maxLoad = load
				bestCandidate = id
			}
		}
	}

	if bestCandidate != "" {
		log.Printf("[Autoscaler] Reactivating most loaded draining node: %s (Load: %d)", bestCandidate, maxLoad)
		job.redis.SetNodeStatus(ctx, bestCandidate, "active")

		if nodeType == "injection" {
			children, _ := job.redis.GetNodeChildren(ctx, bestCandidate)
			for _, cid := range children {
				job.redis.SetNodeStatus(ctx, cid, "active")
			}
		}

		lockKey := fmt.Sprintf("lock:scaling:%s", nodeType)
		job.redis.Del(ctx, lockKey)
		return true
	}
	return false
}

// markVictimForDraining cerca il nodo attivo con meno carico per metterlo in draining
func (job *AutoscalerJob) markVictimForDraining(ctx context.Context, nodeType string) {
	nodeIds, _ := job.redis.GetNodePool(ctx, nodeType)

	var bestVictim string
	minLoad := math.MaxInt32
	now := time.Now().Unix()

	for _, id := range nodeIds {
		nodeInfo, err := job.redis.GetNodeProvisioning(ctx, id)
		if err != nil || nodeInfo.Role == "root" {
			continue
		}
		status, _ := job.redis.GetNodeStatus(ctx, id)
		if status == "active" {

			// info.CreatedAt è popolato dal Controller durante il provisioning
			if (now - nodeInfo.CreatedAt) < int64(NodeMinLifeTime.Seconds()) {
				continue // Il nodo è troppo giovane, non lo spegniamo
			}
			load, _ := job.getNodeLoad(ctx, id, nodeType)
			if load < minLoad {
				minLoad = load
				bestVictim = id
			}
		}
	}
	if bestVictim != "" {
		log.Printf("[Autoscaler] DRAINING least loaded node: %s (Load: %d)", bestVictim, minLoad)
		job.redis.SetNodeStatus(ctx, bestVictim, "draining")
	}
}

func (job *AutoscalerJob) cleanupDrainingNodes(ctx context.Context) {
	tiers := []string{"injection", "relay", "egress"}
	for _, tier := range tiers {
		nodeIds, _ := job.redis.GetNodePool(ctx, tier)
		for _, id := range nodeIds {
			status, _ := job.redis.GetNodeStatus(ctx, id)
			if status == "draining" {
				if job.isLogicallyEmpty(ctx, id, tier) {
					log.Printf("[Autoscaler] Final Cleanup: %s (%s) is empty.", id, tier)
					job.provisioner.DestroyNode(ctx, id, tier)
				}

			}
		}
	}
}

func (job *AutoscalerJob) getNodeLoad(ctx context.Context, nodeId, nodeType string) (int, error) {
	switch nodeType {
	case "injection":
		score, err := job.redis.ZScore(ctx, "pool:injection:load", nodeId)
		return int(score), err
	case "relay":
		score, err := job.redis.ZScore(ctx, "pool:relay:load", nodeId)
		return int(score), err
	case "egress":
		count, err := job.redis.GetNodeTotalViewers(ctx, nodeId)
		return int(count), err
	}
	return 1, nil
}

func (job *AutoscalerJob) tryAcquireLock(ctx context.Context, tier string) bool {
	lockKey := fmt.Sprintf("lock:scaling:%s", tier)
	acquired, _ := job.redis.SetNX(ctx, lockKey, "busy", ScalingCooldown)
	return acquired
}

// isLogicallyEmpty controlla se un nodo non ha più percorsi mesh attivi
func (job *AutoscalerJob) isLogicallyEmpty(ctx context.Context, id, nodeType string) bool {
	if nodeType == "egress" {
		// Un Egress è vuoto logicamente solo se non ha più mountpoints
		key := fmt.Sprintf("node:%s:mountpoints", id)
		count, _ := job.redis.GetRedisClient().SCard(ctx, key).Result()
		return count == 0
	}
	// Per Injection e Relay, il carico coincide con le sessioni
	load, _ := job.getNodeLoad(ctx, id, nodeType)
	return load == 0
}
