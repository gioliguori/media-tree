package tree

import (
	"context"
	"controller/internal/domain"
	"fmt"
	"log"
)

// ScaleUpInjection implementa l'interfaccia ProvisionerClient richiesta dall'autoscaler
func (tm *TreeManager) ScaleUpInjection(ctx context.Context, treeId string) error {
	log.Printf("[TreeManager] Scaling up: Provisioning new Injection+Relay pair for tree %s", treeId)

	injectionId, err := tm.generateNodeID(ctx, treeId, "injection")
	if err != nil {
		return fmt.Errorf("failed to generate injection ID: %w", err)
	}

	relayRootId, err := tm.generateNodeID(ctx, treeId, "relay-root")
	if err != nil {
		return fmt.Errorf("failed to generate relay-root ID: %w", err)
	}

	log.Printf("[TreeManager] ID Assigned: %s <-> %s", injectionId, relayRootId)

	// Crea la coppia usando la funzione del manager
	_, err = tm.createInjectionPair(ctx, treeId, injectionId, relayRootId)
	if err != nil {
		return fmt.Errorf("scale up failed: %w", err)
	}

	log.Printf("[SUCCESS] Scale Up Complete: Pair %s <-> %s created active", injectionId, relayRootId)
	return nil
}

// Converte le stringhe dell'Autoscaler in una struct NodeInfo per il Provisioner e chiama DestroyNode()
func (tm *TreeManager) DestroyNode(ctx context.Context, treeId, nodeId, nodeType string) error {
	log.Printf("[TreeManager] Scaling down: Autoscaler requested destroy for node %s (%s)", nodeId, nodeType)

	// Flag stato a "destroying"
	if err := tm.redis.SetNodeStatus(ctx, treeId, nodeId, "destroying"); err != nil {
		log.Printf("[WARN] Failed to set status destroying for %s: %v", nodeId, err)
	}
	// Se è un injection, dobbiamo prima distruggere il relayroot figlio
	if nodeType == "injection" {
		children, err := tm.redis.GetNodeChildren(ctx, treeId, nodeId)
		if err == nil {

			for _, childId := range children {
				tm.redis.SetNodeStatus(ctx, treeId, childId, "destroying")
			}
			for _, childId := range children {
				log.Printf("[TreeManager] Cascading destroy to child relay: %s", childId)

				if err := tm.DestroyNode(ctx, treeId, childId, "relay"); err != nil {
					log.Printf("[WARN] Failed to destroy child relay %s: %v", childId, err)
				}
			}
		} else {
			log.Printf("[WARN] Failed to get children for %s: %v", nodeId, err)
		}
	}

	partialNodeInfo := &domain.NodeInfo{
		TreeId:   treeId,
		NodeId:   nodeId,
		NodeType: domain.NodeType(nodeType),
	}

	// Chiamiamo il provisioner che gestisce: Stop Docker -> Remove Docker -> Clean Redis
	if err := tm.provisioner.DestroyNode(ctx, partialNodeInfo); err != nil {
		return fmt.Errorf("failed to destroy node %s: %w", nodeId, err)
	}

	return nil
}
