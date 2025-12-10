package redis

import (
	"context"
	"fmt"
	"log"
)

// NodeData rappresenta quello che BaseNode salva in Redis
type NodeData struct {
	NodeId    string `redis:"nodeId"`
	NodeType  string `redis:"type"`
	TreeId    string `redis:"treeId"`
	Host      string `redis:"host"`
	Port      int    `redis:"port"`
	AudioPort int    `redis:"audioPort"`
	VideoPort int    `redis:"videoPort"`
	Layer     int    `redis:"layer"`
	Status    string `redis:"status"`
	Created   int64  `redis:"created"`
}

// GetNode legge un nodo da Redis
// Chiave: tree:{treeId}:node:{nodeId}
func (c *Client) GetNode(ctx context.Context, treeId, nodeId string) (*NodeData, error) {
	key := fmt.Sprintf("tree:%s:node:%s", treeId, nodeId)

	var node NodeData

	// .Scan() legge da Redis e riempie la struct usando i tag
	if err := c.rdb.HGetAll(ctx, key).Scan(&node); err != nil {
		return nil, fmt.Errorf("failed to get node %s: %w", nodeId, err)
	}

	// Scan non dà errore se la chiave non esiste restituisce una struct vuota.
	if node.NodeId == "" {
		return nil, fmt.Errorf("node not found: %s", nodeId)
	}

	return &node, nil
}

// GetTreeNodes legge tutti i nodi di un tree per tipo
// Chiave: tree:{treeId}:{nodeType} (injection, relay, egress)
// Ritorna: lista di nodeId
func (c *Client) GetTreeNodes(ctx context.Context, treeId, nodeType string) ([]string, error) {
	key := fmt.Sprintf("tree:%s:%s", treeId, nodeType)

	// SMembers: Legge tutti i membri di un SET Redis.
	nodeIds, err := c.rdb.SMembers(ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get tree nodes %s/%s: %w", treeId, nodeType, err)
	}

	return nodeIds, nil
}

// GetAllTreeNodes legge TUTTI i nodi di un tree
// Ritorna: lista completa di NodeData
func (c *Client) GetAllTreeNodes(ctx context.Context, treeId string) ([]*NodeData, error) {
	var allNodes []*NodeData
	skipped := 0
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
				log.Printf("[WARN] Failed to get node %s in tree %s: %v\n", nodeId, treeId, err)
				skipped++
				continue
			}
			allNodes = append(allNodes, node)
		}
	}

	if skipped > 0 {
		log.Printf("[WARN] Skipped %d invalid nodes in tree %s\n", skipped, treeId)
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

// ForceDeleteNode rimuove un nodo da Redis e pulisce i riferimenti
func (c *Client) ForceDeleteNode(ctx context.Context, treeId, nodeId, nodeType string) error {
	// Pulizia riferimenti
	parentsKey := fmt.Sprintf("tree:%s:parents:%s", treeId, nodeId)
	parents, _ := c.rdb.SMembers(ctx, parentsKey).Result()
	for _, parentId := range parents {
		c.rdb.SRem(ctx, fmt.Sprintf("tree:%s:children:%s", treeId, parentId), nodeId)
	}

	childrenKey := fmt.Sprintf("tree:%s:children:%s", treeId, nodeId)
	children, _ := c.rdb.SMembers(ctx, childrenKey).Result()
	for _, childId := range children {
		c.rdb.SRem(ctx, fmt.Sprintf("tree:%s:parents:%s", treeId, childId), nodeId)
	}

	// Cancella dati
	if err := c.rdb.Del(ctx, fmt.Sprintf("tree:%s:node:%s", treeId, nodeId)).Err(); err != nil {
		log.Printf("[WARN] Failed to delete node key %s: %v\n", nodeId, err)
	}

	if err := c.rdb.SRem(ctx, fmt.Sprintf("tree:%s:%s", treeId, nodeType), nodeId).Err(); err != nil {
		log.Printf("[WARN] Failed to remove %s from type set: %v\n", nodeId, err)
	}

	if err := c.rdb.Del(ctx, parentsKey).Err(); err != nil {
		log.Printf("[WARN] Failed to delete parents key %s: %v\n", nodeId, err)
	}

	if err := c.rdb.Del(ctx, childrenKey).Err(); err != nil {
		log.Printf("[WARN] Failed to delete children key %s: %v\n", nodeId, err)
	}

	log.Printf("[INFO] ForceDeleteNode %s completed\n", nodeId)

	return nil
}

// GetNodesByType è un alias più leggibile
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
