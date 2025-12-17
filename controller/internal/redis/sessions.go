package redis

import (
	"context"
	"fmt"
	"strings"
)

// SaveSession salva session metadata
func (c *Client) SaveSession(
	ctx context.Context,
	treeId string,
	sessionId string,
	data map[string]any,
) error {
	key := fmt.Sprintf("tree:%s:session:%s", treeId, sessionId)
	return c.rdb.HSet(ctx, key, data).Err()
}

// GetSession legge session metadata
func (c *Client) GetSession(
	ctx context.Context,
	treeId string,
	sessionId string,
) (map[string]string, error) {
	key := fmt.Sprintf("tree:%s:session:%s", treeId, sessionId)
	result, err := c.rdb.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, err
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("session not found: %s", sessionId)
	}

	return result, nil
}

// SessionExists verifica se session esiste
func (c *Client) SessionExists(ctx context.Context, treeId string, sessionId string) (bool, error) {
	key := fmt.Sprintf("tree:%s:session:%s", treeId, sessionId)
	exists, err := c.rdb.Exists(ctx, key).Result()
	return exists > 0, err
}

// DeleteSession rimuove session
func (c *Client) DeleteSession(
	ctx context.Context,
	treeId string,
	sessionId string,
) error {
	key := fmt.Sprintf("tree:%s:session:%s", treeId, sessionId)
	return c.rdb.Del(ctx, key).Err()
}

// AddSessionToTree aggiunge session a tree index
func (c *Client) AddSessionToTree(
	ctx context.Context,
	treeId string,
	sessionId string,
) error {
	key := fmt.Sprintf("tree:%s:sessions", treeId)
	return c.rdb.SAdd(ctx, key, sessionId).Err()
}

// RemoveSessionFromTree rimuove session da tree index
func (c *Client) RemoveSessionFromTree(
	ctx context.Context,
	treeId string,
	sessionId string,
) error {
	key := fmt.Sprintf("tree:%s:sessions", treeId)
	return c.rdb.SRem(ctx, key, sessionId).Err()
}

// GetTreeSessions legge tutte le sessioni di un tree
func (c *Client) GetTreeSessions(
	ctx context.Context,
	treeId string,
) ([]string, error) {
	key := fmt.Sprintf("tree:%s:sessions", treeId)
	return c.rdb.SMembers(ctx, key).Result()
}

// Egress

// AddEgressToSession aggiunge egress a lista session
func (c *Client) AddEgressToSession(
	ctx context.Context,
	treeId string,
	sessionId string,
	egressId string,
) error {
	key := fmt.Sprintf("tree:%s:session:%s:egresses", treeId, sessionId)
	return c.rdb.SAdd(ctx, key, egressId).Err()
}

// GetSessionEgresses legge lista egress per session
func (c *Client) GetSessionEgresses(
	ctx context.Context,
	treeId string,
	sessionId string,
) ([]string, error) {
	key := fmt.Sprintf("tree:%s:session:%s:egresses", treeId, sessionId)
	return c.rdb.SMembers(ctx, key).Result()
}

// RemoveEgressFromSession rimuove egress da session
func (c *Client) RemoveEgressFromSession(
	ctx context.Context,
	treeId string,
	sessionId string,
	egressId string,
) error {
	key := fmt.Sprintf("tree:%s:session:%s:egresses", treeId, sessionId)
	return c.rdb.SRem(ctx, key, egressId).Err()
}

// FindEgressServingSession ritorna egress che servono una sessione
// Usato da ProvisionViewer per riusare egress esistente invece di crearne uno nuovo
func (c *Client) FindEgressServingSession(
	ctx context.Context,
	treeId string,
	sessionId string,
) ([]string, error) {
	// Wrapper semantico per GetSessionEgresses
	return c.GetSessionEgresses(ctx, treeId, sessionId)
}

// SaveSessionPath salva path costruito per coppia (session, egress)
func (c *Client) SaveSessionPath(
	ctx context.Context,
	treeId string,
	sessionId string,
	egressId string,
	path []string,
) error {
	key := fmt.Sprintf("tree:%s:session:%s:path:%s", treeId, sessionId, egressId)
	pathStr := strings.Join(path, ",")
	return c.rdb.Set(ctx, key, pathStr, 0).Err()
}

// GetSessionPath legge path per coppia (session, egress)
func (c *Client) GetSessionPath(
	ctx context.Context,
	treeId string,
	sessionId string,
	egressId string,
) ([]string, error) {
	key := fmt.Sprintf("tree:%s:session:%s:path:%s", treeId, sessionId, egressId)
	pathStr, err := c.rdb.Get(ctx, key).Result()
	if err != nil {
		return nil, err
	}

	if pathStr == "" {
		return []string{}, nil
	}

	return strings.Split(pathStr, ","), nil
}

// GetNextRoomId genera room ID incrementale
func (c *Client) GetNextRoomId(ctx context.Context, treeId string) (int, error) {
	key := fmt.Sprintf("tree:%s:roomCounter", treeId)

	// Controlla se il counter esiste
	exists, err := c.rdb.Exists(ctx, key).Result()
	if err != nil {
		return 0, err
	}

	// Se non esiste, inizializza a 999
	if exists == 0 {
		if err := c.rdb.Set(ctx, key, 999, 0).Err(); err != nil {
			return 0, err
		}
	}

	roomId, err := c.rdb.Incr(ctx, key).Result()
	if err != nil {
		return 0, err
	}
	return int(roomId), nil
}

// GetNextSSRC genera SSRC incrementale
// Range: 10000-999999999
func (c *Client) GetNextSSRC(ctx context.Context, treeId string) (int, error) {
	key := fmt.Sprintf("tree:%s:ssrcCounter", treeId)

	// Controlla se il counter esiste
	exists, err := c.rdb.Exists(ctx, key).Result()
	if err != nil {
		return 0, err
	}

	// Se non esiste, inizializza a 9999
	if exists == 0 {
		if err := c.rdb.Set(ctx, key, 9999, 0).Err(); err != nil {
			return 0, err
		}
	}

	ssrc, err := c.rdb.Incr(ctx, key).Result()
	if err != nil {
		return 0, err
	}
	return int(ssrc), nil
}
