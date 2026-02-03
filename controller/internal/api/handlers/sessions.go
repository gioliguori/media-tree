package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"controller/internal/session"
)

type SessionHandler struct {
	sessionManager *session.SessionManager
}

func NewSessionHandler(sessionMgr *session.SessionManager) *SessionHandler {
	return &SessionHandler{sessionManager: sessionMgr}
}

// POST /api/sessions
// Crea sessione broadcaster
func (h *SessionHandler) CreateSession(c *gin.Context) {
	var req session.CreateSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	sessionInfo, err := h.sessionManager.CreateSession(c.Request.Context(), req.SessionId)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, sessionInfo)
}

//	GET /api/sessions/:sessionId/view
//
// Provisiona egress on-demand per viewer
func (h *SessionHandler) ViewSession(c *gin.Context) {
	sessionId := c.Param("sessionId")

	viewerInfo, err := h.sessionManager.ProvisionViewer(c.Request.Context(), sessionId)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, viewerInfo)
}

// GET /api/sessions/:sessionId
// Ritorna i dettagli completi di una sessione (SSRC, RoomId, WHIP Endpoint)
func (h *SessionHandler) GetSession(c *gin.Context) {
	sessionId := c.Param("sessionId")

	if sessionId == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "sessionId is required"})
		return
	}

	sessionInfo, err := h.sessionManager.GetSessionDetails(c.Request.Context(), sessionId)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, sessionInfo)
}

// GET /api/sessions
func (h *SessionHandler) ListSessions(c *gin.Context) {
	sessions, err := h.sessionManager.ListSessions(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, sessions)
}

// DELETE /api/sessions/:sessionId
func (h *SessionHandler) DestroySession(c *gin.Context) {
	sessionId := c.Param("sessionId")

	if err := h.sessionManager.DestroySessionComplete(c.Request.Context(), sessionId); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "destroyed", "sessionId": sessionId})
}

// DELETE /api/sessions/:sessionId/path/:egressId
// Rimuove un singolo percorso
func (h *SessionHandler) DestroySessionPath(c *gin.Context) {
	sessionId := c.Param("sessionId")
	egressId := c.Param("egressId")

	if sessionId == "" || egressId == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "sessionId and egressId are required"})
		return
	}

	if err := h.sessionManager.DestroySessionPath(c.Request.Context(), sessionId, egressId); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":    "path_destroyed",
		"sessionId": sessionId,
		"egressId":  egressId,
	})
}
