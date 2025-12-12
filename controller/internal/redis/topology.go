package redis

import (
	"context"
	"fmt"
	"log"
	"strings"
)

// ============================================
// TOPOLOGIA - CHILDREN & PARENTS
// ============================================

// GetNodeChildren legge i children di un nodo
// Chiave: tree:{treeId}:children:{nodeId}
// USATO SOLO per Injection → RelayRoot (compatibilità con InjectionNode.js)
func (c *Client) GetNodeChildren(ctx context.Context, treeId, nodeId string) ([]string, error) {
	key := fmt.Sprintf("tree:%s:children:%s", treeId, nodeId)

	children, err := c.rdb.SMembers(ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get children for %s: %w", nodeId, err)
	}

	return children, nil
}

// GetNodeParents legge i parents di un nodo
// Chiave: tree:{treeId}:parents:{nodeId}
func (c *Client) GetNodeParents(ctx context.Context, treeId, nodeId string) ([]string, error) {
	key := fmt.Sprintf("tree:%s:parents:%s", treeId, nodeId)

	parents, err := c.rdb.SMembers(ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get parents for %s: %w", nodeId, err)
	}

	return parents, nil
}

// ============================================
// ADD - CON EVENTI
// ============================================

// AddNodeChild aggiunge un child a un nodo E pubblica evento
// Chiave: tree:{treeId}:children:{nodeId}
// USATO SOLO per linkare Injection → RelayRoot
func (c *Client) AddNodeChild(ctx context.Context, treeId, parentId, childId string) error {
	key := fmt.Sprintf("tree:%s:children:%s", treeId, parentId)

	if err := c.rdb.SAdd(ctx, key, childId).Err(); err != nil {
		return fmt.Errorf("failed to add child %s to %s: %w", childId, parentId, err)
	}

	// Pubblica evento child-added automaticamente
	if err := c.PublishChildAdded(ctx, treeId, parentId, childId); err != nil {
		log.Printf("[WARN] Failed to publish child-added event: %v", err)
		// Non bloccare se evento fallisce
	}

	log.Printf("[Redis] Added child %s to %s (tree %s)", childId, parentId, treeId)
	return nil
}

// AddNodeParent aggiunge un parent a un nodo E pubblica evento
// Chiave: tree:{treeId}:parents:{nodeId}
func (c *Client) AddNodeParent(ctx context.Context, treeId, childId, parentId string) error {
	key := fmt.Sprintf("tree:%s:parents:%s", treeId, childId)

	if err := c.rdb.SAdd(ctx, key, parentId).Err(); err != nil {
		return fmt.Errorf("failed to add parent %s to %s: %w", parentId, childId, err)
	}

	// Pubblica evento parent-added automaticamente
	if err := c.PublishParentAdded(ctx, treeId, childId, parentId); err != nil {
		log.Printf("[WARN] Failed to publish parent-added event: %v", err)
		// Non bloccare se evento fallisce
	}

	log.Printf("[Redis] Added parent %s to %s (tree %s)", parentId, childId, treeId)
	return nil
}

// ============================================
// REMOVE - CON EVENTI
// ============================================

// RemoveNodeChild rimuove un child da un nodo E pubblica evento
func (c *Client) RemoveNodeChild(ctx context.Context, treeId, parentId, childId string) error {
	key := fmt.Sprintf("tree:%s:children:%s", treeId, parentId)

	if err := c.rdb.SRem(ctx, key, childId).Err(); err != nil {
		return fmt.Errorf("failed to remove child %s from %s: %w", childId, parentId, err)
	}

	// Pubblica evento child-removed automaticamente
	if err := c.PublishChildRemoved(ctx, treeId, parentId, childId); err != nil {
		log.Printf("[WARN] Failed to publish child-removed event: %v", err)
	}

	log.Printf("[Redis] Removed child %s from %s (tree %s)", childId, parentId, treeId)
	return nil
}

