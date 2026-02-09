package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"controller/internal/domain"
	"controller/internal/tree"
)

type NodeHandler struct {
	nodeManager *tree.TreeManager
}

func NewNodeHandler(manager *tree.TreeManager) *NodeHandler {
	return &NodeHandler{
		nodeManager: manager,
	}
}

// GET /api/nodes
func (h *NodeHandler) ListNodes(c *gin.Context) {
	nodes, err := h.nodeManager.ListNodes(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, nodes)
}

// POST /api/nodes
func (h *NodeHandler) CreateNode(c *gin.Context) {
	nodeType := c.Query("type") // injection, relay, egress
	role := c.DefaultQuery("role", "standalone")

	if nodeType == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "type query param is required"})
		return
	}

	nodes, err := h.nodeManager.CreateNode(c.Request.Context(), domain.NodeType(nodeType), role)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, nodes)
}

// DELETE /api/nodes/:nodeId
func (h *NodeHandler) DestroyNode(c *gin.Context) {
	nodeId := c.Param("nodeId")
	nodeType := c.Query("type") // injection, relay, egress

	if nodeType == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "type query param is required"})
		return
	}

	if err := h.nodeManager.DestroyNode(c.Request.Context(), nodeId, nodeType); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "destroyed", "nodeId": nodeId})
}
