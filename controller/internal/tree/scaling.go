package tree

import (
	"context"
	"controller/internal/domain"
	"fmt"
	"log"
)

func (tm *TreeManager) ScaleUp(ctx context.Context, nodeType domain.NodeType) error {
	log.Printf("[PoolManager] Scaling up: Provisioning new %s node", nodeType)

	// CreateNode gestisce già internamente la differenza tra Injection (coppia) e gli altri
	_, err := tm.CreateNode(ctx, nodeType)
	if err != nil {
		return fmt.Errorf("scale up failed for %s: %w", nodeType, err)
	}

	return nil
}

// DestroyNode gestisce la rimozione controllata di un nodo.
func (tm *TreeManager) DestroyNode(ctx context.Context, nodeId, nodeType string) error {
	log.Printf("[PoolManager] Scaling down: Destroying node %s (%s)", nodeId, nodeType)

	// Imposta lo stato a "destroying" su Redis
	if err := tm.redis.SetNodeStatus(ctx, nodeId, "destroying"); err != nil {
		log.Printf("[WARN] Failed to set status destroying for %s: %v", nodeId, err)
	}

	// Recupera le info dal database
	nodeInfo, err := tm.redis.GetNodeProvisioning(ctx, nodeId)
	if err != nil || nodeInfo == nil {
		// Fallback se le info non sono più in Redis: creiamo un oggetto parziale per il provisioner
		nodeInfo = &domain.NodeInfo{
			NodeId:   nodeId,
			NodeType: domain.NodeType(nodeType),
		}
	}

	// Se il nodo è un Injection, dobbiamo distruggere anche il RelayRoot figlio
	if nodeType == string(domain.NodeTypeInjection) {
		children, err := tm.redis.GetNodeChildren(ctx, nodeId)
		if err == nil {
			for _, childId := range children {
				log.Printf("[PoolManager] Cascading destroy to static child relay: %s", childId)
				// Chiamata ricorsiva o diretta al provisioner
				if err := tm.DestroyNode(ctx, childId, string(domain.NodeTypeRelay)); err != nil {
					log.Printf("[WARN] Failed to destroy child relay %s: %v", childId, err)
				}
			}
		}
	}

	// Chiamata al Provisioner (Stop -> Remove -> Cleanup Redis)
	if err := tm.provisioner.DestroyNode(ctx, nodeInfo); err != nil {
		return fmt.Errorf("failed to destroy node %s: %w", nodeId, err)
	}

	return nil
}
