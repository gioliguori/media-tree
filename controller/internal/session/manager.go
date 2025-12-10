package session

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"controller/internal/domain"
	"controller/internal/redis"
	"controller/internal/tree"
)

type SessionManager struct {
	redis       *redis.Client
	treeManager *tree.TreeManager
	selector    *NodeSelector
	httpClient  *http.Client
}

func NewSessionManager(redisClient *redis.Client, treeMgr *tree.TreeManager) *SessionManager {
	return &SessionManager{
		redis:       redisClient,
		treeManager: treeMgr,
		selector:    NewNodeSelector(redisClient),
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// CreateSession wrapper gestisce manual o auto
func (sm *SessionManager) CreateSession(
	ctx context.Context,
	req CreateSessionRequest,
) (*SessionInfo, error) {
	if req.IsManual() {
		// Manual node selection
		log.Printf("[SessionManager] Creating session %s (manual mode)", req.SessionId)
		return sm.createSessionManual(ctx, req)
	} else {
		// Auto node selection with round-robin
		log.Printf("[SessionManager] Creating session %s (auto mode, scale=%s)", req.SessionId, req.Scale)
		return sm.createSessionAuto(ctx, req)
	}
}

func (sm *SessionManager) createSessionAuto(
	ctx context.Context,
	req CreateSessionRequest,
) (*SessionInfo, error) {
	// Default scale se non specificato
	if req.Scale == "" {
		req.Scale = ScaleLow
		log.Printf("[SessionManager] No scale specified, defaulting to 'low'")
	}

	// Seleziona tree con round-robin
	treeId, err := sm.selector.SelectTree(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to select tree: %w", err)
	}

	// Seleziona injection node con round-robin
	injectionNodeId, err := sm.selector.SelectInjection(ctx, treeId)
	if err != nil {
		return nil, fmt.Errorf("failed to select injection: %w", err)
	}

	// Determina count da scale
	egressCount := ScaleToEgressCount(req.Scale)

	// Seleziona egress nodes con round-robin
	egressNodeIds, err := sm.selector.SelectEgress(ctx, treeId, egressCount)
	if err != nil {
		return nil, fmt.Errorf("failed to select egress: %w", err)
	}

	log.Printf("[SessionManager] Auto-selected: tree=%s, injection=%s, egress=%v (scale=%s, count=%d)",
		treeId, injectionNodeId, egressNodeIds, req.Scale, egressCount)

	// Costruisci request manuale
	manualReq := CreateSessionRequest{
		SessionId:       req.SessionId,
		TreeId:          treeId,
		InjectionNodeId: injectionNodeId,
		EgressNodeIds:   egressNodeIds,
	}

	// Chiama implementazione manuale
	return sm.createSessionManual(ctx, manualReq)
}

func (sm *SessionManager) createSessionManual(
	ctx context.Context,
	req CreateSessionRequest,
) (*SessionInfo, error) {
	log.Printf("[SessionManager] Creating session %s on tree %s", req.SessionId, req.TreeId)

	// Validazione Input
	if err := sm.validateInput(ctx, req); err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	// SSRC univoci
	audioSsrc, videoSsrc, err := GenerateSSRCPair(ctx, sm.redis, req.TreeId)
	if err != nil {
		return nil, fmt.Errorf("failed to generate SSRC: %w", err)
	}
	log.Printf("[SessionManager] Generated SSRC: audio=%d, video=%d", audioSsrc, videoSsrc)

	// Genera RoomId
	roomId, err := GenerateRoomId(ctx, sm.redis, req.TreeId)
	if err != nil {
		return nil, fmt.Errorf("failed to generate room ID: %w", err)
	}
	log.Printf("[SessionManager] Generated room ID: %d", roomId)

	// Calcola Paths per ogni Egress
	paths, err := CalculateAllPaths(ctx, sm.redis, req.TreeId, req.InjectionNodeId, req.EgressNodeIds)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate paths: %w", err)
	}

	for egressId, path := range paths {
		log.Printf("[SessionManager] Path to %s: %v", egressId, path)
	}

	// Crea Session su Injection Node
	injectionResp, err := sm.createInjectionSession(ctx, req.TreeId, req.InjectionNodeId, req.SessionId, roomId, audioSsrc, videoSsrc)
	if err != nil {
		return nil, fmt.Errorf("failed to create injection session: %w", err)
	}
	log.Printf("[SessionManager] Injection session created: %s", injectionResp.Endpoint)

	// Configura Routing su Relay Nodes
	if err := sm.configureRelays(ctx, req.TreeId, req.SessionId, audioSsrc, videoSsrc, paths); err != nil {
		// TODO: Cleanup injection
		return nil, fmt.Errorf("failed to configure relays: %w", err)
	}

	// Crea Mountpoint su Egress Nodes
	pathInfos, err := sm.configureEgresses(ctx, req.TreeId, req.SessionId, audioSsrc, videoSsrc, paths)
	if err != nil {
		// TODO: Cleanup relay + injection
		return nil, fmt.Errorf("failed to configure egresses: %w", err)
	}

	// Salva Session Distribution Metadata su Redis
	if err := sm.saveSessionMetadata(ctx, req, paths); err != nil {
		// TODO: Cleanup everything
		return nil, fmt.Errorf("failed to save metadata: %w", err)
	}

	// Costruisci Response
	whipEndpoint := fmt.Sprintf("http://%s:7070%s", req.InjectionNodeId, injectionResp.Endpoint)

	sessionInfo := &SessionInfo{
		SessionId:       req.SessionId,
		TreeId:          req.TreeId,
		InjectionNodeId: req.InjectionNodeId,
		AudioSsrc:       audioSsrc,
		VideoSsrc:       videoSsrc,
		RoomId:          roomId,
		WhipEndpoint:    whipEndpoint,
		Paths:           pathInfos,
		EgressCount:     len(req.EgressNodeIds),
		Active:          true,
		CreatedAt:       time.Now(),
	}

	log.Printf("[SessionManager] Session %s created successfully", req.SessionId)
	return sessionInfo, nil
}

// validateInput valida input CreateSession
func (sm *SessionManager) validateInput(ctx context.Context, req CreateSessionRequest) error {
	// Check tree exists
	exists, err := sm.redis.TreeExists(ctx, req.TreeId)
	if err != nil {
		return fmt.Errorf("failed to check tree: %w", err)
	}
	if !exists {
		return fmt.Errorf("tree not found: %s", req.TreeId)
	}

	// Check injection node exists and is injection type
	injectionInfo, err := sm.redis.GetNodeProvisioning(ctx, req.TreeId, req.InjectionNodeId)
	if err != nil {
		return fmt.Errorf("injection node not found: %w", err)
	}
	if injectionInfo.NodeType != domain.NodeTypeInjection {
		return fmt.Errorf("node %s is not injection type (got %s)", req.InjectionNodeId, injectionInfo.NodeType)
	}

	// Check ogni egress exists and is egress type
	for _, egressId := range req.EgressNodeIds {
		egressInfo, err := sm.redis.GetNodeProvisioning(ctx, req.TreeId, egressId)
		if err != nil {
			return fmt.Errorf("egress node %s not found: %w", egressId, err)
		}
		if egressInfo.NodeType != domain.NodeTypeEgress {
			return fmt.Errorf("node %s is not egress type (got %s)", egressId, egressInfo.NodeType)
		}
	}

	// Check session non esiste già
	exists, err = sm.redis.SessionExists(ctx, req.TreeId, req.SessionId)
	if err != nil {
		return fmt.Errorf("failed to check session: %w", err)
	}
	if exists {
		return fmt.Errorf("session already exists: %s", req.SessionId)
	}

	return nil
}

// createInjectionSession chiama HTTP API dell'injection node
func (sm *SessionManager) createInjectionSession(
	ctx context.Context,
	treeId string,
	injectionNodeId string,
	sessionId string,
	roomId int,
	audioSsrc int,
	videoSsrc int,
) (*InjectionSessionResponse, error) {
	// Get node info per API port
	nodeInfo, err := sm.redis.GetNodeProvisioning(ctx, treeId, injectionNodeId)
	if err != nil {
		return nil, fmt.Errorf("failed to get node info: %w", err)
	}

	// Prepara request
	reqBody := InjectionSessionRequest{
		SessionId: sessionId,
		RoomId:    roomId,
		AudioSsrc: audioSsrc,
		VideoSsrc: videoSsrc,
	}

	bodyJSON, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// HTTP POST a injection node
	url := fmt.Sprintf("http://%s:%d/session", nodeInfo.InternalHost, nodeInfo.InternalAPIPort)
	log.Printf("[SessionManager] Calling injection API: %s", url)

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(bodyJSON))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := sm.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("injection API returned status %d", resp.StatusCode)
	}

	// Parse response
	var injectionResp InjectionSessionResponse
	if err := json.NewDecoder(resp.Body).Decode(&injectionResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &injectionResp, nil
}

