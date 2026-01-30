package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"controller/internal/session"
	"controller/internal/tree"
)

type SessionHandler struct {
	sessionManager *session.SessionManager
	treeManager    *tree.TreeManager
}

func NewSessionHandler(sessionMgr *session.SessionManager, treeMgr *tree.TreeManager) *SessionHandler {
	return &SessionHandler{
		sessionManager: sessionMgr,
		treeManager:    treeMgr,
	}
}

// POST /api/sessions
// Crea sessione broadcaster
func (h *SessionHandler) CreateSession(c *gin.Context) {
	var req session.CreateSessionRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.TreeId == "" {
		// Recupera il primo albero disponibile (per ora)
		summaries, _ := h.treeManager.ListTrees(c.Request.Context())
		if len(summaries) > 0 {
			req.TreeId = summaries[0].TreeId
		} else {
			c.JSON(http.StatusBadRequest, gin.H{"error": "no trees available, please create one first"})
			return
		}
	}
	sessionInfo, err := h.sessionManager.CreateSession(
		c.Request.Context(),
		req.SessionId,
		req.TreeId,
	)
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
	treeId := c.Query("treeId")

	if sessionId == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "sessionId is required"})
		return
	}

	if treeId == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "treeId query parameter is required"})
		return
	}
	//  Provisiona viewer on-demand
	viewerInfo, err := h.sessionManager.ProvisionViewer(
		c.Request.Context(),
		treeId,
		sessionId,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// Ritorna JSON (frontend fa redirect)
	c.JSON(http.StatusOK, viewerInfo)
}

// GET /api/sessions?treeId=X
// Lista sessioni per tree
func (h *SessionHandler) ListSessions(c *gin.Context) {
	treeId := c.Query("treeId")
	if treeId == "" {
		treeId = c.Param("treeId")
	}

	if treeId == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "treeId is required (query or path param)"})
		return
	}

	sessions, err := h.sessionManager.ListSessions(c.Request.Context(), treeId)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, sessions)
}

// GET /api/trees/:treeId/sessions/: sessionId
// Dettagli sessione
func (h *SessionHandler) GetSession(c *gin.Context) {
	treeId := c.Param("treeId")
	sessionId := c.Param("sessionId")

	if treeId == "" || sessionId == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "treeId and sessionId are required"})
		return
	}

	sessionInfo, err := h.sessionManager.GetSessionDetails(c.Request.Context(), treeId, sessionId)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, sessionInfo)
}

// DestroySessionPath distrugge solo un path (egress specifico)
func (h *SessionHandler) DestroySessionPath(c *gin.Context) {
	treeId := c.Param("treeId")
	sessionId := c.Param("sessionId")
	egressId := c.Param("egressId")

	if treeId == "" || sessionId == "" || egressId == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "treeId, sessionId and egressId are required",
		})
		return
	}

	if err := h.sessionManager.DestroySessionPath(
		c.Request.Context(),
		treeId,
		sessionId,
		egressId,
	); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":    "pathDestroyed",
		"sessionId": sessionId,
		"egressId":  egressId,
	})
}

// DestroySession distrugge intera sessione (tutti i path)
func (h *SessionHandler) DestroySession(c *gin.Context) {
	treeId := c.Param("treeId")
	sessionId := c.Param("sessionId")

	if treeId == "" || sessionId == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "treeId and sessionId are required",
		})
		return
	}

	// Chiama DestroySessionComplete
	if err := h.sessionManager.DestroySessionComplete(
		c.Request.Context(),
		treeId,
		sessionId,
	); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":    "destroyed",
		"sessionId": sessionId,
		"treeId":    treeId,
	})
}