// RemoveNodeParent rimuove un parent da un nodo E pubblica evento
func (c *Client) RemoveNodeParent(ctx context.Context, treeId, nodeId, parentId string) error {
	key := fmt.Sprintf("tree:%s:parents:%s", treeId, nodeId)

	if err := c.rdb.SRem(ctx, key, parentId).Err(); err != nil {
		return fmt.Errorf("failed to remove parent %s from %s: %w", parentId, nodeId, err)
	}

	// Pubblica evento parent-removed automaticamente
	if err := c.PublishParentRemoved(ctx, treeId, nodeId, parentId); err != nil {
		log.Printf("[WARN] Failed to publish parent-removed event: %v", err)
	}

	log.Printf("[Redis] Removed parent %s from %s (tree %s)", parentId, nodeId, treeId)
	return nil
}

// RemoveAllNodeParents rimuove tutti i parent di un nodo E pubblica eventi
func (c *Client) RemoveAllNodeParents(ctx context.Context, treeId, nodeId string) error {
	// 1. Leggi parents esistenti (per pubblicare eventi)
	parents, err := c.GetNodeParents(ctx, treeId, nodeId)
	if err != nil {
		return fmt.Errorf("failed to get parents: %w", err)
	}

	// 2. Pubblica evento per ogni parent rimosso
	for _, parentId := range parents {
		if err := c.PublishParentRemoved(ctx, treeId, nodeId, parentId); err != nil {
			log.Printf("[WARN] Failed to publish parent-removed event: %v", err)
		}
	}

	// 3. Rimuovi la key Redis
	key := fmt.Sprintf("tree:%s:parents:%s", treeId, nodeId)
	if err := c.rdb.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("failed to remove all parents for %s: %w", nodeId, err)
	}

	log.Printf("[Redis] Removed all %d parents from %s (tree %s)", len(parents), nodeId, treeId)
	return nil
}

// ============================================
// BATCH OPERATIONS - CON EVENTI
// ============================================

// SetTopology aggiunge relazione parent-child atomicamente E pubblica eventi
// DEPRECATO: Usa AddNodeChild + AddNodeParent invece (più esplicito)
func (c *Client) SetTopology(ctx context.Context, treeId, childId, parentId string) error {
	pipe := c.rdb.TxPipeline()

	// Add Parent
	keyParents := fmt.Sprintf("tree:%s:parents:%s", treeId, childId)
	pipe.SAdd(ctx, keyParents, parentId)

	// Add Child
	keyChildren := fmt.Sprintf("tree:%s:children:%s", treeId, parentId)
	pipe.SAdd(ctx, keyChildren, childId)

	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("failed to set topology atomically: %w", err)
	}

	// Pubblica eventi DOPO commit atomico
	if err := c.PublishParentAdded(ctx, treeId, childId, parentId); err != nil {
		log.Printf("[WARN] Failed to publish parent-added event: %v", err)
	}
	if err := c.PublishChildAdded(ctx, treeId, parentId, childId); err != nil {
		log.Printf("[WARN] Failed to publish child-added event: %v", err)
	}

	log.Printf("[Redis] Set topology: %s <-> %s (tree %s)", parentId, childId, treeId)
	return nil
}

// RemoveTopology rimuove relazione parent-child atomicamente E pubblica eventi
// DEPRECATO: Usa RemoveNodeChild + RemoveNodeParent invece
func (c *Client) RemoveTopology(ctx context.Context, treeId, childId, parentId string) error {
	pipe := c.rdb.TxPipeline()

	keyParents := fmt.Sprintf("tree:%s:parents:%s", treeId, childId)
	pipe.SRem(ctx, keyParents, parentId)

	keyChildren := fmt.Sprintf("tree:%s:children:%s", treeId, parentId)
	pipe.SRem(ctx, keyChildren, childId)

	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("failed to remove topology atomically: %w", err)
	}

	// Pubblica eventi DOPO commit atomico
	if err := c.PublishParentRemoved(ctx, treeId, childId, parentId); err != nil {
		log.Printf("[WARN] Failed to publish parent-removed event: %v", err)
	}
	if err := c.PublishChildRemoved(ctx, treeId, parentId, childId); err != nil {
		log.Printf("[WARN] Failed to publish child-removed event: %v", err)
	}

	log.Printf("[Redis] Removed topology: %s <-> %s (tree %s)", parentId, childId, treeId)
	return nil
}