// destroyInjectionSession chiama HTTP API dell'injection node per distruggere sessione
func (sm *SessionManager) destroyInjectionSession(
	ctx context.Context,
	treeId string,
	injectionNodeId string,
	sessionId string,
) error {
	// Get node info per API port
	nodeInfo, err := sm.redis.GetNodeProvisioning(ctx, treeId, injectionNodeId)
	if err != nil {
		return fmt.Errorf("failed to get node info: %w", err)
	}

	// HTTP POST a injection node
	url := fmt.Sprintf("http://%s:%d/session/%s/destroy", nodeInfo.InternalHost, nodeInfo.InternalAPIPort, sessionId)
	log.Printf("[SessionManager] Calling injection destroy API: %s", url)

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create HTTP request: %w", err)
	}

	resp, err := sm.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("injection destroy API returned status %d", resp.StatusCode)
	}

	log.Printf("[SessionManager] Injection session %s destroyed", sessionId)
	return nil
}

// configureRelays configura routing su tutti i relay nei paths
func (sm *SessionManager) configureRelays(
	ctx context.Context,
	treeId string,
	sessionId string,
	audioSsrc int,
	videoSsrc int,
	paths map[string][]string,
) error {
	// Colleziona tutti i relay e le loro route
	relayRoutes := make(map[string][]Route)

	for _, path := range paths {
		// Estrai relay nodes dal path
		relayNodes := ExtractRelayNodesFromPath(path)

		for _, relayId := range relayNodes {
			// Determina next hop per questo relay
			nextHop, err := GetNextHop(path, relayId)
			if err != nil {
				return fmt.Errorf("failed to get next hop for %s: %w", relayId, err)
			}

			// Crea route
			route := Route{
				TargetId:  nextHop,
				Host:      nextHop,
				AudioPort: 5002,
				VideoPort: 5004,
			}

			// Aggiungi route per questo relay
			relayRoutes[relayId] = append(relayRoutes[relayId], route)

			// Salva routing su Redis
			if err := sm.redis.AddRoute(ctx, treeId, sessionId, relayId, nextHop); err != nil {
				return fmt.Errorf("failed to save route: %w", err)
			}
		}
	}

	// Pubblica eventi a ogni relay
	for relayId, routes := range relayRoutes {
		event := RelaySessionEvent{
			Type:      "session-created",
			SessionId: sessionId,
			TreeId:    treeId,
			AudioSsrc: audioSsrc,
			VideoSsrc: videoSsrc,
			Routes:    routes,
		}

		if err := sm.redis.PublishNodeSessionCreated(ctx, treeId, relayId, event); err != nil {
			return fmt.Errorf("failed to publish to relay %s: %w", relayId, err)
		}

		// Registra session su relay (per recovery)
		if err := sm.redis.AddSessionToNode(ctx, treeId, relayId, sessionId); err != nil {
			log.Printf("[WARN] Failed to register session on relay %s: %v", relayId, err)
		}

		log.Printf("[SessionManager] Configured relay %s with %d routes", relayId, len(routes))
	}

	return nil
}

