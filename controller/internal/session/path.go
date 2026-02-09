package session

import (
	"context"
	"controller/internal/redis"
	"fmt"
)

// BuildPath recupera la catena attuale da Redis e costruisce il percorso completo fino all'Egress
func BuildPath(ctx context.Context, redisClient *redis.Client, sessionId string, targetRelayId string, egressId string) ([]string, error) {
	// Recupera la catena
	chain, err := redisClient.GetSessionChain(ctx, sessionId)
	if err != nil {
		return nil, fmt.Errorf("failed to get session chain: %w", err)
	}

	// Costruisce il percorso troncando la catena al relay scelto
	// (Hole-filling potrebbe aver scelto un relay a metà della catena)
	finalPath := []string{}
	found := false
	for _, nodeId := range chain {
		finalPath = append(finalPath, nodeId)
		if nodeId == targetRelayId {
			found = true
			break
		}
	}

	if !found {
		return nil, fmt.Errorf("relay %s not found in session chain", targetRelayId)
	}

	// Aggiunge l'Egress finale
	finalPath = append(finalPath, egressId)

	return finalPath, nil
}

func GetNextHop(path []string, nodeId string) (string, error) {
	for i, node := range path {
		if node == nodeId {
			if i+1 >= len(path) {
				return "", fmt.Errorf("node %s is last in path", nodeId)
			}
			return path[i+1], nil
		}
	}
	return "", fmt.Errorf("node %s not found in path", nodeId)
}
