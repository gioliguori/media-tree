package redis

import (
	"context"
	"fmt"
	"strconv"
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

// SaveSessionEgressInfo salva info per egress specifico
func (c *Client) SaveSessionEgressInfo(
	ctx context.Context,
	treeId string,
	sessionId string,
	egressId string,
	path string,
	whepEndpoint string,
) error {
	key := fmt.Sprintf("tree:%s:session:%s:egress:%s", treeId, sessionId, egressId)
	return c.rdb.HSet(ctx, key,
		"path", path,
		"whep_endpoint", whepEndpoint,
	).Err()
}

// GetSessionEgressInfo legge info egress
func (c *Client) GetSessionEgressInfo(
	ctx context.Context,
	treeId string,
	sessionId string,
	egressId string,
) (map[string]string, error) {
	key := fmt.Sprintf("tree:%s:session:%s:egress:%s", treeId, sessionId, egressId)
	return c.rdb.HGetAll(ctx, key).Result()
}

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

// GetNextRoomId genera room ID incrementale
func (c *Client) GetNextRoomId(ctx context.Context, treeId string) (int, error) {
	key := fmt.Sprintf("tree:%s:room_counter", treeId)

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

func (c *Client) DeleteMountpoint(
	ctx context.Context,
	treeId string,
	nodeId string,
	sessionId string,
) error {
	// Cancella hash mountpoint
	key := fmt.Sprintf("tree:%s:mountpoint:%s:%s", treeId, nodeId, sessionId)
	if err := c.rdb.Del(ctx, key).Err(); err != nil {
		return err
	}

	// Rimuovi da SET di indice
	if err := c.rdb.SRem(ctx, fmt.Sprintf("tree:%s:mountpoints", treeId), fmt.Sprintf("%s:%s", nodeId, sessionId)).Err(); err != nil {
		return err
	}

	if err := c.rdb.SRem(ctx, fmt.Sprintf("tree:%s:mountpoints:node:%s", treeId, nodeId), sessionId).Err(); err != nil {
		return err
	}

	return nil
}

// GetNextSSRC genera SSRC incrementale
// Range: 10000-999999999
func (c *Client) GetNextSSRC(ctx context.Context, treeId string) (int, error) {
	key := fmt.Sprintf("tree:%s:ssrc_counter", treeId)

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

// ParseSessionData converte map[string]string in dati strutturati
func ParseSessionData(data map[string]string) (sessionId string, audioSsrc, videoSsrc, roomId int, err error) {
	sessionId = data["session_id"]

	audioSsrc, err = strconv.Atoi(data["audio_ssrc"])
	if err != nil {
		return "", 0, 0, 0, fmt.Errorf("invalid audio_ssrc: %w", err)
	}

	videoSsrc, err = strconv.Atoi(data["video_ssrc"])
	if err != nil {
		return "", 0, 0, 0, fmt.Errorf("invalid video_ssrc: %w", err)
	}

	roomId, err = strconv.Atoi(data["room_id"])
	if err != nil {
		return "", 0, 0, 0, fmt.Errorf("invalid room_id: %w", err)
	}

	return sessionId, audioSsrc, videoSsrc, roomId, nil
}
