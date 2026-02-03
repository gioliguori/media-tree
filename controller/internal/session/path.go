package session

import (
	"context"
	"fmt"
	"log"
	"strings"

	"controller/internal/redis"
)

// BuildPath costruisce path da injection a egress usando layer
func BuildPath(
	ctx context.Context,
	redisClient *redis.Client,
	sessionId string,
	injectionId string,
	relayRootId string,
	egressId string,
) ([]string, error) {
	// Injection -> Relay-root
	path := []string{injectionId, relayRootId}

	// // Get layer egress
	// egressNode, err := redisClient.GetNodeProvisioning(ctx, treeId, egressId)
	// if err != nil {
	// 	return nil, fmt.Errorf("egress not found: %w", err)
	// }
	// egressLayer := egressNode.Layer
	// log.Printf("[BuildPath] Building path:  injection=%s, egress=%s (layer %d)",
	// 	injectionId, egressId, egressLayer)
	// // Caso egress L1
	// // Path diretto:  injection -> relay-root -> egress
	// if egressLayer == 1 {
	// 	path = append(path, egressId)
	// 	log.Printf("[BuildPath] Direct path (L1): %v", path)
	// 	return path, nil
	// }

	// // Per ogni layer da 1 a (egressLayer - 1), seleziona best relay
	// for layer := 1; layer < egressLayer; layer++ {
	// 	relayId, err := SelectBestRelayAtLayer(ctx, redisClient, treeId, layer, sessionId)
	// 	if err != nil {
	// 		return nil, fmt.Errorf("no relay at layer %d: %w", layer, err)
	// 	}
	// 	path = append(path, relayId)
	// 	log.Printf("[BuildPath] Added relay %s at layer %d", relayId, layer)
	// }

	// Aggiungi egress finale
	path = append(path, egressId)

	log.Printf("[BuildPath] Complete path: %v (%d hops)", path, len(path)-1)
	return path, nil
}

// Path utilities

// ExtractRelayNodes estrae i relay da un path (skip injection e egress)
func ExtractRelayNodes(path []string) []string {
	if len(path) <= 2 {
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

// CountPathsUsingRelay conta quanti path usano un relay specifico
func CountPathsUsingRelay(paths [][]string, relayId string) int {
	count := 0
	for _, path := range paths {
		if contains(path, relayId) {
			count++
		}
	}
	return count
}

// contains verifica se slice contiene item
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
