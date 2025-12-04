package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"controller/internal/redis"
)

type HealthHandler struct {
	redisClient *redis.Client
}

func NewHealthHandler(redisClient *redis.Client) *HealthHandler {
	return &HealthHandler{
		redisClient: redisClient,
	}
}

// Health verifica se il processo è vivo
// GET /health
func (h *HealthHandler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":    "ok",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

// Ready verifica se il controller è pronto a servire richieste
// GET /ready
func (h *HealthHandler) Ready(c *gin.Context) {
	// Verifica connessione Redis
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()

	if err := h.redisClient.Ping(ctx); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status":    "not_ready",
			"error":     "redis connection failed",
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		})
		return
	}

	// Tutto ok
	c.JSON(http.StatusOK, gin.H{
		"status":    "ready",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"checks": gin.H{
			"redis": "ok",
		},
	})
}

func (h *HealthHandler) TestRedis(c *gin.Context) {
	ctx := c.Request.Context()

	// Leggi tutti i nodi di tree-1
	nodes, err := h.redisClient.GetAllTreeNodes(ctx, "tree-1")
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	c.JSON(200, gin.H{
		"nodes": nodes,
		"count": len(nodes),
	})
}

func (h *HealthHandler) TestTopology(c *gin.Context) {
	ctx := c.Request.Context()
	treeId := "tree-1"

	// Leggi topologia esistente
	injectionChildren, _ := h.redisClient.GetNodeChildren(ctx, treeId, "injection-1")
	relayParent, _ := h.redisClient.GetNodeParent(ctx, treeId, "relay-1")
	relayChildren, _ := h.redisClient.GetNodeChildren(ctx, treeId, "relay-1")
	egressParent, _ := h.redisClient.GetNodeParent(ctx, treeId, "egress-1")

	c.JSON(200, gin.H{
		"injection-1": gin.H{
			"children": injectionChildren,
		},
		"relay-1": gin.H{
			"parent":   relayParent,
			"children": relayChildren,
		},
		"egress-1": gin.H{
			"parent": egressParent,
		},
	})
}
