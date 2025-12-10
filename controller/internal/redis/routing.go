package redis

import (
	"context"
	"encoding/json"
	"fmt"
)

// AddRoute aggiunge route per relay
func (c *Client) AddRoute(
	ctx context.Context,
	treeId string,
	sessionId string,
	relayId string,
	targetId string,
) error {
	key := fmt.Sprintf("tree:%s:routing:%s:%s", treeId, sessionId, relayId)
	return c.rdb.SAdd(ctx, key, targetId).Err()
}

// GetRoutes legge routes per relay
func (c *Client) GetRoutes(
	ctx context.Context,
	treeId string,
	sessionId string,
	relayId string,
) ([]string, error) {
	key := fmt.Sprintf("tree:%s:routing:%s:%s", treeId, sessionId, relayId)
	return c.rdb.SMembers(ctx, key).Result()
}

// RemoveRoute rimuove route
func (c *Client) RemoveRoute(
	ctx context.Context,
	treeId string,
	sessionId string,
	relayId string,
	targetId string,
) error {
	key := fmt.Sprintf("tree:%s:routing:%s:%s", treeId, sessionId, relayId)
	return c.rdb.SRem(ctx, key, targetId).Err()
}

// RemoveAllRoutesForSession rimuove tutte le route per una session
func (c *Client) RemoveAllRoutesForSession(
	ctx context.Context,
	treeId string,
	sessionId string,
) error {
	// Pattern: tree:{treeId}:routing:{sessionId}:*
	pattern := fmt.Sprintf("tree:%s:routing:%s:*", treeId, sessionId)
	keys, err := c.Keys(ctx, pattern)
	if err != nil {
		return err
	}

	if len(keys) == 0 {
		return nil
	}

	for _, key := range keys {
		if err := c.rdb.Del(ctx, key).Err(); err != nil {
			return err
		}
	}

	return nil
}

// AddSessionToNode registra session su node (per recovery)
func (c *Client) AddSessionToNode(
	ctx context.Context,
	treeId string,
	nodeId string,
	sessionId string,
) error {
	key := fmt.Sprintf("tree:%s:sessions:node:%s", treeId, nodeId)
	return c.rdb.SAdd(ctx, key, sessionId).Err()
}

// RemoveSessionFromNode rimuove session da node
func (c *Client) RemoveSessionFromNode(
	ctx context.Context,
	treeId string,
	nodeId string,
	sessionId string,
) error {
	key := fmt.Sprintf("tree:%s:sessions:node:%s", treeId, nodeId)
	return c.rdb.SRem(ctx, key, sessionId).Err()
}

// GetNodeSessions legge tutte le sessioni di un node
func (c *Client) GetNodeSessions(
	ctx context.Context,
	treeId string,
	nodeId string,
) ([]string, error) {
	key := fmt.Sprintf("tree:%s:sessions:node:%s", treeId, nodeId)
	return c.rdb.SMembers(ctx, key).Result()
}

// PublishSessionEvent pubblica evento session su canale Redis
func (c *Client) PublishSessionEvent(
	ctx context.Context,
	channel string,
	event any,
) error {
	// Serializza evento in JSON
	eventJSON, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	// Pubblica su canale Redis
	if err := c.rdb.Publish(ctx, channel, eventJSON).Err(); err != nil {
		return fmt.Errorf("failed to publish event: %w", err)
	}

	return nil
}

// PublishNodeSessionCreated pubblica evento session-created a un nodo
func (c *Client) PublishNodeSessionCreated(
	ctx context.Context,
	treeId string,
	nodeId string,
	event any,
) error {
	channel := fmt.Sprintf("sessions:%s:%s", treeId, nodeId)
	return c.PublishSessionEvent(ctx, channel, event)
}

// PublishSessionDestroyed pubblica evento session-destroyed a un nodo
func (c *Client) PublishNodeSessionDestroyed(
	ctx context.Context,
	treeId string,
	nodeId string,
	sessionId string,
) error {
	channel := fmt.Sprintf("sessions:%s:%s", treeId, nodeId)
	event := map[string]any{
		"type":      "session-destroyed",
		"sessionId": sessionId,
		"treeId":    treeId,
	}
	return c.PublishSessionEvent(ctx, channel, event)
}

// PublishSessionDestroyed pubblica evento session-destroyed broadcast
func (c *Client) PublishSessionDestroyed(
	ctx context.Context,
	treeId string,
	sessionId string,
) error {
	channel := fmt.Sprintf("sessions:%s", treeId)
	event := map[string]any{
		"type":      "session-destroyed",
		"sessionId": sessionId,
		"treeId":    treeId,
	}
	return c.PublishSessionEvent(ctx, channel, event)
}
