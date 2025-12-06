package redis

import (
	"context"
	"fmt"
)

// GetNodeParent legge i parents di un nodo
// Chiave: tree:{treeId}:parents:{nodeId}
func (c *Client) GetNodeParents(ctx context.Context, treeId, nodeId string) ([]string, error) {
	key := fmt.Sprintf("tree:%s:parents:%s", treeId, nodeId)

	parents, err := c.rdb.SMembers(ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get parents for %s: %w", nodeId, err)
	}

	return parents, nil
}

// AddNodeParent: Aggiunge un genitore alla lista
// Chiave: tree:{treeId}:parents:{nodeId}
func (c *Client) AddNodeParent(ctx context.Context, treeId, childId, parentId string) error {
	key := fmt.Sprintf("tree:%s:parents:%s", treeId, childId)

	if err := c.rdb.SAdd(ctx, key, parentId).Err(); err != nil {
		return fmt.Errorf("failed to add parent %s to %s: %w", parentId, childId, err)
	}

	return nil
}

// RemoveNodeParent rimuove il parent di un nodo
func (c *Client) RemoveNodeParent(ctx context.Context, treeId, nodeId, parentId string) error {
	key := fmt.Sprintf("tree:%s:parents:%s", treeId, nodeId)

	if err := c.rdb.SRem(ctx, key, parentId).Err(); err != nil {
		return fmt.Errorf("failed to remove parent %s from %s: %w", parentId, nodeId, err)
	}

	return nil
}

// rimuove tutti i parents
func (c *Client) RemoveAllNodeParents(ctx context.Context, treeId, nodeId string) error {
	key := fmt.Sprintf("tree:%s:parents:%s", treeId, nodeId)

	if err := c.rdb.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("failed to remove all parents for %s: %w", nodeId, err)
	}

	return nil
}

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

// AddNodeChild aggiunge un child a un nodo
// Chiave: tree:{treeId}:children:{nodeId}
func (c *Client) AddNodeChild(ctx context.Context, treeId, parentId, childId string) error {
	key := fmt.Sprintf("tree:%s:children:%s", treeId, parentId)

	if err := c.rdb.SAdd(ctx, key, childId).Err(); err != nil {
		return fmt.Errorf("failed to add child %s to %s: %w", childId, parentId, err)
	}

	return nil
}

// RemoveNodeChild rimuove un child da un nodo
// Chiave: tree:{treeId}:children:{nodeId}
func (c *Client) RemoveNodeChild(ctx context.Context, treeId, parentId, childId string) error {
	key := fmt.Sprintf("tree:%s:children:%s", treeId, parentId)

	if err := c.rdb.SRem(ctx, key, childId).Err(); err != nil {
		return fmt.Errorf("failed to remove child %s from %s: %w", childId, parentId, err)
	}

	return nil
}

// DeleteNodeTopology rimuove tutta la topologia di un nodo (parent + children)
func (c *Client) DeleteNodeTopology(ctx context.Context, treeId, nodeId string) error {
	// Rimuovi parents
	parentsKey := fmt.Sprintf("tree:%s:parents:%s", treeId, nodeId)
	c.rdb.Del(ctx, parentsKey)

	// Rimuovi children
	childrenKey := fmt.Sprintf("tree:%s:children:%s", treeId, nodeId)
	c.rdb.Del(ctx, childrenKey)

	return nil
}

// SetTopology configura parent e child atomicamente (ora transazione)
func (c *Client) SetTopology(ctx context.Context, treeId, childId, parentId string) error {
	pipe := c.rdb.TxPipeline()

	// Add Parent
	keyParents := fmt.Sprintf("tree:%s:parents:%s", treeId, childId)
	pipe.SAdd(ctx, keyParents, parentId)

	// Add Child
	keyChildren := fmt.Sprintf("tree:%s:children:%s", treeId, parentId)
	pipe.SAdd(ctx, keyChildren, childId)

	// Esegui tutto insieme
	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("failed to set topology atomically: %w", err)
	}

	return nil
}

// RemoveTopology rimuove relazione parent-child atomicamente
func (c *Client) RemoveTopology(ctx context.Context, treeId, childId, parentId string) error {
	pipe := c.rdb.TxPipeline()

	// Remove Parent
	keyParents := fmt.Sprintf("tree:%s:parents:%s", treeId, childId)
	pipe.SRem(ctx, keyParents, parentId)

	// Remove Child
	keyChildren := fmt.Sprintf("tree:%s:children:%s", treeId, parentId)
	pipe.SRem(ctx, keyChildren, childId)

	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("failed to remove topology atomically: %w", err)
	}

	return nil
}

// --- EVENTI PUB/SUB ---
// Qui sotto gestiamo i messaggi che arrivano ai nodi Node.js per avvisarli dei cambi.

// PublishTopologyEvent pubblica un evento su topology:{treeId}:{nodeId}
func (c *Client) PublishTopologyEvent(ctx context.Context, treeId, nodeId string, event map[string]any) error {
	channel := fmt.Sprintf("topology:%s:%s", treeId, nodeId)
	return c.PublishJSON(ctx, channel, event)
}

// PublishGlobalTopologyEvent pubblica un evento su topology:{treeId}
// BROADCAST - tutti i nodi del tree ricevono
func (c *Client) PublishGlobalTopologyEvent(ctx context.Context, treeId string, event map[string]any) error {
	channel := fmt.Sprintf("topology:%s", treeId)
	return c.PublishJSON(ctx, channel, event)
}

func (c *Client) PublishParentAdded(ctx context.Context, treeId, nodeId, parentId string) error {
	event := map[string]any{
		"type":     "parent-added",
		"nodeId":   nodeId,
		"parentId": parentId,
	}

	return c.PublishTopologyEvent(ctx, treeId, nodeId, event)
}

func (c *Client) PublishParentRemoved(ctx context.Context, treeId, nodeId, parentId string) error {
	event := map[string]any{
		"type":     "parent-removed",
		"nodeId":   nodeId,
		"parentId": parentId,
	}

	return c.PublishTopologyEvent(ctx, treeId, nodeId, event)
}

func (c *Client) PublishChildAdded(ctx context.Context, treeId, parentId, childId string) error {
	event := map[string]any{
		"type":    "child-added",
		"nodeId":  parentId,
		"childId": childId,
	}

	return c.PublishTopologyEvent(ctx, treeId, parentId, event)
}

func (c *Client) PublishChildRemoved(ctx context.Context, treeId, parentId, childId string) error {
	event := map[string]any{
		"type":    "child-removed",
		"nodeId":  parentId,
		"childId": childId,
	}

	return c.PublishTopologyEvent(ctx, treeId, parentId, event)
}

func (c *Client) PublishTopologyReset(ctx context.Context, treeId string) error {
	event := map[string]any{
		"type": "topology-reset",
	}

	return c.PublishGlobalTopologyEvent(ctx, treeId, event)
}
