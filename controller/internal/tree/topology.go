package tree

import (
	"context"
	"fmt"
	"log"

	"controller/internal/domain"
)

// configureTopology configura topologia Redis secondo layer
// Layer 0 (injection) -> Layer 1 (relay): FULL MESH
// Layer N>0 -> Layer N+1: ROUND ROBIN
func (tm *TreeManager) configureTopology(
	ctx context.Context,
	treeId string,
	allNodes []*domain.NodeInfo,
) error {
	log.Printf("[INFO] Configuring topology for tree %s", treeId)
	// Raggruppa nodi per layer
	nodesByLayer := make(map[int][]*domain.NodeInfo)
	maxLayer := 0

	for _, node := range allNodes {
		nodesByLayer[node.Layer] = append(nodesByLayer[node.Layer], node)
		if node.Layer > maxLayer {
			maxLayer = node.Layer
		}
	}

	// Itera sui layer e crea le connessioni
	for layer := 0; layer < maxLayer; layer++ {
		parentsLayer := nodesByLayer[layer]
		childrenLayer := nodesByLayer[layer+1]

		if len(parentsLayer) == 0 || len(childrenLayer) == 0 {
			continue // Skip layer vuoti
		}

		// I nodi Egress sono foglie, non possono essere genitori anche se si trovano a layer intermedi
		parents := filterNonEgress(parentsLayer)
		if len(parents) == 0 {
			continue // Nessun parent valido
		}

		if layer == 0 {
			// FULL MESH: Injection -> Relay
			if err := tm.connectFullMesh(ctx, treeId, parents, childrenLayer); err != nil {
				return err
			}
		} else {
			// ROUND ROBIN: Relay -> Egress (o Relay -> Relay)
			if err := tm.connectRoundRobin(ctx, treeId, parents, childrenLayer); err != nil {
				return err
			}
		}
	}

	return nil
}

// connectFullMesh connette ogni parent a TUTTI i children (Layer 0 -> 1)
func (tm *TreeManager) connectFullMesh(
	ctx context.Context,
	treeId string,
	parents, children []*domain.NodeInfo,
) error {
	log.Printf("[INFO] Full mesh: %d parents -> %d children", len(parents), len(children))

	for _, parent := range parents {
		for _, child := range children {
			if err := tm.redis.SetTopology(ctx, treeId, child.NodeId, parent.NodeId); err != nil {
				return fmt.Errorf("failed to link %s <-> %s: %w", parent.NodeId, child.NodeId, err)
			}

			log.Printf("[TOPOLOGY] %s -> %s (full mesh)", parent.NodeId, child.NodeId)
		}
	}

	return nil
}

// connectRoundRobin: Distribuisce i figli equamente tra i genitori.
func (tm *TreeManager) connectRoundRobin(
	ctx context.Context,
	treeId string,
	parents, children []*domain.NodeInfo,
) error {
	log.Printf("[INFO] Round robin: %d parents -> %d children", len(parents), len(children))
	childrenPerParent := len(children) / len(parents)
	extraChildren := len(children) % len(parents)

	childIndex := 0

	for parentIdx, parent := range parents {
		// I primi genitori prendono un figlio in più per distribuire il resto
		count := childrenPerParent
		if parentIdx < extraChildren {
			count++
		}

		// Assegna children a questo parent
		for i := 0; i < count && childIndex < len(children); i++ {
			child := children[childIndex]

			if err := tm.redis.SetTopology(ctx, treeId, child.NodeId, parent.NodeId); err != nil {
				return fmt.Errorf("failed to link %s <-> %s: %w", parent.NodeId, child.NodeId, err)
			}

			log.Printf("[TOPOLOGY] %s -> %s (round robin)", parent.NodeId, child.NodeId)
			childIndex++
		}
	}

	return nil
}

// publishTopologyEvents: Notifica i nodi (via Pub/Sub) che la loro topologia è cambiata
// I nodi Node.js ascoltano questi eventi per aggiornare le loro tabelle di routing interne
func (tm *TreeManager) publishTopologyEvents(
	ctx context.Context,
	treeId string,
	allNodes []*domain.NodeInfo,
) error {
	log.Printf("[INFO] Publishing topology events for tree %s", treeId)
	for _, node := range allNodes {
		// Leggi parents da Redis
		parents, _ := tm.redis.GetNodeParents(ctx, treeId, node.NodeId)

		// Pubblica parent-added per ogni parent
		for _, parentId := range parents {
			if err := tm.redis.PublishParentAdded(ctx, treeId, node.NodeId, parentId); err != nil {
				log.Printf("[WARN] Failed to publish parent-added for %s: %v\n", node.NodeId, err)
			}
		}

		// Leggi children da Redis (solo se non è egress)
		if node.NodeType != domain.NodeTypeEgress {
			children, _ := tm.redis.GetNodeChildren(ctx, treeId, node.NodeId)

			// Pubblica child-added per ogni child
			for _, childId := range children {
				if err := tm.redis.PublishChildAdded(ctx, treeId, node.NodeId, childId); err != nil {
					log.Printf("[WARN] Failed to publish child-added for %s: %v\n", node.NodeId, err)
				}
			}
		}
	}

	return nil
}

// filterNonEgress filtra via egress nodes (che sono foglie)
func filterNonEgress(nodes []*domain.NodeInfo) []*domain.NodeInfo {
	filtered := make([]*domain.NodeInfo, 0, len(nodes))
	for _, node := range nodes {
		if node.NodeType != domain.NodeTypeEgress {
			filtered = append(filtered, node)
		}
	}
	return filtered
}

// ValidateTopology
func (tm *TreeManager) ValidateTopology(ctx context.Context, treeId string) (bool, []string, error) {
	issues := make([]string, 0)

	nodes, err := tm.redis.GetAllProvisionedNodes(ctx, treeId)
	if err != nil {
		return false, nil, fmt.Errorf("failed to get nodes: %w", err)
	}

	for _, node := range nodes {
		parents, _ := tm.redis.GetNodeParents(ctx, treeId, node.NodeId)
		children, _ := tm.redis.GetNodeChildren(ctx, treeId, node.NodeId)

		// Check 1: Injection (layer 0) non ha parents
		if node.NodeType == domain.NodeTypeInjection {
			if len(parents) > 0 {
				issues = append(issues, fmt.Sprintf("Injection %s should not have parents", node.NodeId))
			}
			if len(children) == 0 {
				issues = append(issues, fmt.Sprintf("Injection %s has no children", node.NodeId))
			}
		}

		// Check 2: Relay ha parents E children
		if node.NodeType == domain.NodeTypeRelay {
			if len(parents) == 0 {
				issues = append(issues, fmt.Sprintf("Relay %s has no parents", node.NodeId))
			}
			if len(children) == 0 {
				issues = append(issues, fmt.Sprintf("Relay %s has no children", node.NodeId))
			}
		}

		// Check 3: Egress ha parents ma NO children (foglia)
		if node.NodeType == domain.NodeTypeEgress {
			if len(parents) == 0 {
				issues = append(issues, fmt.Sprintf("Egress %s has no parents", node.NodeId))
			}
			if len(children) > 0 {
				issues = append(issues, fmt.Sprintf("Egress %s should not have children (leaf node)", node.NodeId))
			}
		}
	}

	return len(issues) == 0, issues, nil
}
