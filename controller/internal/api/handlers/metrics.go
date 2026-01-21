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

// GET /api/metrics/:treeId/:nodeId
func (h *MetricsHandler) GetNodeMetrics(c *gin.Context) {
	treeID := c.Param("treeId")
	nodeID := c.Param("nodeId")

	metrics, err := h.redisClient.GetNodeMetrics(c.Request.Context(), treeID, nodeID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "metrics not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"treeId":  treeID,
		"nodeId":  nodeID,
		"metrics": metrics,
	})
}

// GET /api/metrics/:treeId
func (h *MetricsHandler) GetTreeMetrics(c *gin.Context) {
	treeID := c.Param("treeId")

	nodes, err := h.redisClient.GetAllProvisionedNodes(c.Request.Context(), treeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	result := make(map[string]any)
	for _, node := range nodes {
		metrics, err := h.redisClient.GetNodeMetrics(c.Request.Context(), treeID, node.NodeId)
		if err != nil {
			continue
		}
		result[node.NodeId] = metrics
	}

	c.JSON(http.StatusOK, gin.H{
		"treeId":  treeID,
		"metrics": result,
	})
}
