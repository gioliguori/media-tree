package redis

import (
	"context"
	"fmt"
	"strconv"
)

// NodeData rappresenta ESATTAMENTE quello che BaseNode salva in Redis
type NodeData struct {
	NodeId    string
	Type      string // "injection", "relay", "egress"
	TreeId    string
	Host      string
	Port      int
	AudioPort int
	VideoPort int
	Layer     int
	Status    string
	Created   int64
}

// GetNode legge un nodo da Redis
// Chiave: tree:{treeId}:node:{nodeId}
func (c *Client) GetNode(ctx context.Context, treeId, nodeId string) (*NodeData, error) {
	key := fmt.Sprintf("tree:%s:node:%s", treeId, nodeId)

	result, err := c.rdb.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get node %s: %w", nodeId, err)
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("node not found: %s", nodeId)
	}

	// Parse hash Redis in NodeData struct
	node := &NodeData{
		NodeId: result["nodeId"],
		Type:   result["type"],
		TreeId: result["treeId"],
		Host:   result["host"],
		Status: result["status"],
	}

	// Parse campi int
	if port, err := strconv.Atoi(result["port"]); err == nil {
		node.Port = port
	}
	if audioPort, err := strconv.Atoi(result["audioPort"]); err == nil {
		node.AudioPort = audioPort
	}
	if videoPort, err := strconv.Atoi(result["videoPort"]); err == nil {
		node.VideoPort = videoPort
	}
	if layer, err := strconv.Atoi(result["layer"]); err == nil {
		node.Layer = layer
	}
	if created, err := strconv.ParseInt(result["created"], 10, 64); err == nil {
		node.Created = created
	}

	return node, nil
}

// GetTreeNodes legge tutti i nodi di un tree per tipo
// Chiave: tree:{treeId}:{nodeType} (injection, relay, egress)
// Ritorna: lista di nodeId
func (c *Client) GetTreeNodes(ctx context.Context, treeId, nodeType string) ([]string, error) {
	key := fmt.Sprintf("tree:%s:%s", treeId, nodeType)

	nodeIds, err := c.rdb.SMembers(ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get tree nodes %s/%s: %w", treeId, nodeType, err)
	}

	return nodeIds, nil
}

// GetAllTreeNodes legge TUTTI i nodi di un tree (injection + relay + egress)
// Ritorna: lista completa di NodeData
func (c *Client) GetAllTreeNodes(ctx context.Context, treeId string) ([]*NodeData, error) {
	var allNodes []*NodeData

	// Leggi ogni tipo di nodo
	for _, nodeType := range []string{"injection", "relay", "egress"} {
		nodeIds, err := c.GetTreeNodes(ctx, treeId, nodeType)
		if err != nil {
			return nil, err
		}

		// Leggi metadata di ogni nodo
		for _, nodeId := range nodeIds {
			node, err := c.GetNode(ctx, treeId, nodeId)
			if err != nil {
				// Skip nodi non validi (potrebbero essere expired)
				continue
			}
			allNodes = append(allNodes, node)
		}
	}

	return allNodes, nil
}

// NodeExists verifica se un nodo esiste in Redis
func (c *Client) NodeExists(ctx context.Context, treeId, nodeId string) (bool, error) {
	key := fmt.Sprintf("tree:%s:node:%s", treeId, nodeId)

	exists, err := c.rdb.Exists(ctx, key).Result()
	if err != nil {
		return false, fmt.Errorf("failed to check node existence %s: %w", nodeId, err)
	}

	return exists > 0, nil
}

// ForceDeleteNode rimuove un nodo da Redis (cleanup forzato)
// Usa SOLO quando il container è già morto e non può unregistersi
func (c *Client) ForceDeleteNode(ctx context.Context, treeId, nodeId, nodeType string) error {
	// Rimuovi hash nodo
	nodeKey := fmt.Sprintf("tree:%s:node:%s", treeId, nodeId)
	if err := c.rdb.Del(ctx, nodeKey).Err(); err != nil {
		return fmt.Errorf("failed to delete node hash %s: %w", nodeId, err)
	}

	// Rimuovi da set tipo
	setKey := fmt.Sprintf("tree:%s:%s", treeId, nodeType)
	if err := c.rdb.SRem(ctx, setKey, nodeId).Err(); err != nil {
		return fmt.Errorf("failed to remove node from set %s: %w", nodeId, err)
	}

	// Rimuovi topologia (parent/children)
	// Questo pulisce anche se il nodo aveva relazioni
	parentKey := fmt.Sprintf("tree:%s:parent:%s", treeId, nodeId)
	c.rdb.Del(ctx, parentKey)

	childrenKey := fmt.Sprintf("tree:%s:children:%s", treeId, nodeId)
	c.rdb.Del(ctx, childrenKey)

	return nil
}

// GetNodesByType è un alias più leggibile di GetTreeNodes
func (c *Client) GetNodesByType(ctx context.Context, treeId string, nodeType string) ([]string, error) {
	return c.GetTreeNodes(ctx, treeId, nodeType)
}

// GetInjectionNodes legge tutti gli injection nodes
func (c *Client) GetInjectionNodes(ctx context.Context, treeId string) ([]string, error) {
	return c.GetTreeNodes(ctx, treeId, "injection")
}

// GetRelayNodes legge tutti i relay nodes
func (c *Client) GetRelayNodes(ctx context.Context, treeId string) ([]string, error) {
	return c.GetTreeNodes(ctx, treeId, "relay")
}

// GetEgressNodes legge tutti gli egress nodes
func (c *Client) GetEgressNodes(ctx context.Context, treeId string) ([]string, error) {
	return c.GetTreeNodes(ctx, treeId, "egress")
}
