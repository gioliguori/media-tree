package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"controller/internal/provisioner"
	"controller/internal/redis"
	"controller/internal/tree"
)

type TreeHandler struct {
	treeManager *tree.TreeManager
}

func NewTreeHandler(redisClient *redis.Client, prov provisioner.Provisioner) *TreeHandler {
	return &TreeHandler{
		treeManager: tree.NewTreeManager(redisClient, prov),
	}
}

// POST /trees
func (h *TreeHandler) CreateTree(c *gin.Context) {
	var req struct {
		TreeId   string `json:"tree_id" binding:"required"`
		Template string `json:"template" binding:"required"`
	}

	// Validazione input
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	tree, err := h.treeManager.CreateTree(c.Request.Context(), req.TreeId, req.Template)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, tree)
}

// GET /trees/:id
func (h *TreeHandler) GetTree(c *gin.Context) {
	treeId := c.Param("id")

	tree, err := h.treeManager.GetTree(c.Request.Context(), treeId)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, tree)
}

// DELETE /trees/:id
func (h *TreeHandler) DestroyTree(c *gin.Context) {
	treeId := c.Param("id")

	if err := h.treeManager.DestroyTree(c.Request.Context(), treeId); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "destroyed", "tree_id": treeId})
}

// GET /trees
func (h *TreeHandler) ListTrees(c *gin.Context) {
	trees, err := h.treeManager.ListTrees(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"trees": trees, "count": len(trees)})
}
