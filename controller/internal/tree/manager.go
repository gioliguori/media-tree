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
	log.Println("[PoolManager] Starting bootstrap of minimum mesh...")

	// Creiamo le unità base necessarie
	typesToCreate := []domain.NodeType{
		domain.NodeTypeInjection, // Questo creerà automaticamente anche il RelayRoot
		domain.NodeTypeRelay,     // Relay
		domain.NodeTypeEgress,    // Egress
	}

	for _, nType := range typesToCreate {
		if _, err := tm.CreateNode(ctx, nType); err != nil {
			return fmt.Errorf("failed to bootstrap node type %s: %w", nType, err)
		}
	}

	log.Println("[PoolManager] Bootstrap completed successfully")
	return nil
}

func (tm *TreeManager) CreateNode(ctx context.Context, nodeType domain.NodeType) ([]*domain.NodeInfo, error) {
	log.Printf("[PoolManager] Request to create node of type: %s", nodeType)

	if nodeType == domain.NodeTypeInjection {
		// Logica speciale: l'injection richiede sempre un RelayRoot statico
		injId, _ := tm.generateNodeID(ctx, "injection")
		rootId, _ := tm.generateNodeID(ctx, "relay-root")
		return tm.createInjectionPair(ctx, injId, rootId)
	}

	// Logica Standard per Relay ed Egress
	nodeId, err := tm.generateNodeID(ctx, string(nodeType))
	if err != nil {
		return nil, err
	}

	node, err := tm.provisioner.CreateNode(ctx, domain.NodeSpec{
		NodeId:   nodeId,
		NodeType: nodeType,
	})
	if err != nil {
		return nil, fmt.Errorf("provisioner failed for %s: %w", nodeId, err)
	}

	// Registrazione nel pool globale su Redis
	if err := tm.redis.AddNodeToPool(ctx, string(nodeType), nodeId); err != nil {
		log.Printf("[WARN] Failed to add node %s to pool: %v", nodeId, err)
	}

	return []*domain.NodeInfo{node}, nil
}

// Crea 1 Injection + 1 RelayRoot, li collega e li mette nei pool
// Viene usato sia all'avvio (CreateTree) sia durante lo scaling (ScaleUpInjection)
func (tm *TreeManager) createInjectionPair(ctx context.Context, injId, rootId string) ([]*domain.NodeInfo, error) {
	log.Printf("[TreeManager] Provisioning pair: %s <-> %s", injId, rootId)

	created := []*domain.NodeInfo{}

	// Crea Injection
	injSpec := domain.NodeSpec{
		NodeId:   injId,
		NodeType: domain.NodeTypeInjection,
	}
	injNode, err := tm.provisioner.CreateNode(ctx, injSpec)
	if err != nil {
		return nil, fmt.Errorf("failed to create injection %s: %w", injId, err)
	}
	created = append(created, injNode)

	// Crea Relay Root
	relaySpec := domain.NodeSpec{
		NodeId:   rootId,
		NodeType: domain.NodeTypeRelay,
	}
	relayNode, err := tm.provisioner.CreateNode(ctx, relaySpec)
	if err != nil {
		// Rollback
		log.Printf("[WARN] Relay provisioning failed. Rolling back injection %s...", injId)
		_ = tm.provisioner.DestroyNode(ctx, injNode)
		return nil, fmt.Errorf("failed to create relay root %s: %w", rootId, err)
	}
	created = append(created, relayNode)

	//  Redis Topology (Parent/Child)
	if err := tm.redis.AddNodeChild(ctx, injId, rootId); err != nil {
		log.Printf("[WARN] Failed to link child: %v", err)
	}
	if err := tm.redis.AddNodeParent(ctx, rootId, injId); err != nil {
		log.Printf("[WARN] Failed to link parent: %v", err)
	}

	// Redis Pools
	if err := tm.redis.AddNodeToPool(ctx, "injection", injId); err != nil {
		log.Printf("[WARN] Failed to add injection to pool: %v", err)
	}
	if err := tm.redis.AddNodeToPool(ctx, "relay", rootId); err != nil {
		log.Printf("[WARN] Failed to add relay to pool: %v", err)
	}

	return created, nil
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
