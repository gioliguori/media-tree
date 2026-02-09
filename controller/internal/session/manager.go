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
) (*SessionInfo, error) {
	log.Printf("[SessionManager] Creating  session: %s", sessionId)

	// Check session non esiste già
	exists, err := sm.redis.SessionExists(ctx, sessionId)
	if err != nil || exists {
		return nil, fmt.Errorf("session error or already exists")
	}

	// Seleziona Injection
	injectionId, err := sm.selector.SelectInjection(ctx, sessionId)
	if err != nil {
		return nil, fmt.Errorf("failed to select injection: %w", err)
	}

	log.Printf("[SessionManager] Selected injection:  %s", injectionId)

	// Get Relay root
	children, err := sm.redis.GetNodeChildren(ctx, injectionId)
	if err != nil || len(children) == 0 {
		return nil, fmt.Errorf("relay-root not found for injection %s", injectionId)
	}
	relayRootId := children[0]

	// Genera ssrc e roomId
	audioSsrc, videoSsrc, err := GenerateSSRCPair(ctx, sm.redis)
	if err != nil {
		return nil, fmt.Errorf("failed to generate SSRC: %w", err)
	}

	roomId, err := GenerateRoomId(ctx, sm.redis)
	if err != nil {
		return nil, fmt.Errorf("failed to generate room ID: %w", err)
	}

	log.Printf("[SessionManager] Generated SSRC: audio=%d, video=%d, room=%d",
		audioSsrc, videoSsrc, roomId)

	// Salva metadata sessione
	metadata := map[string]any{
		"sessionId":       sessionId,
		"injectionNodeId": injectionId,
		"relayRootId":     relayRootId,
		"audioSsrc":       audioSsrc,
		"videoSsrc":       videoSsrc,
		"roomId":          roomId,
		"active":          true,
		"createdAt":       time.Now().UnixMilli(),
	}
	if err := sm.redis.SaveSession(ctx, sessionId, metadata); err != nil {
		return nil, fmt.Errorf("failed to save session:  %w", err)
	}

	// Creiamo la chain iniziale
	if err := sm.redis.InitSessionChain(ctx, sessionId, relayRootId); err != nil {
		log.Printf("[WARN] Failed to initialize session chain in Redis: %v", err)
	}

	// Aggiungi all'indice globale e registra sul nodo injection
	sm.redis.AddSessionToGlobalIndex(ctx, sessionId)
	sm.redis.AddSessionToNode(ctx, injectionId, sessionId)

	log.Printf("[SessionManager] Session metadata saved to Redis")

	// Notifica Injection Node
	injectionResp, err := sm.createInjectionSession(ctx, injectionId, sessionId, roomId, audioSsrc, videoSsrc)
	if err != nil {
		// Rollback:   cleanup Redis
		sm.redis.ReleaseInjectionSlot(ctx, injectionId, sessionId)
		sm.redis.DeleteSession(ctx, sessionId)
		sm.redis.RemoveSessionFromGlobalIndex(ctx, sessionId)
		sm.redis.DeleteSessionChain(ctx, sessionId)
		return nil, fmt.Errorf("failed to create injection session: %w", err)
	}
	log.Printf("[SessionManager] Injection session created: %s", injectionResp.Endpoint)

	// Costruisci risposta
	// Get injection node info per WHIP endpoint
	injectionNode, err := sm.redis.GetNodeProvisioning(ctx, injectionId)
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
func (sm *SessionManager) ProvisionViewer(
	ctx context.Context,
	sessionId string,
) (*ViewSessionResponse, error) {
	log.Printf("[SessionManager] Provisioning viewer for session %s", sessionId)

	// Riuso egress node
	existingEgress, err := sm.redis.FindEgressServingSession(ctx, sessionId)
	if err == nil && len(existingEgress) > 0 {
		log.Printf("[SessionManager] Found %d existing egress:  %v", len(existingEgress), existingEgress)

		// Riusa egress se disponibile
		for _, egressId := range existingEgress {
			if sm.selector.CanAcceptViewer(ctx, egressId) {
				log.Printf("[SessionManager] Reusing egress %s (multicast)", egressId)

				egressNode, _ := sm.redis.GetNodeProvisioning(ctx, egressId)
				path, _ := sm.redis.GetSessionPath(ctx, sessionId, egressId)

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
	egressId, err := sm.selector.SelectBestEgressForSession(ctx, sessionId)
	if err != nil {
		return nil, fmt.Errorf("no egress available - scaling needed: %w", err)
	}

	log.Printf("[SessionManager] Selected new egress: %s", egressId)

	// Selezione Relay a cui collegare egress (Hole-filling o Deepening)
	relayId, isNewRelayAdded, err := sm.selector.SelectRelayForViewer(ctx, sessionId)
	if err != nil {
		return nil, err
	}

	// Recupero dati sessione
	sessionData, _ := sm.redis.GetSession(ctx, sessionId)
	audioSsrc := parseInt(sessionData["audioSsrc"])
	videoSsrc := parseInt(sessionData["videoSsrc"])

	// Caso scaling deep
	if isNewRelayAdded {
		chain, _ := sm.redis.GetSessionChain(ctx, sessionId)
		if len(chain) >= 2 {
			parentOfNewRelay := chain[len(chain)-2]
			newRelayInfo, _ := sm.redis.GetNodeProvisioning(ctx, relayId)
			routeToNewRelay := redis.Route{
				TargetId: relayId, Host: newRelayInfo.InternalHost,
				AudioPort: newRelayInfo.InternalRTPAudio, VideoPort: newRelayInfo.InternalRTPVideo,
			}

			// Verifica se il genitore ha già la sessione inizializzata
			parentSessions, _ := sm.redis.GetNodeSessions(ctx, parentOfNewRelay)
			alreadyInit := false
			for _, s := range parentSessions {
				if s == sessionId {
					alreadyInit = true
					break
				}
			}
			if !alreadyInit {
				// Il genitore non ha ancora la sessione
				sm.redis.PublishNodeSessionCreated(ctx, parentOfNewRelay, sessionId, audioSsrc, videoSsrc, []redis.Route{routeToNewRelay})
				sm.redis.AddSessionToNode(ctx, parentOfNewRelay, sessionId)
			} else {
				// Il genitore ha già la sessione, aggiungiamo solo la nuova rotta
				sm.redis.PublishRouteAdded(ctx, parentOfNewRelay, sessionId, relayId)
			}
			// Registriamo la rotta su Redis
			sm.redis.AddRoute(ctx, sessionId, parentOfNewRelay, relayId)
		}
	}

	// Costruisci path e salva info
	path, err := BuildPath(ctx, sm.redis, sessionId, relayId, egressId)
	if err != nil {
		return nil, fmt.Errorf("failed to build path: %w", err)
	}

	log.Printf("[SessionManager] Path:  %v", path)

	sm.redis.SetEgressParent(ctx, sessionId, egressId, relayId)
	sm.redis.AddEgressToSession(ctx, sessionId, egressId)
	sm.redis.SaveSessionPath(ctx, sessionId, egressId, path)

	// Notifica configurazione finale
	egressNode, _ := sm.redis.GetNodeProvisioning(ctx, egressId)

	routeToEgress := []redis.Route{{
		TargetId: egressId, Host: egressNode.InternalHost,
		AudioPort: egressNode.InternalRTPAudio, VideoPort: egressNode.InternalRTPVideo,
	}}

	if isNewRelayAdded {
		// Se è un nuovo salto, creiamo la sessione sul Relay
		sm.redis.PublishNodeSessionCreated(ctx, relayId, sessionId, audioSsrc, videoSsrc, routeToEgress)
		sm.redis.AddSessionToNode(ctx, relayId, sessionId)

	} else {
		// Se è un buco, aggiungiamo solo la rotta verso l'Egress
		sm.redis.PublishRouteAdded(ctx, relayId, sessionId, egressId)
	}
	sm.redis.AddRoute(ctx, sessionId, relayId, egressId)

	// Notifica Egress
	sm.redis.PublishNodeSessionCreated(ctx, egressId, sessionId, audioSsrc, videoSsrc, nil)

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

// DestroySessionComplete distrugge tutta la sessione (tutti i path)
func (sm *SessionManager) DestroySessionComplete(
	ctx context.Context,
	sessionId string,
) error {
	log.Printf("[SessionManager] Destroying entire session: %s", sessionId)

	// Recupera tutti gli Egress che stavano guardando questa sessione
	egresses, _ := sm.redis.GetSessionEgresses(ctx, sessionId)

	// Chiama DestroySessionPath per ognuno
	for _, eid := range egresses {
		sm.DestroySessionPath(ctx, sessionId, eid)
	}

	// Piccola pausa per permettere a Redis di processare i decrementi
	time.Sleep(100 * time.Millisecond)

	// Se la catena ha ancora nodi puliscili forzatamente
	chain, _ := sm.redis.GetSessionChain(ctx, sessionId)
	for _, nid := range chain {
		sm.redis.PublishNodeSessionDestroyed(ctx, nid, sessionId)
		if !strings.Contains(nid, "relay-root") {
			sm.redis.ReleaseDeepReserve(ctx, sessionId, nid)
		}
	}

	// Distruggi injection
	sessionData, _ := sm.redis.GetSession(ctx, sessionId)
	injectionId := sessionData["injectionNodeId"]
	relayRootId := sessionData["relayRootId"]

	// Cleanup Injection
	// facciamo in ReleaseInjectionSlot()
	// sm.redis.RemoveSessionFromNode(ctx, injectionId, sessionId)
	sm.redis.PublishNodeSessionDestroyed(ctx, injectionId, sessionId)
	sm.redis.ReleaseInjectionSlot(ctx, injectionId, sessionId)

	// Cleanup Relay-root
	sm.redis.PublishNodeSessionDestroyed(ctx, relayRootId, sessionId)
	sm.redis.RemoveSessionFromNode(ctx, relayRootId, sessionId)

	// Cleanup metadata principale
	sm.redis.DeleteSession(ctx, sessionId)
	sm.redis.RemoveSessionFromGlobalIndex(ctx, sessionId)
	sm.redis.DeleteSessionChain(ctx, sessionId)

	log.Printf("[SessionManager] Session %s destroyed completely", sessionId)
	return nil
}

// DestroySessionPath distrugge solo un path specifico (Backtracking)
func (sm *SessionManager) DestroySessionPath(
	ctx context.Context,
	sessionId string,
	egressId string,
) error {
	log.Printf("[SessionManager] Backtracking cleanup for session %s, egress %s", sessionId, egressId)

	relayId, err := sm.redis.GetEgressParent(ctx, sessionId, egressId)
	if err != nil {
		log.Printf("[WARN] Parent for egress %s not found", egressId)
		return nil
	}

	// Libera slot Edge
	sm.redis.ReleaseEdgeSlot(ctx, sessionId, relayId)
	sm.redis.RemoveEgressFromSession(ctx, sessionId, egressId)
	sm.redis.RemoveEgressParent(ctx, sessionId, egressId)
	sm.redis.Del(ctx, fmt.Sprintf("path:%s:%s", sessionId, egressId))

	// Notifica Nodi
	sm.redis.PublishNodeSessionDestroyed(ctx, egressId, sessionId)
	sm.redis.PublishRouteRemoved(ctx, relayId, sessionId, egressId)
	// Backtracking
	for {
		chain, _ := sm.redis.GetSessionChain(ctx, sessionId)
		if len(chain) <= 1 {
			break
		} // Arrivati al Relay Root, stop.

		lastNodeId := chain[len(chain)-1]
		edgeCount, _ := sm.redis.GetEdgeCount(ctx, sessionId, lastNodeId)

		// Il nodo è un ramo morto se: non ha più viewer locali ed è l'ultimo della fila
		if edgeCount == 0 {
			log.Printf("[SessionManager] Pruning dead branch: %s", lastNodeId)

			parentId := chain[len(chain)-2]

			// Notifica i nodi
			sm.redis.PublishRouteRemoved(ctx, parentId, sessionId, lastNodeId)
			sm.redis.PublishNodeSessionDestroyed(ctx, lastNodeId, sessionId)

			// Libera slot Deep Reserve e pulisce catena
			sm.redis.ReleaseDeepReserve(ctx, sessionId, lastNodeId)
			sm.redis.RemoveFromSessionChain(ctx, sessionId, lastNodeId)

			// Continua il ciclo per vedere se anche il parent è diventato inutile
		} else {
			break // Il nodo serve ancora, stop backtracking.
		}
	}
	return nil
}

// Helpers

// createInjectionSession chiama HTTP API injection node
func (sm *SessionManager) createInjectionSession(
	ctx context.Context,
	injectionNodeId string,
	sessionId string,
	roomId int,
	audioSsrc int,
	videoSsrc int,
) (*InjectionSessionResponse, error) {
	// Get node info per API endpoint
	nodeInfo, err := sm.redis.GetNodeProvisioning(ctx, injectionNodeId)
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
) ([]*SessionSummary, error) {
	log.Printf("[SessionManager] Listing sessions")

	sessionIds, err := sm.redis.GetGlobalSessions(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get tree sessions:  %w", err)
	}

	// Get details for each session
	summaries := make([]*SessionSummary, 0, len(sessionIds))

	for _, sessionId := range sessionIds {
		session, err := sm.redis.GetSession(ctx, sessionId)
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
			InjectionNodeId: session["injectionNodeId"],
			Active:          isActive,
			CreatedAt:       createdAt,
		})
	}

	log.Printf("[SessionManager] Found %d sessions ", len(summaries))
	return summaries, nil
}

// GetSessionDetails legge dettagli sessione completi
// Usato da GET /api/trees/:treeId/sessions/:sessionId
func (sm *SessionManager) GetSessionDetails(
	ctx context.Context,
	sessionId string,
) (*SessionInfo, error) {
	log.Printf("[SessionManager] Getting session details %s", sessionId)

	// Get session metadata
	session, err := sm.redis.GetSession(ctx, sessionId)
	if err != nil {
		return nil, fmt.Errorf("session not found: %w", err)
	}
	// Parse metadata
	injectionId := session["injectionNodeId"]
	audioSsrc := parseInt(session["audioSsrc"])
	videoSsrc := parseInt(session["videoSsrc"])
	roomId := parseInt(session["roomId"])
	// Get injection node info per ricostruire l'endpoint
	injectionNode, err := sm.redis.GetNodeProvisioning(ctx, injectionId)
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
		InjectionNodeId: injectionId,
		AudioSsrc:       audioSsrc,
		VideoSsrc:       videoSsrc,
		RoomId:          roomId,
		WhipEndpoint:    whipEndpoint,
		Active:          session["active"] == "true",
		CreatedAt:       createdAt,
	}, nil
}
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
