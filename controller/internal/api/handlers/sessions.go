package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"controller/internal/redis"
	"controller/internal/session"
	"controller/internal/tree"
)

type SessionHandler struct {
	sessionManager *session.SessionManager
}

func NewSessionHandler(
	redisClient *redis.Client,
	treeManager *tree.TreeManager,
) *SessionHandler {
	return &SessionHandler{
		sessionManager: session.NewSessionManager(redisClient, treeManager),
	}
}

// POST /api/sessions
func (h *SessionHandler) CreateSession(c *gin.Context) {
	var req session.CreateSessionRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	sessionInfo, err := h.sessionManager.CreateSession(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, sessionInfo)
}

// DELETE /api/trees/:tree_id/sessions/:session_id
func (h *SessionHandler) DestroySession(c *gin.Context) {
	treeId := c.Param("tree_id")
	sessionId := c.Param("session_id")

	if treeId == "" || sessionId == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "tree_id and session_id are required"})
		return
	}

	if err := h.sessionManager.DestroySession(c.Request.Context(), treeId, sessionId); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "destroyed", "session_id": sessionId})
}

// GET /api/trees/:tree_id/sessions/:session_id
func (h *SessionHandler) GetSession(c *gin.Context) {
	treeId := c.Param("tree_id")
	sessionId := c.Param("session_id")

	if treeId == "" || sessionId == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "tree_id and session_id are required"})
		return
	}

	sessionInfo, err := h.sessionManager.GetSession(c.Request.Context(), treeId, sessionId)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	// if sessionInfo == nil {
	// 	log.Printf("[DEBUG] sessionInfo è NIL!")
	// } else {
	// 	log.Printf("[DEBUG] Sto inviando sessionInfo: %+v", sessionInfo)
	// }
	c.JSON(http.StatusOK, sessionInfo)
}

// GET /api/trees/:tree_id/sessions
func (h *SessionHandler) ListSessions(c *gin.Context) {
	treeId := c.Param("tree_id")

	sessions, err := h.sessionManager.ListSessions(c.Request.Context(), treeId)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"tree_id":  treeId,
		"sessions": sessions,
		"count":    len(sessions),
	})
}