// configureEgresses configura mountpoint su tutti gli egress
func (sm *SessionManager) configureEgresses(
	ctx context.Context,
	treeId string,
	sessionId string,
	audioSsrc int,
	videoSsrc int,
	paths map[string][]string,
) ([]PathInfo, error) {
	pathInfos := make([]PathInfo, 0, len(paths))

	for egressId, path := range paths {
		// Pubblica evento session-created a egress
		event := EgressSessionEvent{
			Type:      "session-created",
			SessionId: sessionId,
			TreeId:    treeId,
			AudioSsrc: audioSsrc,
			VideoSsrc: videoSsrc,
		}

		if err := sm.redis.PublishNodeSessionCreated(ctx, treeId, egressId, event); err != nil {
			return nil, fmt.Errorf("failed to publish to egress %s: %w", egressId, err)
		}

		// Get node info per costruire WHEP endpoint
		nodeInfo, err := sm.redis.GetNodeProvisioning(ctx, treeId, egressId)
		if err != nil {
			return nil, fmt.Errorf("failed to get egress info: %w", err)
		}

		whepEndpoint := fmt.Sprintf("http://%s:%d/whep/endpoint/%s",
			nodeInfo.InternalHost,
			nodeInfo.InternalAPIPort,
			sessionId,
		)

		// Salva path info su Redis
		pathStr := PathToString(path)
		if err := sm.redis.SaveSessionEgressInfo(ctx, treeId, sessionId, egressId, pathStr, whepEndpoint); err != nil {
			return nil, fmt.Errorf("failed to save egress info: %w", err)
		}

		// Aggiungi egress a session
		if err := sm.redis.AddEgressToSession(ctx, treeId, sessionId, egressId); err != nil {
			return nil, fmt.Errorf("failed to add egress to session: %w", err)
		}

		// Registra session su egress (per recovery)
		if err := sm.redis.AddSessionToNode(ctx, treeId, egressId, sessionId); err != nil {
			log.Printf("[WARN] Failed to register session on egress %s: %v", egressId, err)
		}

		pathInfo := PathInfo{
			EgressNodeId: egressId,
			Hops:         path,
			WhepEndpoint: whepEndpoint,
		}
		pathInfos = append(pathInfos, pathInfo)

		log.Printf("[SessionManager] Configured egress %s: %s", egressId, whepEndpoint)
	}

	return pathInfos, nil
}