// DeleteNodeTopology rimuove tutta la topologia di un nodo E pubblica eventi
func (c *Client) DeleteNodeTopology(ctx context.Context, treeId, nodeId string) error {
	// 1. Leggi parents (per eventi)
	parents, _ := c.GetNodeParents(ctx, treeId, nodeId)
	for _, parentId := range parents {
		// Rimuovi da parent.children
		c.RemoveNodeChild(ctx, treeId, parentId, nodeId)
		// Evento pubblicato dentro RemoveNodeChild
	}

	// 2. Leggi children (per eventi)
	children, _ := c.GetNodeChildren(ctx, treeId, nodeId)
	for _, childId := range children {
		// Rimuovi da child.parents
		c.RemoveNodeParent(ctx, treeId, childId, nodeId)
		// Evento pubblicato dentro RemoveNodeParent
	}

	// 3. Rimuovi chiavi Redis
	parentsKey := fmt.Sprintf("tree:%s:parents:%s", treeId, nodeId)
	c.rdb.Del(ctx, parentsKey)

	childrenKey := fmt.Sprintf("tree:%s:children:%s", treeId, nodeId)
	c.rdb.Del(ctx, childrenKey)

	log.Printf("[Redis] Deleted topology for node %s (tree %s)", nodeId, treeId)
	return nil
}

// ============================================
// EVENTI PUB/SUB
// ============================================

// PublishTopologyEvent pubblica un evento su topology:{treeId}:{nodeId}
func (c *Client) PublishTopologyEvent(ctx context.Context, treeId, nodeId string, event map[string]any) error {
	channel := fmt.Sprintf("topology:%s:%s", treeId, nodeId)
	return c.PublishJSON(ctx, channel, event)
}

// PublishGlobalTopologyEvent pubblica un evento su topology:{treeId}
func (c *Client) PublishGlobalTopologyEvent(ctx context.Context, treeId string, event map[string]any) error {
	channel := fmt.Sprintf("topology:%s", treeId)
	return c.PublishJSON(ctx, channel, event)
}

// PublishParentAdded pubblica evento parent-added
func (c *Client) PublishParentAdded(ctx context.Context, treeId, nodeId, parentId string) error {
	event := map[string]any{
		"type":     "parent-added",
		"nodeId":   nodeId,
		"parentId": parentId,
	}

	return c.PublishTopologyEvent(ctx, treeId, nodeId, event)
}

// PublishParentRemoved pubblica evento parent-removed
func (c *Client) PublishParentRemoved(ctx context.Context, treeId, nodeId, parentId string) error {
	event := map[string]any{
		"type":     "parent-removed",
		"nodeId":   nodeId,
		"parentId": parentId,
	}

	return c.PublishTopologyEvent(ctx, treeId, nodeId, event)
}

// PublishChildAdded pubblica evento child-added
func (c *Client) PublishChildAdded(ctx context.Context, treeId, parentId, childId string) error {
	event := map[string]any{
		"type":    "child-added",
		"nodeId":  parentId,
		"childId": childId,
	}

	return c.PublishTopologyEvent(ctx, treeId, parentId, event)
}

// PublishChildRemoved pubblica evento child-removed
func (c *Client) PublishChildRemoved(ctx context.Context, treeId, parentId, childId string) error {
	event := map[string]any{
		"type":    "child-removed",
		"nodeId":  parentId,
		"childId": childId,
	}

	return c.PublishTopologyEvent(ctx, treeId, parentId, event)
}

// PublishTopologyReset pubblica evento topology-reset
func (c *Client) PublishTopologyReset(ctx context.Context, treeId string) error {
	event := map[string]any{
		"type": "topology-reset",
	}

	return c.PublishGlobalTopologyEvent(ctx, treeId, event)
}

// ============================================
// UTILITY
// ============================================

// GetAllTrees ritorna lista di tutti i tree
func (c *Client) GetAllTrees(ctx context.Context) ([]string, error) {
	pattern := "tree:*:metadata"
	keys, err := c.Keys(ctx, pattern)
	if err != nil {
		return nil, fmt.Errorf("failed to get trees: %w", err)
	}

	trees := make([]string, 0, len(keys))
	for _, key := range keys {
		// Estrai tree_id da "tree:{treeId}:metadata"
		parts := strings.Split(key, ":")
		if len(parts) >= 2 {
			trees = append(trees, parts[1])
		}
	}

	return trees, nil
}
