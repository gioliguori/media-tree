package redis

import (
	"context"
	"encoding/json"
	"fmt"
)

// Route: Dati completi
type Route struct {
	TargetId  string `json:"targetId"`
	Host      string `json:"host"`
	AudioPort int    `json:"audioPort"`
	VideoPort int    `json:"videoPort"`
}

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

// RemoveAllRoutesForSession
func (c *Client) RemoveAllRoutesForSession(
	ctx context.Context,
	treeId string,
	sessionId string,
) error {
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

// RemoveAllRoutesForRelay
func (c *Client) RemoveAllRoutesForRelay(
	ctx context.Context,
	treeId string,
	sessionId string,
	relayId string,
) error {
	key := fmt.Sprintf("tree:%s:routing:%s:%s", treeId, sessionId, relayId)
	return c.rdb.Del(ctx, key).Err()
}

// AddSessionToNode registra session su node (per recovery)
func (c *Client) AddSessionToNode(
	ctx context.Context,
	treeId string,
	nodeId string,
	sessionId string,
) error {
	key := fmt.Sprintf("tree:%s:node:%s:sessions", treeId, nodeId)
	return c.rdb.SAdd(ctx, key, sessionId).Err()
}

// RemoveSessionFromNode rimuove session da node
func (c *Client) RemoveSessionFromNode(
	ctx context.Context,
	treeId string,
	nodeId string,
	sessionId string,
) error {
	key := fmt.Sprintf("tree:%s:node:%s:sessions", treeId, nodeId)
	return c.rdb.SRem(ctx, key, sessionId).Err()
}

// GetNodeSessions legge tutte le sessioni di un node
func (c *Client) GetNodeSessions(
	ctx context.Context,
	treeId string,
	nodeId string,
) ([]string, error) {
	key := fmt.Sprintf("tree:%s:node:%s:sessions", treeId, nodeId)
	return c.rdb.SMembers(ctx, key).Result()
}

// Eventi pub/sub

// PublishSessionEvent
func (c *Client) PublishSessionEvent(
	ctx context.Context,
	channel string,
	event any,
) error {
	eventJSON, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}
	return c.rdb.Publish(ctx, channel, eventJSON).Err()
}

// Eventi specifici

// PublishNodeSessionCreated pubblica evento session-created a un nodo
func (c *Client) PublishNodeSessionCreated(
	ctx context.Context,
	treeId string,
	nodeId string,
	sessionId string,
	audioSsrc int,
	videoSsrc int,
	initialRoutes []Route,
) error {
	channel := fmt.Sprintf("sessions:%s:%s", treeId, nodeId)

	event := map[string]any{
		"type":      "session-created",
		"sessionId": sessionId,
		"treeId":    treeId,
		"audioSsrc": audioSsrc,
		"videoSsrc": videoSsrc,
	}

	// Se ci sono rotte, le includiamo
	if len(initialRoutes) > 0 {
		event["routes"] = initialRoutes
	}

	return c.PublishSessionEvent(ctx, channel, event)
}

// PublishRouteAdded
func (c *Client) PublishRouteAdded(
	ctx context.Context,
	treeId string,
	nodeId string,
	sessionId string,
	targetId string,
) error {
	channel := fmt.Sprintf("sessions:%s:%s", treeId, nodeId)

	event := map[string]any{
		"type":      "route-added",
		"sessionId": sessionId,
		"treeId":    treeId,
		"targetId":  targetId,
	}

	return c.PublishSessionEvent(ctx, channel, event)
}

// PublishRouteRemoved notifica rimozione rotta
func (c *Client) PublishRouteRemoved(
	ctx context.Context,
	treeId string,
	nodeId string,
	sessionId string,
	targetId string,
) error {
	channel := fmt.Sprintf("sessions:%s:%s", treeId, nodeId)

	event := map[string]any{
		"type":      "route-removed",
		"sessionId": sessionId,
		"treeId":    treeId,
		"targetId":  targetId,
	}

	return c.PublishSessionEvent(ctx, channel, event)
}

// PublishNodeSessionDestroyed notifica distruzione sessione al nodo
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

// PublishSessionDestroyed (tutto l'albero)
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
