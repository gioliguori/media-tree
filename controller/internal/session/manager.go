package session

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"controller/internal/redis"
)

type SessionManager struct {
	redis      *redis.Client
	selector   *NodeSelector
	httpClient *http.Client
}

func NewSessionManager(redisClient *redis.Client) *SessionManager {
	return &SessionManager{
		redis:    redisClient,
		selector: NewNodeSelector(redisClient),
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// CreateSession crea sessione dormiente (SOLO injection)
// Chiamato quando broadcaster vuole iniziare streaming
// -> Seleziona injection (round-robin per il momento)
// -> Get relay-root associato (topologia statica)
// -> Genera SSRC + RoomId univoci
// -> Salva metadata sessione
// -> Notifica injection node (HTTP API)
// -> Injection crea Janus VideoRoom + forwarda a relay-root
// NON provisiona egress - quello succede on-demand quando arriva viewer
func (sm *SessionManager) CreateSession(
	ctx context.Context,
	sessionId string,
	treeId string,
) (*SessionInfo, error) {
	log.Printf("[SessionManager] Creating session %s on tree %s", sessionId, treeId)
	// Check tree esiste
	exists, err := sm.redis.TreeExists(ctx, treeId)
	if err != nil {
		return nil, fmt.Errorf("failed to check tree:  %w", err)
	}
	if !exists {
		return nil, fmt.Errorf("tree not found: %s", treeId)
	}

	// Check session non esiste già
	exists, err = sm.redis.SessionExists(ctx, treeId, sessionId)
	if err != nil {
		return nil, fmt.Errorf("failed to check session: %w", err)
	}
	if exists {
		return nil, fmt.Errorf("session already exists: %s", sessionId)
	}

	// Seleziona Injection
	injectionId, err := sm.selector.SelectInjection(ctx, treeId)
	if err != nil {
		return nil, fmt.Errorf("failed to select injection: %w", err)
	}

	log.Printf("[SessionManager] Selected injection:  %s", injectionId)

	// Get Relay root
	children, err := sm.redis.GetNodeChildren(ctx, treeId, injectionId)
	if err != nil || len(children) == 0 {
		return nil, fmt.Errorf("relay-root not found for injection %s", injectionId)
	}
	relayRootId := children[0] // Primo child è relay-root (coppia statica)
	log.Printf("[SessionManager] Relay-root:  %s", relayRootId)

	// Genera ssrc e roomId
	audioSsrc, videoSsrc, err := GenerateSSRCPair(ctx, sm.redis, treeId)
	if err != nil {
		return nil, fmt.Errorf("failed to generate SSRC: %w", err)
	}

	roomId, err := GenerateRoomId(ctx, sm.redis, treeId)
	if err != nil {
		return nil, fmt.Errorf("failed to generate room ID: %w", err)
	}
	log.Printf("[SessionManager] Generated SSRC: audio=%d, video=%d, room=%d",
		audioSsrc, videoSsrc, roomId)

	// Salva metadata sessione
	metadata := map[string]any{
		"sessionId":       sessionId,
		"treeId":          treeId,
		"injectionNodeId": injectionId,
		"relayRootId":     relayRootId,
		"audioSsrc":       audioSsrc,
		"videoSsrc":       videoSsrc,
		"roomId":          roomId,
		"active":          true,
		"createdAt":       time.Now().UnixMilli(),
	}
	if err := sm.redis.SaveSession(ctx, treeId, sessionId, metadata); err != nil {
		return nil, fmt.Errorf("failed to save session:  %w", err)
	}

	// Aggiungi a index tree
	if err := sm.redis.AddSessionToTree(ctx, treeId, sessionId); err != nil {
		log.Printf("[WARN] Failed to add session to tree index: %v", err)
	}
	// Registra session su injection node (per recovery)
	if err := sm.redis.AddSessionToNode(ctx, treeId, injectionId, sessionId); err != nil {
		log.Printf("[WARN] Failed to register session on injection:  %v", err)
	}
	log.Printf("[SessionManager] Session metadata saved to Redis")

	// Notifica Injection Node
	injectionResp, err := sm.createInjectionSession(ctx, treeId, injectionId, sessionId, roomId, audioSsrc, videoSsrc)
	if err != nil {
		// Rollback:   cleanup Redis
		sm.redis.DeleteSession(ctx, treeId, sessionId)
		sm.redis.RemoveSessionFromTree(ctx, treeId, sessionId)
		return nil, fmt.Errorf("failed to create injection session: %w", err)
	}
	log.Printf("[SessionManager] Injection session created: %s", injectionResp.Endpoint)

	// Costruisci risposta
	// Get injection node info per WHIP endpoint
	injectionNode, err := sm.redis.GetNodeProvisioning(ctx, treeId, injectionId)
	if err != nil {
		log.Printf("[WARN] Failed to get injection node info: %v", err)
	}

	whipEndpoint := fmt.Sprintf("http://%s:%d%s",
		injectionNode.InternalHost,
		injectionNode.InternalAPIPort,
		injectionResp.Endpoint,
	)

	sessionInfo := &SessionInfo{
		SessionId:       sessionId,
		TreeId:          treeId,
		InjectionNodeId: injectionId,
		AudioSsrc:       audioSsrc,
		VideoSsrc:       videoSsrc,
		RoomId:          roomId,
		WhipEndpoint:    whipEndpoint,
		Active:          true,
		CreatedAt:       time.Now(),
	}

	log.Printf("[SessionManager] Session %s created (dormant)", sessionId)
	return sessionInfo, nil
}

// ProvisionViewer provisiona egress + path on-demand per viewer
// Chiamato quando viewer vuole guardare sessione
// -> Get session metadata
// -> Check egress esistenti -> Riusa se possibile (multicast)
// -> Seleziona nuovo egress (breadth-first)
// -> Costruisci path (layer-by-layer)
// -> Provvisiona relay + egress
// -> Salva mapping session->egress
// -> Return WHEP endpoint
func (sm *SessionManager) ProvisionViewer(
	ctx context.Context,
	treeId string,
	sessionId string,
) (*ViewSessionResponse, error) {
	log.Printf("[SessionManager] Provisioning viewer for session %s", sessionId)
	// Get session metadata
	session, err := sm.redis.GetSession(ctx, treeId, sessionId)
	if err != nil {
		return nil, fmt.Errorf("session not found: %w", err)
	}
	injectionId := session["injectionNodeId"]
	relayRootId := session["relayRootId"]
	audioSsrc := parseInt(session["audioSsrc"])
	videoSsrc := parseInt(session["videoSsrc"])
	log.Printf("[SessionManager] Session:  injection=%s, relay-root=%s", injectionId, relayRootId)
	// Check egress esistenti
	existingEgress, err := sm.redis.FindEgressServingSession(ctx, treeId, sessionId)
	if err == nil && len(existingEgress) > 0 {
		log.Printf("[SessionManager] Found %d existing egress:  %v", len(existingEgress), existingEgress)

		// Riusa egress se disponibile
		for _, egressId := range existingEgress {
			if sm.selector.CanAcceptViewer(ctx, egressId) {
				log.Printf("[SessionManager] Reusing egress %s (multicast)", egressId)

				egressNode, _ := sm.redis.GetNodeProvisioning(ctx, treeId, egressId)
				path, _ := sm.redis.GetSessionPath(ctx, treeId, sessionId, egressId)

				return &ViewSessionResponse{
					SessionId:    sessionId,
					EgressNodeId: egressId,
					EgressPort:   egressNode.ExternalAPIPort,
					WhepEndpoint: fmt.Sprintf("http://%s:%d/?id=%s",
						egressNode.ExternalHost,
						egressNode.ExternalAPIPort,
						sessionId),
					Path:   path,
					Reused: true,
				}, nil
			}
		}

		log.Printf("[SessionManager] All existing egress overloaded, creating new one")
	}

	// Seleziona nuovo egress
	egressId, err := sm.selector.SelectBestEgressForSession(ctx, treeId, sessionId)
	if err != nil {
		// TODO scaling?
		return nil, fmt.Errorf("no egress available - scaling needed: %w", err)
	}

	log.Printf("[SessionManager] Selected new egress: %s", egressId)

	// Costruisci path
	path, err := BuildPath(ctx, sm.redis, treeId, sessionId, injectionId, relayRootId, egressId)
	if err != nil {
		return nil, fmt.Errorf("failed to build path: %w", err)
	}

	log.Printf("[SessionManager] Path:  %v", path)

	// Provvisiona path
	if err := sm.provisionPath(ctx, treeId, sessionId, audioSsrc, videoSsrc, path); err != nil {
		return nil, fmt.Errorf("failed to provision path: %w", err)
	}
	// Salva mapping
	if err := sm.redis.AddEgressToSession(ctx, treeId, sessionId, egressId); err != nil {
		log.Printf("[WARN] Failed to add egress to session: %v", err)
	}
	if err := sm.redis.AddSessionToNode(ctx, treeId, egressId, sessionId); err != nil {
		log.Printf("[WARN] Failed to register session on egress: %v", err)
	}
	if err := sm.redis.SaveSessionPath(ctx, treeId, sessionId, egressId, path); err != nil {
		log.Printf("[WARN] Failed to save path: %v", err)
	}
	// Get egress info
	egressNode, err := sm.redis.GetNodeProvisioning(ctx, treeId, egressId)
	if err != nil {
		return nil, fmt.Errorf("failed to get egress node: %w", err)
	}
	log.Printf("[SessionManager] Viewer provisioned: egress=%s, port=%d", egressId, egressNode.ExternalAPIPort)

	return &ViewSessionResponse{
		SessionId:    sessionId,
		EgressNodeId: egressId,
		EgressPort:   egressNode.ExternalAPIPort,
		WhepEndpoint: fmt.Sprintf("http://%s:%d/?id=%s",
			egressNode.ExternalHost,
			egressNode.ExternalAPIPort,
			sessionId),
		Path:   path,
		Reused: false,
	}, nil
}

// provisionPath notifica relay + egress nel path
func (sm *SessionManager) provisionPath(
	ctx context.Context,
	treeId string,
	sessionId string,
	audioSsrc int,
	videoSsrc int,
	path []string,
) error {
	relayNodes := ExtractRelayNodes(path)
	log.Printf("[ProvisionPath] Configuring %d relay nodes", len(relayNodes))

	for _, relayId := range relayNodes {
		// Identifica il target (Next Hop ID)
		nextHop, err := GetNextHop(path, relayId)
		if err != nil {
			return fmt.Errorf("failed to get next hop for %s: %w", relayId, err)
		}
		// Verifica se sessione attiva
		nodeSessions, _ := sm.redis.GetNodeSessions(ctx, treeId, relayId)
		alreadyActive := false
		for _, s := range nodeSessions {
			if s == sessionId {
				alreadyActive = true
				break
			}
		}
		if alreadyActive {
			// CASO A: ROUTE-ADDED (Solo ID)
			log.Printf("[ProvisionPath] Relay %s active -> Adding route ID: %s", relayId, nextHop)
			if err := sm.redis.PublishRouteAdded(ctx, treeId, relayId, sessionId, nextHop); err != nil {
				return err
			}
		} else {
			// CASO B: SESSION-CREATED (Full Info)

			// Recupera info da Redis
			nextNodeInfo, err := sm.redis.GetNodeProvisioning(ctx, treeId, nextHop)
			if err != nil {
				log.Printf("[WARN] Node info not found for %s", nextHop)
			}

			// Crea oggetto Route
			routeFull := redis.Route{
				TargetId:  nextHop,
				Host:      nextNodeInfo.InternalHost,
				AudioPort: nextNodeInfo.InternalRTPAudio,
				VideoPort: nextNodeInfo.InternalRTPVideo,
			}

			log.Printf("[ProvisionPath] Relay %s new -> Session created with full route to %s", relayId, routeFull.Host)

			// Invia evento con array di rotte
			routes := []redis.Route{routeFull}
			if err := sm.redis.PublishNodeSessionCreated(ctx, treeId, relayId, sessionId, audioSsrc, videoSsrc, routes); err != nil {
				return err
			}
			// Persistenza
			sm.redis.AddSessionToNode(ctx, treeId, relayId, sessionId)
		}
		// Persistenza
		sm.redis.AddRoute(ctx, treeId, sessionId, relayId, nextHop)

	}

	// Egress
	egressId := path[len(path)-1]
	if err := sm.redis.PublishNodeSessionCreated(ctx, treeId, egressId, sessionId, audioSsrc, videoSsrc, nil); err != nil {
		return err
	}
	sm.redis.AddSessionToNode(ctx, treeId, egressId, sessionId)
	return nil
}

// createInjectionSession chiama HTTP API injection node
func (sm *SessionManager) createInjectionSession(
	ctx context.Context,
	treeId string,
	injectionNodeId string,
	sessionId string,
	roomId int,
	audioSsrc int,
	videoSsrc int,
) (*InjectionSessionResponse, error) {
	// Get node info per API endpoint
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

// ListSessions lista tutte le sessioni di un tree
func (sm *SessionManager) ListSessions(
	ctx context.Context,
	treeId string,
) ([]*SessionSummary, error) {
	log.Printf("[SessionManager] Listing sessions for tree %s", treeId)

	sessionIds, err := sm.redis.GetTreeSessions(ctx, treeId)
	if err != nil {
		return nil, fmt.Errorf("failed to get tree sessions:  %w", err)
	}

	// Get details for each session
	summaries := make([]*SessionSummary, 0, len(sessionIds))

	for _, sessionId := range sessionIds {
		session, err := sm.redis.GetSession(ctx, treeId, sessionId)
		if err != nil {
			log.Printf("[WARN] Failed to get session %s: %v", sessionId, err)
			continue
		}

		// Parse timestamp
		createdAt := time.Now()
		if createdAtStr := session["createdAt"]; createdAtStr != "" {
			if ts := parseInt64(createdAtStr); ts > 0 {
				createdAt = time.UnixMilli(ts)
			}
		}

		isActive := false
		if val, ok := session["active"]; ok {
			// Se è "1" -> true
			// Se è "true" -> true
			// Se è "TRUE" -> true
			if b, err := strconv.ParseBool(val); err == nil {
				isActive = b
			}
		}
		summaries = append(summaries, &SessionSummary{
			SessionId:       sessionId,
			TreeId:          treeId,
			InjectionNodeId: session["injectionNodeId"],
			Active:          isActive,
			CreatedAt:       createdAt,
		})
	}

	log.Printf("[SessionManager] Found %d sessions for tree %s", len(summaries), treeId)
	return summaries, nil
}

// Helpers

func parseInt(s string) int {
	var i int
	fmt.Sscanf(s, "%d", &i)
	return i
}

func parseInt64(s string) int64 {
	var i int64
	fmt.Sscanf(s, "%d", &i)
	return i
}

// GetSessionDetails legge dettagli sessione completi
// Usato da GET /api/trees/:treeId/sessions/:sessionId
func (sm *SessionManager) GetSessionDetails(
	ctx context.Context,
	treeId string,
	sessionId string,
) (*SessionInfo, error) {
	log.Printf("[SessionManager] Getting session details %s", sessionId)

	// Get session metadata
	session, err := sm.redis.GetSession(ctx, treeId, sessionId)
	if err != nil {
		return nil, fmt.Errorf("session not found: %w", err)
	}
	// Parse metadata
	injectionId := session["injectionNodeId"]
	audioSsrc := parseInt(session["audioSsrc"])
	videoSsrc := parseInt(session["videoSsrc"])
	roomId := parseInt(session["roomId"])
	// Get injection node info
	injectionNode, _ := sm.redis.GetNodeProvisioning(ctx, treeId, injectionId)
	whipEndpoint := fmt.Sprintf("http://%s:%d/whip/endpoint/%s",
		injectionNode.InternalHost,
		injectionNode.InternalAPIPort,
		sessionId,
	)
	// Parse timestamp
	createdAt := time.Now()
	if createdAtStr := session["createdAt"]; createdAtStr != "" {
		if ts := parseInt64(createdAtStr); ts > 0 {
			createdAt = time.UnixMilli(ts)
		}
	}

	return &SessionInfo{
		SessionId:       sessionId,
		TreeId:          treeId,
		InjectionNodeId: injectionId,
		AudioSsrc:       audioSsrc,
		VideoSsrc:       videoSsrc,
		RoomId:          roomId,
		WhipEndpoint:    whipEndpoint,
		Active:          session["active"] == "true",
		CreatedAt:       createdAt,
	}, nil
}

// DestroySessionComplete distrugge tutta la sessione (tutti i path)
func (sm *SessionManager) DestroySessionComplete(
	ctx context.Context,
	treeId string,
	sessionId string,
) error {
	log.Printf("[SessionManager] Destroying entire session: %s", sessionId)

	// Get tutti gli egress
	egresses, err := sm.redis.GetSessionEgresses(ctx, treeId, sessionId)
	if err != nil {
		log.Printf("[WARN] Failed to get egresses: %v", err)
		egresses = []string{} // Continua comunque
	}

	// Distruggi ogni path
	for _, egressId := range egresses {
		if err := sm.DestroySessionPath(ctx, treeId, sessionId, egressId); err != nil {
			log.Printf("[WARN] Failed to destroy path %s: %v", egressId, err)
			// Continua con gli altri
		}
	}

	// Distruggi injection
	sessionData, err := sm.redis.GetSession(ctx, treeId, sessionId)
	if err == nil {
		injectionId := sessionData["injectionNodeId"]
		sm.redis.RemoveSessionFromNode(ctx, treeId, injectionId, sessionId)
		sm.redis.PublishNodeSessionDestroyed(ctx, treeId, injectionId, sessionId)
	}

	// Cleanup metadata principale
	sm.redis.DeleteSession(ctx, treeId, sessionId)
	sm.redis.RemoveSessionFromTree(ctx, treeId, sessionId)

	log.Printf("[SessionManager] Session %s destroyed completely", sessionId)
	return nil
}

// DestroySessionPath distrugge solo un path specifico (Backtracking)
func (sm *SessionManager) DestroySessionPath(
	ctx context.Context,
	treeId string,
	sessionId string,
	egressId string,
) error {
	log.Printf("[SessionManager] Destroying path: session=%s, egress=%s", sessionId, egressId)

	// Get path da distruggere
	path, err := sm.redis.GetSessionPath(ctx, treeId, sessionId, egressId)
	if err != nil {
		return fmt.Errorf("path not found: %w", err)
	}

	// Distruggi l'Egress
	log.Printf("[DestroyPath] Destroying egress node %s", egressId)
	sm.redis.RemoveSessionFromNode(ctx, treeId, egressId, sessionId)
	sm.redis.PublishNodeSessionDestroyed(ctx, treeId, egressId, sessionId)
	sm.redis.RemoveEgressFromSession(ctx, treeId, sessionId, egressId)

	// Rimuovi la chiave del path salvato
	pathKey := fmt.Sprintf("tree:%s:session:%s:path:%s", treeId, sessionId, egressId)
	sm.redis.Del(ctx, pathKey)

	// BACKTRACKING: Risali il path dai Relay verso la Root
	currentTargetId := egressId

	for i := len(path) - 2; i >= 1; i-- {
		relayId := path[i]

		log.Printf("[DestroyPath] Checking relay %s (removing route to %s)", relayId, currentTargetId)

		// Rimuovi la rotta verso il target corrente
		err := sm.redis.RemoveRoute(ctx, treeId, sessionId, relayId, currentTargetId)
		if err != nil {
			log.Printf("[WARN] Failed to remove route on %s: %v", relayId, err)
		}

		// Controlla quante rotte sono rimaste attive su questo relay
		remainingRoutes, err := sm.redis.GetRoutes(ctx, treeId, sessionId, relayId)
		routeCount := len(remainingRoutes)
		if err != nil {
			log.Printf("[WARN] Failed to get routes for %s: %v", relayId, err)
			routeCount = 0
		}

		if routeCount > 0 {
			// CASO 1: Il Relay serve ancora qualcun altro
			log.Printf("[DestroyPath] Relay %s still has %d routes. Keeping session.", relayId, routeCount)
			// Notifica solo la rimozione della rotta specifica
			sm.redis.PublishRouteRemoved(ctx, treeId, relayId, sessionId, currentTargetId)

			break
		} else {
			// CASO 2: Il Relay non ha più rotte
			log.Printf("[DestroyPath] Relay %s is empty. Destroying session.", relayId)

			// Distruggi sessione sul relay
			sm.redis.RemoveSessionFromNode(ctx, treeId, relayId, sessionId)
			sm.redis.PublishNodeSessionDestroyed(ctx, treeId, relayId, sessionId)

			// Il relay corrente diventa il target da rimuovere al prossimo giro del loop
			currentTargetId = relayId
		}
	}

	log.Printf("[SessionManager] Path destroyed: %s -> %s", sessionId, egressId)
	return nil
}