// saveSessionMetadata salva metadata distribuzione session su Redis
func (sm *SessionManager) saveSessionMetadata(
	ctx context.Context,
	req CreateSessionRequest,
	paths map[string][]string,
) error {
	// Estrai relay nodes dai paths
	relayNodeIds := sm.extractRelayNodes(paths)

	egressNodeIdsJSON, err := json.Marshal(req.EgressNodeIds)
	if err != nil {
		return fmt.Errorf("failed to marshal egressNodeIds: %w", err)
	}

	relayNodeIdsJSON, err := json.Marshal(relayNodeIds)
	if err != nil {
		return fmt.Errorf("failed to marshal relayNodeIds: %w", err)
	}

	distributionData := map[string]any{
		"egressNodeIds": string(egressNodeIdsJSON),
		"relayNodeIds":  string(relayNodeIdsJSON),
		"egressCount":   len(req.EgressNodeIds),
		"relayCount":    len(relayNodeIds),
	}

	// aggiunge campi (non sovrascrive injection)
	if err := sm.redis.SaveSession(ctx, req.TreeId, req.SessionId, distributionData); err != nil {
		return fmt.Errorf("failed to save distribution metadata: %w", err)
	}

	// Recovery index
	if err := sm.redis.AddSessionToNode(ctx, req.TreeId, req.InjectionNodeId, req.SessionId); err != nil {
		log.Printf("[WARN] Failed to register session on injection: %v", err)
	}

	log.Printf("[SessionManager] Distribution metadata saved to Redis")
	return nil
}

