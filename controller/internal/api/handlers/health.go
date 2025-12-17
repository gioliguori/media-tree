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
			"status":    "notReady",
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