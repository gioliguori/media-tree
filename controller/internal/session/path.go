package session

import (
	"context"
	"fmt"
	"strings"

	"controller/internal/redis"
)

func CalculatePath(
	ctx context.Context,
	redisClient *redis.Client,
	treeId string,
	injectionNodeId string,
	egressNodeId string,
) ([]string, error) {
	const maxHops = 10

	current := egressNodeId
	path := []string{current}

	// Risali l'albero fino all'injection
	for current != injectionNodeId {
		if len(path) > maxHops {
			return nil, fmt.Errorf("path too long (max %d hops, possible cycle)", maxHops)
		}

		// Leggi parents del nodo corrente
		parents, err := redisClient.GetNodeParents(ctx, treeId, current)
		if err != nil {
			return nil, fmt.Errorf("failed to get parents of %s: %w", current, err)
		}

		if len(parents) == 0 {
			return nil, fmt.Errorf("no path to injection (node %s has no parents)", current)
		}

		var parent string
		if len(parents) == 1 {
			// Caso semplice: un solo parent
			parent = parents[0]
		} else {
			// Multiple parents: scegli quello che corrisponde all'injectionNodeId richiesto
			found := false
			for _, p := range parents {
				if p == injectionNodeId {
					parent = p
					found = true
					break
				}
			}

			if !found {
				// Nessuno dei parent è l'injection richiesto
				// Significa che il path non passa per l'injection specificato
				return nil, fmt.Errorf("node %s has multiple parents %v, but none is the target injection %s", current, parents, injectionNodeId)
			}
		}

		path = append([]string{parent}, path...) // Prepend parent
		current = parent
	}

	// Verifica che il primo nodo sia effettivamente injection
	if path[0] != injectionNodeId {
		return nil, fmt.Errorf("path does not start with injection node: %s", path[0])
	}

	return path, nil
}

// CalculateAllPaths calcola paths per tutti gli egress
func CalculateAllPaths(
	ctx context.Context,
	redisClient *redis.Client,
	treeId string,
	injectionNodeId string,
	egressNodeIds []string,
) (map[string][]string, error) {
	paths := make(map[string][]string)

	for _, egressId := range egressNodeIds {
		path, err := CalculatePath(ctx, redisClient, treeId, injectionNodeId, egressId)
		if err != nil {
			return nil, fmt.Errorf("failed to calculate path to %s: %w", egressId, err)
		}
		paths[egressId] = path
	}

	return paths, nil
}

// ExtractRelayNodesFromPath estrae i relay da un path (skip injection e egress)
func ExtractRelayNodesFromPath(path []string) []string {
	if len(path) <= 2 {
		// Path troppo corto (solo injection + egress), nessun relay
		return []string{}
	}

	// Ritorna tutti i nodi tranne primo (injection) e ultimo (egress)
	return path[1 : len(path)-1]
}

// GetNextHop ritorna il next hop per un nodo nel path
func GetNextHop(path []string, nodeId string) (string, error) {
	for i, node := range path {
		if node == nodeId {
			if i+1 >= len(path) {
				return "", fmt.Errorf("node %s is last in path, no next hop", nodeId)
			}
			return path[i+1], nil
		}
	}
	return "", fmt.Errorf("node %s not found in path", nodeId)
}

// PathToString converte path in stringa comma-separated
func PathToString(path []string) string {
	return strings.Join(path, ",")
}

// StringToPath converte stringa in path array
func StringToPath(pathStr string) []string {
	if pathStr == "" {
		return []string{}
	}
	return strings.Split(pathStr, ",")
}

// ValidatePath verifica che path sia valido
func ValidatePath(path []string) error {
	if len(path) < 2 {
		return fmt.Errorf("path too short (need at least injection + egress)")
	}

	// Check nodi duplicati
	seen := make(map[string]bool)
	for _, node := range path {
		if seen[node] {
			return fmt.Errorf("path contains duplicate node: %s", node)
		}
		seen[node] = true
	}

	return nil
}
