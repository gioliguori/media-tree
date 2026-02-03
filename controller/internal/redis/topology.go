package redis

import (
	"context"
	"fmt"
	"log"
)

// GetNodeChildren legge i children di un nodo
func (c *Client) GetNodeChildren(ctx context.Context, nodeId string) ([]string, error) {
	key := fmt.Sprintf("node:%s:children", nodeId)

	children, err := c.rdb.SMembers(ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get children for %s: %w", nodeId, err)
	}

	return children, nil
}

// GetNodeParents legge i parents di un nodo
func (c *Client) GetNodeParents(ctx context.Context, nodeId string) ([]string, error) {
	key := fmt.Sprintf("node:%s:parents", nodeId)

	parents, err := c.rdb.SMembers(ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get parents for %s: %w", nodeId, err)
	}

	return parents, nil
}

// ADD

// AddNodeChild aggiunge un child a un nodo e pubblica evento
func (c *Client) AddNodeChild(ctx context.Context, parentId, childId string) error {
	key := fmt.Sprintf("node:%s:children", parentId)

	if err := c.rdb.SAdd(ctx, key, childId).Err(); err != nil {
		return fmt.Errorf("failed to add child %s to %s: %w", childId, parentId, err)
	}

	// Pubblica evento child-added
	if err := c.PublishChildAdded(ctx, parentId, childId); err != nil {
		log.Printf("[WARN] Failed to publish child-added event: %v", err)
	}

	log.Printf("[Redis] Added child %s to %s", childId, parentId)
	return nil
}

// AddNodeParent aggiunge un parent a un nodo e pubblica evento
func (c *Client) AddNodeParent(ctx context.Context, childId, parentId string) error {
	key := fmt.Sprintf("node:%s:parents", childId)

	if err := c.rdb.SAdd(ctx, key, parentId).Err(); err != nil {
		return fmt.Errorf("failed to add parent %s to %s: %w", parentId, childId, err)
	}

	// Pubblica evento parent-added
	if err := c.PublishParentAdded(ctx, childId, parentId); err != nil {
		log.Printf("[WARN] Failed to publish parent-added event: %v", err)
	}

	log.Printf("[Redis] Added parent %s to %s", parentId, childId)
	return nil
}

// REMOVE

// RemoveNodeChild rimuove un child da un nodo E pubblica evento
func (c *Client) RemoveNodeChild(ctx context.Context, parentId, childId string) error {
	key := fmt.Sprintf("node:%s:children", parentId)

	if err := c.rdb.SRem(ctx, key, childId).Err(); err != nil {
		return fmt.Errorf("failed to remove child %s from %s: %w", childId, parentId, err)
	}

	// Pubblica evento child-removed
	if err := c.PublishChildRemoved(ctx, parentId, childId); err != nil {
		log.Printf("[WARN] Failed to publish child-removed event: %v", err)
	}

	log.Printf("[Redis] Removed child %s from %s", childId, parentId)
	return nil
}

// RemoveNodeParent rimuove un parent da un nodo E pubblica evento
func (c *Client) RemoveNodeParent(ctx context.Context, nodeId, parentId string) error {
	key := fmt.Sprintf("node:%s:parents", nodeId)

	if err := c.rdb.SRem(ctx, key, parentId).Err(); err != nil {
		return fmt.Errorf("failed to remove parent %s from %s: %w", parentId, nodeId, err)
	}

	// Pubblica evento parent-removed
	if err := c.PublishParentRemoved(ctx, nodeId, parentId); err != nil {
		log.Printf("[WARN] Failed to publish parent-removed event: %v", err)
	}

	log.Printf("[Redis] Removed parent %s from %s", parentId, nodeId)
	return nil
}

// RemoveAllNodeParents rimuove tutti i parent di un nodo e pubblica eventi
func (c *Client) RemoveAllNodeParents(ctx context.Context, nodeId string) error {
	// Leggi parents esistenti
	parents, err := c.GetNodeParents(ctx, nodeId)
	if err != nil {
		return fmt.Errorf("failed to get parents: %w", err)
	}

	// Pubblica evento per ogni parent rimosso
	for _, parentId := range parents {
		if err := c.PublishParentRemoved(ctx, nodeId, parentId); err != nil {
			log.Printf("[WARN] Failed to publish parent-removed event: %v", err)
		}
	}

	// Rimuovi la key Redis
	key := fmt.Sprintf("node:%s:parents", nodeId)
	if err := c.rdb.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("failed to remove all parents for %s: %w", nodeId, err)
	}

	log.Printf("[Redis] Removed all %d parents from %s", len(parents), nodeId)
	return nil
}

// EVENTI PUB/SUB
func (c *Client) PublishTopologyEvent(ctx context.Context, nodeId string, event map[string]any) error {
	channel := fmt.Sprintf("node:%s:topology", nodeId)
	return c.PublishJSON(ctx, channel, event)
}

func (c *Client) PublishGlobalTopologyEvent(ctx context.Context, event map[string]any) error {
	channel := "topology:global"
	return c.PublishJSON(ctx, channel, event)
}

// PublishParentAdded pubblica evento parent-added
func (c *Client) PublishParentAdded(ctx context.Context, nodeId, parentId string) error {
	event := map[string]any{
		"type":     "parent-added",
		"nodeId":   nodeId,
		"parentId": parentId,
	}

	return c.PublishTopologyEvent(ctx, nodeId, event)
}

// PublishParentRemoved pubblica evento parent-removed
func (c *Client) PublishParentRemoved(ctx context.Context, nodeId, parentId string) error {
	event := map[string]any{
		"type":     "parent-removed",
		"nodeId":   nodeId,
		"parentId": parentId,
	}

	return c.PublishTopologyEvent(ctx, nodeId, event)
}

// PublishChildAdded pubblica evento child-added
func (c *Client) PublishChildAdded(ctx context.Context, parentId, childId string) error {
	event := map[string]any{
		"type":    "child-added",
		"nodeId":  parentId,
		"childId": childId,
	}

	return c.PublishTopologyEvent(ctx, parentId, event)
}

// PublishChildRemoved pubblica evento child-removed
func (c *Client) PublishChildRemoved(ctx context.Context, parentId, childId string) error {
	event := map[string]any{
		"type":    "child-removed",
		"nodeId":  parentId,
		"childId": childId,
	}

	return c.PublishTopologyEvent(ctx, parentId, event)
}

// PublishTopologyReset pubblica evento topology-reset
func (c *Client) PublishTopologyReset(ctx context.Context) error {
	event := map[string]any{
		"type": "topology-reset",
	}

	return c.PublishGlobalTopologyEvent(ctx, event)
}
