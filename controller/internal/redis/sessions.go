package redis

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"
)

// SaveSession salva session metadata
func (c *Client) SaveSession(
	ctx context.Context,
	sessionId string,
	data map[string]any,
) error {
	key := fmt.Sprintf("session:%s", sessionId)
	return c.rdb.HSet(ctx, key, data).Err()
}

// GetSession legge session metadata
func (c *Client) GetSession(
	ctx context.Context,
	sessionId string,
) (map[string]string, error) {
	key := fmt.Sprintf("session:%s", sessionId)
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
func (c *Client) SessionExists(ctx context.Context, sessionId string) (bool, error) {
	key := fmt.Sprintf("session:%s", sessionId)
	exists, err := c.rdb.Exists(ctx, key).Result()
	return exists > 0, err
}

// DeleteSession rimuove session
func (c *Client) DeleteSession(
	ctx context.Context,
	sessionId string,
) error {
	key := fmt.Sprintf("session:%s", sessionId)
	return c.rdb.Del(ctx, key).Err()
}

// AddSessionToGlobalIndex aggiunge la sessione all'indice di sistema
func (c *Client) AddSessionToGlobalIndex(
	ctx context.Context,
	sessionId string,
) error {
	return c.rdb.SAdd(ctx, "sessions:global", sessionId).Err()
}

// RemoveSessionFromGlobalIndex rimuove la sessione dall'indice di sistema
func (c *Client) RemoveSessionFromGlobalIndex(
	ctx context.Context,
	sessionId string,
) error {
	return c.rdb.SRem(ctx, "sessions:global", sessionId).Err()
}

// GetGlobalSessions legge tutte le sessioni attive nel sistema
func (c *Client) GetGlobalSessions(
	ctx context.Context,
) ([]string, error) {
	return c.rdb.SMembers(ctx, "sessions:global").Result()
}

// Egress

// AddEgressToSession aggiunge egress a lista session
func (c *Client) AddEgressToSession(
	ctx context.Context,
	sessionId string,
	egressId string,
) error {
	key := fmt.Sprintf("session:%s:egresses", sessionId)
	return c.rdb.SAdd(ctx, key, egressId).Err()
}

// GetSessionEgresses legge lista egress per session
func (c *Client) GetSessionEgresses(
	ctx context.Context,
	sessionId string,
) ([]string, error) {
	key := fmt.Sprintf("session:%s:egresses", sessionId)
	return c.rdb.SMembers(ctx, key).Result()
}

// RemoveEgressFromSession rimuove egress da session
func (c *Client) RemoveEgressFromSession(
	ctx context.Context,
	sessionId string,
	egressId string,
) error {
	key := fmt.Sprintf("session:%s:egresses", sessionId)
	return c.rdb.SRem(ctx, key, egressId).Err()
}

// FindEgressServingSession ritorna egress che servono una sessione
// Usato da ProvisionViewer per riusare egress esistente invece di crearne uno nuovo
func (c *Client) FindEgressServingSession(
	ctx context.Context,
	sessionId string,
) ([]string, error) {
	// Wrapper semantico per GetSessionEgresses
	return c.GetSessionEgresses(ctx, sessionId)
}

// SaveSessionPath salva path costruito per coppia (session, egress)
func (c *Client) SaveSessionPath(
	ctx context.Context,
	sessionId string,
	egressId string,
	path []string,
) error {
	key := fmt.Sprintf("path:%s:%s", sessionId, egressId)
	pathStr := strings.Join(path, ",")
	return c.rdb.Set(ctx, key, pathStr, 0).Err()
}

// GetSessionPath legge path per coppia (session, egress)
func (c *Client) GetSessionPath(
	ctx context.Context,
	sessionId string,
	egressId string,
) ([]string, error) {
	key := fmt.Sprintf("path:%s:%s", sessionId, egressId)
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
func (c *Client) GetNextRoomId(ctx context.Context) (int, error) {
	key := "global:roomCounter"

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
func (c *Client) GetNextSSRC(ctx context.Context) (int, error) {
	key := "global:ssrcCounter"

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

// acquireEdgeSlotLua:
// Scorre la catena della sessione. Se un nodo ha carico globale < 20 lo occupa atomicamente
var acquireEdgeSlotLua = `
-- Recupera la catena della sessione saltando il primo elemento (Relay Root)
local chain = redis.call('LRANGE', KEYS[1], 1, -1)
local edge_counts_key = KEYS[2]
local global_load_key = KEYS[3]

-- Cicla sui relay
for _, node_id in ipairs(chain) do
    local occupied = tonumber(redis.call('ZSCORE', global_load_key, node_id) or 0)
	-- Recuperiamo il limite dal nodo
    local provisioning_key = "node:" .. node_id .. ":provisioning"
    local max_slots = tonumber(redis.call('HGET', provisioning_key, "maxSlots") or 20)

    if occupied < max_slots then
        redis.call('ZINCRBY', global_load_key, 1, node_id)
        redis.call('HINCRBY', edge_counts_key, node_id, 1)
        return node_id
    end
end
return "FULL"
`

// acquireInjectionSlotLua:
// Gestisce slot sessioni di injection

var acquireInjectionSlotLua = `
-- KEYS[1] -> pool:injection:load (ZSET)
-- ARGV[1] -> sessionId

local pool_key = KEYS[1]
-- Ottieni tutti i nodi injection ordinati per numero di sessioni (score)
local nodes = redis.call('ZRANGE', pool_key, 0, -1, 'WITHSCORES')

for i=1, #nodes, 2 do
    local node_id = nodes[i]
    local current_sessions = tonumber(nodes[i+1])
    
    -- Leggi il limite MaxSlots
    local provisioning_key = "node:" .. node_id .. ":provisioning"
    local max_capacity = tonumber(redis.call('HGET', provisioning_key, "maxSlots") or 10)
    
    -- Se il nodo ha ancora capacità per una nuova sessione
    if current_sessions < max_capacity then
        -- Occupa lo slot incrementando lo score nel pool
        redis.call('ZINCRBY', pool_key, 1, node_id)
        -- Registra la sessione nell'indice del nodo
        redis.call('SADD', "node:" .. node_id .. ":sessions", ARGV[1])
        return node_id
    end
end

return "FULL"
`

// AcquireInjectionSlot prenota un posto per una sessione
func (c *Client) AcquireInjectionSlot(ctx context.Context, sessionId string) (string, error) {
	res, err := c.rdb.Eval(ctx, acquireInjectionSlotLua, []string{"pool:injection:load"}, sessionId).Result()
	if err != nil {
		return "", err
	}
	return res.(string), nil
}

// ReleaseInjectionSlot libera lo slot quando la sessione viene distrutta
func (c *Client) ReleaseInjectionSlot(ctx context.Context, nodeId, sessionId string) error {
	pipe := c.rdb.Pipeline()
	// Decrementa il numero di sessioni attive sul nodo
	pipe.ZIncrBy(ctx, "pool:injection:load", -1, nodeId)
	// Rimuove la sessione dalla lista del nodo
	pipe.SRem(ctx, fmt.Sprintf("node:%s:sessions", nodeId), sessionId)
	_, err := pipe.Exec(ctx)
	return err
}

// AcquireEdgeSlot tenta di occupare un buco nella catena esistente in modo atomico
func (c *Client) AcquireEdgeSlot(ctx context.Context, sessionId string) (string, error) {
	keys := []string{
		fmt.Sprintf("session:%s:chain", sessionId),
		fmt.Sprintf("session:%s:edge_counts", sessionId),
		"pool:relay:load",
	}
	res, err := c.rdb.Eval(ctx, acquireEdgeSlotLua, keys).Result()
	if err != nil {
		return "", err
	}
	return res.(string), nil
}

// Gestione catena

// InitSessionChain crea la spina dorsale iniziale [RelayRoot]
func (c *Client) InitSessionChain(ctx context.Context, sessionId, relayRootId string) error {
	key := fmt.Sprintf("session:%s:chain", sessionId)
	return c.rdb.RPush(ctx, key, relayRootId).Err()
}

// AddRelayToChain allunga la catena
// Occupa 2 slot sul relay appena aggiunto alla catena (1 Riserva Deep + 1 primo Edge).
func (c *Client) AddRelayToChain(ctx context.Context, sessionId, nodeId string) error {
	chainKey := fmt.Sprintf("session:%s:chain", sessionId)
	countsKey := fmt.Sprintf("session:%s:edge_counts", sessionId)
	loadKey := "pool:relay:load"
	nodeSessionsKey := fmt.Sprintf("node:%s:sessions", nodeId)

	pipe := c.rdb.Pipeline()
	pipe.RPush(ctx, chainKey, nodeId)
	pipe.HSet(ctx, countsKey, nodeId, 1)
	pipe.ZIncrBy(ctx, loadKey, 2, nodeId) // +2 (Deep + primo Edge)
	pipe.SAdd(ctx, nodeSessionsKey, sessionId)
	_, err := pipe.Exec(ctx)
	return err
}

func (c *Client) GetSessionChain(ctx context.Context, sessionId string) ([]string, error) {
	key := fmt.Sprintf("session:%s:chain", sessionId)
	return c.rdb.LRange(ctx, key, 0, -1).Result()
}

func (c *Client) RemoveFromSessionChain(ctx context.Context, sessionId, nodeId string) error {
	key := fmt.Sprintf("session:%s:chain", sessionId)
	return c.rdb.LRem(ctx, key, 1, nodeId).Err()
}

// Cleanup

func (c *Client) GetEdgeCount(ctx context.Context, sessionId, nodeId string) (int, error) {
	key := fmt.Sprintf("session:%s:edge_counts", sessionId)
	val, err := c.rdb.HGet(ctx, key, nodeId).Result()
	if err != nil {
		if err == redis.Nil {
			return 0, nil
		}
		return 0, err
	}
	return strconv.Atoi(val)
}

// Chiamata allo scollegamento di un Egress
func (c *Client) ReleaseEdgeSlot(ctx context.Context, sessionId, nodeId string) error {
	countsKey := fmt.Sprintf("session:%s:edge_counts", sessionId)
	loadKey := "pool:relay:load"

	pipe := c.rdb.Pipeline()
	pipe.HIncrBy(ctx, countsKey, nodeId, -1)
	pipe.ZIncrBy(ctx, loadKey, -1, nodeId)
	_, err := pipe.Exec(ctx)
	return err
}

// Chiamata quando il nodo esce dalla chain
func (c *Client) ReleaseDeepReserve(ctx context.Context, sessionId, nodeId string) error {
	loadKey := "pool:relay:load"
	nodeSessionsKey := fmt.Sprintf("node:%s:sessions", nodeId)
	countsKey := fmt.Sprintf("session:%s:edge_counts", sessionId)

	pipe := c.rdb.Pipeline()
	pipe.ZIncrBy(ctx, loadKey, -1, nodeId)
	pipe.SRem(ctx, nodeSessionsKey, sessionId)
	pipe.HDel(ctx, countsKey, nodeId)
	_, err := pipe.Exec(ctx)
	return err
}

// SetEgressParent traccia a quale Relay è appeso un Egress
func (c *Client) SetEgressParent(ctx context.Context, sessionId, egressId, relayId string) error {
	key := fmt.Sprintf("session:%s:egress_parents", sessionId)
	return c.rdb.HSet(ctx, key, egressId, relayId).Err()
}

func (c *Client) GetEgressParent(ctx context.Context, sessionId, egressId string) (string, error) {
	key := fmt.Sprintf("session:%s:egress_parents", sessionId)
	return c.rdb.HGet(ctx, key, egressId).Result()
}

func (c *Client) RemoveEgressParent(ctx context.Context, sessionId, egressId string) error {
	key := fmt.Sprintf("session:%s:egress_parents", sessionId)
	return c.rdb.HDel(ctx, key, egressId).Err()
}

// FindBestRelayForDeepening cerca un relay nel pool con almeno 2 slot liberi
func (c *Client) FindBestRelayForDeepening(ctx context.Context, excludeNodes []string) (string, error) {
	relays, err := c.rdb.ZRangeWithScores(ctx, "pool:relay:load", 0, -1).Result()
	if err != nil {
		return "", err
	}

	if len(relays) == 0 {
		return "", fmt.Errorf("pool:relay:load is empty - check if relays are registered correctly")
	}

	excludeMap := make(map[string]bool)
	for _, n := range excludeNodes {
		excludeMap[n] = true
	}

	var unusedCandidate string
	var fallbackCandidate string

	// Balanced zone (0 < Load <= 10)
	// Cerchiamo un relay già usato ma con ampi margini di crescita
	for _, r := range relays {
		nodeId := r.Member.(string)
		if excludeMap[nodeId] {
			continue
		}

		nodeInfo, err := c.GetNodeProvisioning(ctx, nodeId)
		if err != nil || nodeInfo.Role != "standalone" {
			continue
		}

		// Soglia Balanced: 50% della capacità
		balancedThreshold := float64(nodeInfo.MaxSlots) / 2
		// Soglia Deepening: MaxSlots - 2 (serve spazio per 1 riserva + 1 egress)
		deepeningLimit := float64(nodeInfo.MaxSlots - 2)

		// Balanced Zone (0 < Load <= 50%)
		if r.Score > 0 && r.Score <= balancedThreshold {
			return nodeId, nil
		}
		// Unused (Backup)
		if r.Score == 0 && unusedCandidate == "" {
			unusedCandidate = nodeId
		}

		//  Saturation (Fino al limite fisico del nodo)
		if r.Score > balancedThreshold && r.Score <= deepeningLimit && fallbackCandidate == "" {
			fallbackCandidate = nodeId
		}
	}

	// Usa unused relay
	if unusedCandidate != "" {
		log.Printf("[NodeSelector] Expansion: Using unused relay %s", unusedCandidate)
		return unusedCandidate, nil
	}

	// Saturation
	if fallbackCandidate != "" {
		log.Printf("[NodeSelector] Saturation: Using highly loaded relay %s", fallbackCandidate)
		return fallbackCandidate, nil
	}

	return "", fmt.Errorf("no available standalone relays (all physically saturated)")
}

func (c *Client) DeleteSessionChain(ctx context.Context, sessionId string) error {
	pipe := c.rdb.Pipeline()
	pipe.Del(ctx, fmt.Sprintf("session:%s:chain", sessionId))
	pipe.Del(ctx, fmt.Sprintf("session:%s:edge_counts", sessionId))
	pipe.Del(ctx, fmt.Sprintf("session:%s:egress_parents", sessionId))
	pipe.Del(ctx, fmt.Sprintf("session:%s:egresses", sessionId))

	// Cerchiamo tutte le chiavi routing per questa sessione (routing:sessionId:*)
	pattern := fmt.Sprintf("routing:%s:*", sessionId)
	keys, err := c.rdb.Keys(ctx, pattern).Result()
	if err == nil && len(keys) > 0 {
		pipe.Del(ctx, keys...)
	}

	_, err = pipe.Exec(ctx)
	return err
}
