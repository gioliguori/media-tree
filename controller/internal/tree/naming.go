package tree

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// forse poco efficiente ma lavoriamo con numero piccolo di nodi

// generateNodeID trova il primo ID libero per un tipo di nodo (Gap Detection)
func (tm *TreeManager) generateNodeID(ctx context.Context, treeId string, nodeNamePrefix string) (string, error) {
	// Recupera i nodi di questo albero
	nodes, err := tm.redis.GetAllProvisionedNodes(ctx, treeId)
	if err != nil {
		return "", fmt.Errorf("failed to list nodes for naming: %w", err)
	}

	// Prefisso da cercare (es: "injection-")
	prefix := fmt.Sprintf("%s-", nodeNamePrefix)

	existingIndices := make([]int, 0)

	// Estrai i numeri dai nodi esistenti
	for _, node := range nodes {
		// Controlla se il nodo inizia con il prefisso
		if strings.HasPrefix(node.NodeId, prefix) {
			// Estrai parte numerica
			suffix := strings.TrimPrefix(node.NodeId, prefix)
			if idx, err := strconv.Atoi(suffix); err == nil {
				existingIndices = append(existingIndices, idx)
			}
		}
	}

	// Ordina i numeri
	sort.Ints(existingIndices)

	// Trova il primo buco
	nextIndex := 1
	for _, idx := range existingIndices {
		if idx == nextIndex {
			// Trovato, passo al successivo
			nextIndex++
		} else if idx > nextIndex {
			// Salto trovato
			break
		}
	}

	// Restituisci l'ID formattato
	return fmt.Sprintf("%s%d", prefix, nextIndex), nil
}
