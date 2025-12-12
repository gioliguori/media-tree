package tree

import (
	"context"
	"fmt"

	"controller/internal/domain"
)

// ValidateTopology verifica coerenza topologia
// Controlla solo le relazioni Injection → RelayRoot (unica topologia statica)
func (tm *TreeManager) ValidateTopology(ctx context.Context, treeId string) (bool, []string, error) {
	issues := make([]string, 0)

	nodes, err := tm.redis.GetAllProvisionedNodes(ctx, treeId)
	if err != nil {
		return false, nil, fmt.Errorf("failed to get nodes: %w", err)
	}

	for _, node := range nodes {
		children, _ := tm.redis.GetNodeChildren(ctx, treeId, node.NodeId)

		// Check 1: Injection (layer 0) deve avere children (relay-root)
		if node.NodeType == domain.NodeTypeInjection {
			if len(children) == 0 {
				issues = append(issues, fmt.Sprintf("Injection %s has no children", node.NodeId))
			}
		}

		// Check 2: Egress non deve avere children (foglia)
		if node.NodeType == domain.NodeTypeEgress {
			if len(children) > 0 {
				issues = append(issues, fmt.Sprintf("Egress %s should not have children (leaf node)", node.NodeId))
			}
		}
	}

	return len(issues) == 0, issues, nil
}
