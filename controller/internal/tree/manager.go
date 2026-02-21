package tree

import (
	"context"
	"fmt"
	"log"
	"sync"

	"controller/internal/domain"
	"controller/internal/provisioner"
	"controller/internal/redis"
)

type TreeManager struct {
	redis       *redis.Client
	provisioner provisioner.Provisioner
}

func NewTreeManager(redis *redis.Client, prov provisioner.Provisioner) *TreeManager {
	return &TreeManager{
		redis:       redis,
		provisioner: prov,
	}
}

// Bootstrap inizializza la mesh minima
func (tm *TreeManager) Bootstrap(ctx context.Context) error {
	log.Println("[TreeManager] Starting bootstrap of minimum mesh...")

	// 1. Creiamo il modulo d'ingresso (Injection [ingress] + RelayRoot [root])
	if _, err := tm.CreateNode(ctx, domain.NodeTypeInjection, "ingress"); err != nil {
		return fmt.Errorf("failed to bootstrap ingress module: %w", err)
	}

	// 2. Creiamo un Relay Standalone iniziale per il pool
	if _, err := tm.CreateNode(ctx, domain.NodeTypeRelay, "standalone"); err != nil {
		return fmt.Errorf("failed to bootstrap standalone relay: %w", err)
	}

	// 3. Creiamo un EgressNode iniziale (Ruolo: edge)
	if _, err := tm.CreateNode(ctx, domain.NodeTypeEgress, "edge"); err != nil {
		return fmt.Errorf("failed to bootstrap egress node: %w", err)
	}

	log.Println("[TreeManager] Bootstrap completed successfully")
	return nil
}

func (tm *TreeManager) CreateNode(ctx context.Context, nodeType domain.NodeType, role string) ([]*domain.NodeInfo, error) {
	log.Printf("[PoolManager] Request to create node of type: %s", nodeType)

	maxSlots := 0
	switch nodeType {
	case domain.NodeTypeRelay:
		maxSlots = 20
	case domain.NodeTypeInjection:
		maxSlots = 10
	}

	if nodeType == domain.NodeTypeInjection {
		// Logica speciale: l'injection richiede sempre un RelayRoot statico
		injId, _ := tm.generateNodeID(ctx, "injection")
		rootId, _ := tm.generateNodeID(ctx, "relay-root")

		log.Printf("[TreeManager] Logic Pair: %s <-> %s", injId, rootId)

		// Chiamiamo il Provisioner passando entrambi gli Id
		node, err := tm.provisioner.CreateNode(ctx, domain.NodeSpec{
			NodeId:      injId,
			NodeType:    nodeType,
			MaxSlots:    maxSlots,
			RelayRootId: rootId,
		}, role)

		if err != nil {
			return nil, fmt.Errorf("provisioner failed for injection: %w", err)
		}

		// Registrazione nei Pool
		tm.redis.AddNodeToPool(ctx, "injection", injId)

		// Registrazione del Relay Root
		tm.redis.AddNodeToPool(ctx, "relay", rootId)

		// Creazione Topologia
		if err := tm.redis.AddNodeChild(ctx, injId, rootId); err != nil {
			log.Printf("[WARN] Failed to link child: %v", err)
		}
		if err := tm.redis.AddNodeParent(ctx, rootId, injId); err != nil {
			log.Printf("[WARN] Failed to link parent: %v", err)
		}

		rootInfo, _ := tm.redis.GetNodeProvisioning(ctx, rootId)

		return []*domain.NodeInfo{node, rootInfo}, nil

	}

	// Logica Standard per Relay ed Egress
	nodeId, err := tm.generateNodeID(ctx, string(nodeType))
	if err != nil {
		return nil, err
	}

	node, err := tm.provisioner.CreateNode(ctx, domain.NodeSpec{
		NodeId:   nodeId,
		NodeType: nodeType,
		MaxSlots: maxSlots,
	}, role)
	if err != nil {
		return nil, fmt.Errorf("provisioner failed for %s: %w", nodeId, err)
	}

	node.MaxSlots = maxSlots

	// Registrazione nel pool globale su Redis
	if err := tm.redis.AddNodeToPool(ctx, string(nodeType), nodeId); err != nil {
		log.Printf("[WARN] Failed to add node %s to pool: %v", nodeId, err)
	}

	return []*domain.NodeInfo{node}, nil
}

// DestroyAllNodes pulisce tutto il sistema in parallelo
func (tm *TreeManager) DestroyAllNodes(ctx context.Context) error {
	nodes, err := tm.redis.GetAllProvisionedNodes(ctx)
	if err != nil {
		return err
	}

	var wg sync.WaitGroup
	for _, node := range nodes {
		wg.Add(1)
		go func(n *domain.NodeInfo) {
			defer wg.Done()
			tm.provisioner.DestroyNode(ctx, n)
		}(node)
	}

	// metrics agent
	wg.Add(1)
	go func() {
		defer wg.Done()
		// NodeInfo fake
		agentInfo := &domain.NodeInfo{NodeId: "metrics-agent"}
		if err := tm.provisioner.DestroyNode(ctx, agentInfo); err != nil {
			log.Printf("[WARN] Metrics agent was not running or could not be stopped")
		}
	}()

	wg.Wait()
	return nil
}

// ListNodes ritorna la lista di tutti i nodi attivi
func (tm *TreeManager) ListNodes(ctx context.Context) ([]*domain.NodeInfo, error) {
	return tm.redis.GetAllProvisionedNodes(ctx)
}

// CleanupNodeRedis è una funzione di utilità per pulire chiavi orfane
func (tm *TreeManager) CleanupNodeRedis(ctx context.Context, nodeId string) {
	tm.redis.Del(ctx, fmt.Sprintf("node:%s", nodeId))
	tm.redis.Del(ctx, fmt.Sprintf("node:%s:provisioning", nodeId))
	tm.redis.Del(ctx, fmt.Sprintf("node:%s:children", nodeId))
	tm.redis.Del(ctx, fmt.Sprintf("node:%s:parents", nodeId))
	tm.redis.Del(ctx, fmt.Sprintf("node:%s:sessions", nodeId))
}