// extractRelayNodes estrae unique relay nodes da tutti i paths
func (sm *SessionManager) extractRelayNodes(paths map[string][]string) []string {
	relayMap := make(map[string]bool)

	for _, path := range paths {
		relayNodes := ExtractRelayNodesFromPath(path)
		for _, relayId := range relayNodes {
			relayMap[relayId] = true
		}
	}

	// Convert map to slice
	relayNodeIds := make([]string, 0, len(relayMap))
	for relayId := range relayMap {
		relayNodeIds = append(relayNodeIds, relayId)
	}

	return relayNodeIds
}

// GetSession legge info sessione completa
func (sm *SessionManager) GetSession(
	ctx context.Context,
	treeId string,
	sessionId string,
) (*SessionInfo, error) {
	log.Printf("[SessionManager] Getting session %s on tree %s", sessionId, treeId)

	// Get session metadata
	sessionData, err := sm.redis.GetSession(ctx, treeId, sessionId)
	if err != nil {
		return nil, fmt.Errorf("failed to get session: %w", err)
	}
	cleanWhipEndpoint := fmt.Sprintf("http://%s:7070/whip/endpoint/%s", sessionData["injectionNodeId"], sessionId)
	// Parse session data
	sessionInfo := &SessionInfo{
		SessionId:       sessionData["sessionId"],
		TreeId:          sessionData["treeId"],
		InjectionNodeId: sessionData["injectionNodeId"],
		WhipEndpoint:    cleanWhipEndpoint,
		Active:          sessionData["active"] == "true",
	}

	// Parse numeric fields
	if val, err := strconv.Atoi(sessionData["audioSsrc"]); err == nil {
		sessionInfo.AudioSsrc = val
	}
	if val, err := strconv.Atoi(sessionData["videoSsrc"]); err == nil {
		sessionInfo.VideoSsrc = val
	}
	if val, err := strconv.Atoi(sessionData["roomId"]); err == nil {
		sessionInfo.RoomId = val
	}
	if val, err := strconv.Atoi(sessionData["egressCount"]); err == nil {
		sessionInfo.EgressCount = val
	}

	// Parse created_at
	if val, err := strconv.ParseInt(sessionData["createdAt"], 10, 64); err == nil {
		//sessionInfo.CreatedAt = time.Unix(val, 0)
		sessionInfo.CreatedAt = time.Unix(val/1000, 0)
	}

	// Get egress paths
	egressNodeIds := []string{}
	if egressJSON := sessionData["egressNodeIds"]; egressJSON != "" {
		json.Unmarshal([]byte(egressJSON), &egressNodeIds)
	}

	// Get paths for each egress
	pathInfos := make([]PathInfo, 0, len(egressNodeIds))
	for _, egressId := range egressNodeIds {
		egressInfo, err := sm.redis.GetSessionEgressInfo(ctx, treeId, sessionId, egressId)
		if err != nil {
			log.Printf("[WARN] Failed to get egress info for %s: %v", egressId, err)
			continue
		}

		hops := []string{}
		if egressInfo["path"] != "" {
			hops = strings.Split(egressInfo["path"], ",")
		}

		pathInfos = append(pathInfos, PathInfo{
			EgressNodeId: egressId,
			Hops:         hops,
			WhepEndpoint: egressInfo["whep_endpoint"],
		})
	}
	sessionInfo.Paths = pathInfos

	log.Printf("[SessionManager] Session %s retrieved successfully", sessionId)
	return sessionInfo, nil
}

