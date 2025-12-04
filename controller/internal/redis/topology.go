package redis

import (
	"context"
	"fmt"
)

// GetNodeParent legge il parent di un nodo
// Chiave: tree:{treeId}:parent:{nodeId}
func (c *Client) GetNodeParent(ctx context.Context, treeId, nodeId string) (string, error) {
	key := fmt.Sprintf("tree:%s:parent:%s", treeId, nodeId)

	parent, err := c.rdb.Get(ctx, key).Result()
	if err != nil {
		if err.Error() == "redis: nil" {
			return "", nil // Nessun parent (es: injection node)
		}
		return "", fmt.Errorf("failed to get parent for %s: %w", nodeId, err)
	}

	return parent, nil
}

// SetNodeParent imposta il parent di un nodo
// Chiave: tree:{treeId}:parent:{nodeId}
func (c *Client) SetNodeParent(ctx context.Context, treeId, childId, parentId string) error {
	key := fmt.Sprintf("tree:%s:parent:%s", treeId, childId)

	if err := c.rdb.Set(ctx, key, parentId, 0).Err(); err != nil {
		return fmt.Errorf("failed to set parent for %s: %w", childId, err)
	}

	return nil
}

// RemoveNodeParent rimuove il parent di un nodo
func (c *Client) RemoveNodeParent(ctx context.Context, treeId, nodeId string) error {
	key := fmt.Sprintf("tree:%s:parent:%s", treeId, nodeId)

	if err := c.rdb.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("failed to remove parent for %s: %w", nodeId, err)
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
	// Rimuovi parent
	parentKey := fmt.Sprintf("tree:%s:parent:%s", treeId, nodeId)
	c.rdb.Del(ctx, parentKey)

	// Rimuovi children
	childrenKey := fmt.Sprintf("tree:%s:children:%s", treeId, nodeId)
	c.rdb.Del(ctx, childrenKey)

	return nil
}

// SetTopology configura parent e child atomicamente
func (c *Client) SetTopology(ctx context.Context, treeId, childId, parentId string) error {
	// Set parent del child
	if err := c.SetNodeParent(ctx, treeId, childId, parentId); err != nil {
		return err
	}

	// Aggiungi child al parent
	if err := c.AddNodeChild(ctx, treeId, parentId, childId); err != nil {
		return err
	}

	return nil
}

// RemoveTopology rimuove relazione parent-child atomicamente
func (c *Client) RemoveTopology(ctx context.Context, treeId, childId, parentId string) error {
	// Rimuovi parent del child
	if err := c.RemoveNodeParent(ctx, treeId, childId); err != nil {
		return err
	}

	// Rimuovi child dal parent
	if err := c.RemoveNodeChild(ctx, treeId, parentId, childId); err != nil {
		return err
	}

	return nil
}

// PublishTopologyEvent pubblica un evento su topology:{treeId}:{nodeId}
// TARGETED - solo quel nodo riceve
func (c *Client) PublishTopologyEvent(ctx context.Context, treeId, nodeId string, event map[string]interface{}) error {
	channel := fmt.Sprintf("topology:%s:%s", treeId, nodeId)
	return c.PublishJSON(ctx, channel, event)
}

// PublishGlobalTopologyEvent pubblica un evento su topology:{treeId}
// BROADCAST - tutti i nodi del tree ricevono
func (c *Client) PublishGlobalTopologyEvent(ctx context.Context, treeId string, event map[string]interface{}) error {
	channel := fmt.Sprintf("topology:%s", treeId)
	return c.PublishJSON(ctx, channel, event)
}

// PublishParentChanged pubblica evento parent-changed
func (c *Client) PublishParentChanged(ctx context.Context, treeId, nodeId, oldParent, newParent string) error {
	event := map[string]interface{}{
		"type":      "parent-changed",
		"nodeId":    nodeId,
		"newParent": newParent,
	}

	if oldParent != "" {
		event["oldParent"] = oldParent
	}

	return c.PublishTopologyEvent(ctx, treeId, nodeId, event)
}

// PublishChildAdded pubblica evento child-added
func (c *Client) PublishChildAdded(ctx context.Context, treeId, parentId, childId string) error {
	event := map[string]interface{}{
		"type":    "child-added",
		"nodeId":  parentId,
		"childId": childId,
	}

	return c.PublishTopologyEvent(ctx, treeId, parentId, event)
}

// PublishChildRemoved pubblica evento child-removed
func (c *Client) PublishChildRemoved(ctx context.Context, treeId, parentId, childId string) error {
	event := map[string]interface{}{
		"type":    "child-removed",
		"nodeId":  parentId,
		"childId": childId,
	}

	return c.PublishTopologyEvent(ctx, treeId, parentId, event)
}

// PublishTopologyReset pubblica evento topology-reset (BROADCAST)
func (c *Client) PublishTopologyReset(ctx context.Context, treeId string) error {
	event := map[string]interface{}{
		"type": "topology-reset",
	}

	return c.PublishGlobalTopologyEvent(ctx, treeId, event)
}
