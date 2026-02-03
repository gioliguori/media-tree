package handlers

import (
	"controller/internal/redis"
	"net/http"

	"github.com/gin-gonic/gin"
)

type MetricsHandler struct {
	redisClient *redis.Client
}

func NewMetricsHandler(redisClient *redis.Client) *MetricsHandler {
	return &MetricsHandler{redisClient: redisClient}
}

// GET /api/metrics/:nodeId
func (h *MetricsHandler) GetNodeMetrics(c *gin.Context) {
	nodeId := c.Param("nodeId")

	metrics, err := h.redisClient.GetNodeMetrics(c.Request.Context(), nodeId)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "metrics not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"nodeId":  nodeId,
		"metrics": metrics,
	})
}

// GET /api/metrics/global
func (h *MetricsHandler) GetGlobalMetrics(c *gin.Context) {

	nodes, err := h.redisClient.GetAllProvisionedNodes(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	result := make(map[string]any)
	for _, node := range nodes {
		metrics, err := h.redisClient.GetNodeMetrics(c.Request.Context(), node.NodeId)
		if err != nil {
			continue
		}
		result[node.NodeId] = metrics
	}

	c.JSON(http.StatusOK, gin.H{
		"metrics": result,
	})
}
