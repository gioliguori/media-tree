package tree

// // ValidateMesh verifica che la struttura di base sia corretta
// func (tm *TreeManager) ValidateMesh(ctx context.Context) (bool, []string, error) {
// 	issues := make([]string, 0)

// 	// Recupera tutti i nodi provisionati
// 	nodes, err := tm.redis.GetAllProvisionedNodes(ctx)
// 	if err != nil {
// 		return false, nil, err
// 	}

// 	for _, node := range nodes {
// 		children, _ := tm.redis.GetNodeChildren(ctx, node.NodeId)

// 		// Check: Ogni Injection deve avere esattamente un figlio (RelayRoot)
// 		if node.NodeType == domain.NodeTypeInjection {
// 			if len(children) == 0 {
// 				issues = append(issues, fmt.Sprintf("Injection %s has no RelayRoot linked", node.NodeId))
// 			}
// 			if len(children) > 1 {
// 				issues = append(issues, fmt.Sprintf("Injection %s has multiple children (invalid static pair)", node.NodeId))
// 			}
// 		}
// 	}

// 	return len(issues) == 0, issues, nil
// }