// DestroySession distrugge una sessione completa
func (sm *SessionManager) DestroySession(
	ctx context.Context,
	treeId string,
	sessionId string,
) error {
	log.Printf("[SessionManager] Destroying session %s on tree %s", sessionId, treeId)

	// Get session metadata to find nodes
	sessionData, err := sm.redis.GetSession(ctx, treeId, sessionId)
	if err != nil {
		return fmt.Errorf("failed to get session: %w", err)
	}

	injectionNodeId := sessionData["injectionNodeId"]

	// Parse egress nodes
	egressNodeIds := []string{}
	if egressJSON := sessionData["egressNodeIds"]; egressJSON != "" {
		json.Unmarshal([]byte(egressJSON), &egressNodeIds)
	}

	// Parse relay nodes
	relayNodeIds := []string{}
	if relayJSON := sessionData["relayNodeIds"]; relayJSON != "" {
		json.Unmarshal([]byte(relayJSON), &relayNodeIds)
	}

	// Pubblica eventi session-destroyed a tutti i nodi
	log.Printf("[SessionManager] Publishing session-destroyed events")

	// Injection
	// Distruggi sessione su Injection Node via HTTP API
	log.Printf("[SessionManager] Destroying injection session via HTTP API")
	if err := sm.destroyInjectionSession(ctx, treeId, injectionNodeId, sessionId); err != nil {
		log.Printf("[WARN] Failed to destroy injection session: %v", err)
	}

	// Relay nodes
	for _, relayId := range relayNodeIds {
		if err := sm.redis.PublishNodeSessionDestroyed(ctx, treeId, relayId, sessionId); err != nil {
			log.Printf("[WARN] Failed to publish to relay %s: %v", relayId, err)
		}
	}

	// Egress nodes
	for _, egressId := range egressNodeIds {
		if err := sm.redis.PublishNodeSessionDestroyed(ctx, treeId, egressId, sessionId); err != nil {
			log.Printf("[WARN] Failed to publish to egress %s: %v", egressId, err)
		}
	}

	// Cleanup Redis keys
	log.Printf("[SessionManager] Cleaning up Redis keys")

	// Delete session metadata
	if err := sm.redis.DeleteSession(ctx, treeId, sessionId); err != nil {
		log.Printf("[WARN] Failed to delete session: %v", err)
	}

	// Delete egress infos
	for _, egressId := range egressNodeIds {
		key := fmt.Sprintf("tree:%s:session:%s:egress:%s", treeId, sessionId, egressId)
		sm.redis.Del(ctx, key)
	}

	// Delete egress list
	key := fmt.Sprintf("tree:%s:session:%s:egresses", treeId, sessionId)
	sm.redis.Del(ctx, key)

	// Delete routing for all relays
	if err := sm.redis.RemoveAllRoutesForSession(ctx, treeId, sessionId); err != nil {
		log.Printf("[WARN] Failed to delete routes: %v", err)
	}

	// Remove from tree sessions index
	if err := sm.redis.RemoveSessionFromTree(ctx, treeId, sessionId); err != nil {
		log.Printf("[WARN] Failed to remove from tree index: %v", err)
	}

	// Remove from node sessions indexes
	sm.redis.RemoveSessionFromNode(ctx, treeId, injectionNodeId, sessionId)
	for _, relayId := range relayNodeIds {
		sm.redis.RemoveSessionFromNode(ctx, treeId, relayId, sessionId)
	}
	for _, egressId := range egressNodeIds {
		sm.redis.RemoveSessionFromNode(ctx, treeId, egressId, sessionId)
	}

	log.Printf("[SessionManager] Session %s destroyed successfully", sessionId)
	return nil
}

// ListSessions lista tutte le sessioni di un tree
func (sm *SessionManager) ListSessions(
	ctx context.Context,
	treeId string,
) ([]*SessionInfo, error) {
	log.Printf("[SessionManager] Listing sessions for tree %s", treeId)

	// Get all session IDs for this tree
	sessionIds, err := sm.redis.GetTreeSessions(ctx, treeId)
	if err != nil {
		return nil, fmt.Errorf("failed to get tree sessions: %w", err)
	}

	// Get details for each session
	sessions := make([]*SessionInfo, 0, len(sessionIds))
	for _, sessionId := range sessionIds {
		sessionInfo, err := sm.GetSession(ctx, treeId, sessionId)
		if err != nil {
			log.Printf("[WARN] Failed to get session %s: %v", sessionId, err)
			continue
		}
		sessions = append(sessions, sessionInfo)
	}

	log.Printf("[SessionManager] Found %d sessions for tree %s", len(sessions), treeId)
	return sessions, nil
}
