package redis

import (
	"context"
	"fmt"
	"log"
	"strings"
)

// GetNodeChildren legge i children di un nodo
// Chiave: tree:{treeId}:children:{nodeId}
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

// ADD

// AddNodeChild aggiunge un child a un nodo e pubblica evento
// Chiave: tree:{treeId}:children:{nodeId}
func (c *Client) AddNodeChild(ctx context.Context, treeId, parentId, childId string) error {
	key := fmt.Sprintf("tree:%s:children:%s", treeId, parentId)

	if err := c.rdb.SAdd(ctx, key, childId).Err(); err != nil {
		return fmt.Errorf("failed to add child %s to %s: %w", childId, parentId, err)
	}

	// Pubblica evento child-added
	if err := c.PublishChildAdded(ctx, treeId, parentId, childId); err != nil {
		log.Printf("[WARN] Failed to publish child-added event: %v", err)
	}

	log.Printf("[Redis] Added child %s to %s (tree %s)", childId, parentId, treeId)
	return nil
}

// AddNodeParent aggiunge un parent a un nodo e pubblica evento
// Chiave: tree:{treeId}:parents:{nodeId}
func (c *Client) AddNodeParent(ctx context.Context, treeId, childId, parentId string) error {
	key := fmt.Sprintf("tree:%s:parents:%s", treeId, childId)

	if err := c.rdb.SAdd(ctx, key, parentId).Err(); err != nil {
		return fmt.Errorf("failed to add parent %s to %s: %w", parentId, childId, err)
	}

	// Pubblica evento parent-added
	if err := c.PublishParentAdded(ctx, treeId, childId, parentId); err != nil {
		log.Printf("[WARN] Failed to publish parent-added event: %v", err)
	}

	log.Printf("[Redis] Added parent %s to %s (tree %s)", parentId, childId, treeId)
	return nil
}

// REMOVE

// RemoveNodeChild rimuove un child da un nodo E pubblica evento
func (c *Client) RemoveNodeChild(ctx context.Context, treeId, parentId, childId string) error {
	key := fmt.Sprintf("tree:%s:children:%s", treeId, parentId)

	if err := c.rdb.SRem(ctx, key, childId).Err(); err != nil {
		return fmt.Errorf("failed to remove child %s from %s: %w", childId, parentId, err)
	}

	// Pubblica evento child-removed
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

	// Pubblica evento parent-removed
	if err := c.PublishParentRemoved(ctx, treeId, nodeId, parentId); err != nil {
		log.Printf("[WARN] Failed to publish parent-removed event: %v", err)
	}

	log.Printf("[Redis] Removed parent %s from %s (tree %s)", parentId, nodeId, treeId)
	return nil
}

// RemoveAllNodeParents rimuove tutti i parent di un nodo e pubblica eventi
func (c *Client) RemoveAllNodeParents(ctx context.Context, treeId, nodeId string) error {
	// Leggi parents esistenti
	parents, err := c.GetNodeParents(ctx, treeId, nodeId)
	if err != nil {
		return fmt.Errorf("failed to get parents: %w", err)
	}

	// Pubblica evento per ogni parent rimosso
	for _, parentId := range parents {
		if err := c.PublishParentRemoved(ctx, treeId, nodeId, parentId); err != nil {
			log.Printf("[WARN] Failed to publish parent-removed event: %v", err)
		}
	}

	// Rimuovi la key Redis
	key := fmt.Sprintf("tree:%s:parents:%s", treeId, nodeId)
	if err := c.rdb.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("failed to remove all parents for %s: %w", nodeId, err)
	}

	log.Printf("[Redis] Removed all %d parents from %s (tree %s)", len(parents), nodeId, treeId)
	return nil
}

// EVENTI PUB/SUB
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

// Utility
// GetAllTrees ritorna lista di tutti i tree
func (c *Client) GetAllTrees(ctx context.Context) ([]string, error) {
	pattern := "tree:*:metadata"
	keys, err := c.Keys(ctx, pattern)
	if err != nil {
		return nil, fmt.Errorf("failed to get trees: %w", err)
	}

	trees := make([]string, 0, len(keys))
	for _, key := range keys {
		// Estrai treeId da "tree:{treeId}:metadata"
		parts := strings.Split(key, ":")
		if len(parts) >= 2 {
			trees = append(trees, parts[1])
		}
	}

	return trees, nil
}
