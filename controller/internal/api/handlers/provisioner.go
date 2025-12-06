package handlers

import (
	"fmt"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"

	"controller/internal/domain"
	"controller/internal/provisioner"
	"controller/internal/redis"
)

type ProvisionerHandler struct {
	provisioner *provisioner.DockerProvisioner
	redisClient *redis.Client
}

func NewProvisionerHandler(networkName string, redisClient *redis.Client) (*ProvisionerHandler, error) {
	prov, err := provisioner.NewDockerProvisioner(networkName, redisClient)
	if err != nil {
		return nil, err
	}

	return &ProvisionerHandler{
		provisioner: prov,
		redisClient: redisClient,
	}, nil
}

// TestCreateInjection crea injection node di test
func (h *ProvisionerHandler) TestCreateInjection(c *gin.Context) {
	ctx := c.Request.Context()

	spec := domain.NodeSpec{
		NodeId:   "test-injection-1",
		NodeType: domain.NodeTypeInjection,
		TreeId:   "test-tree-1",
		Layer:    0,
	}

	nodeInfo, err := h.provisioner.CreateNode(ctx, spec)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "created",
		"node":   nodeInfo,
	})
}

// TestCreateRelay crea relay node di test
func (h *ProvisionerHandler) TestCreateRelay(c *gin.Context) {
	ctx := c.Request.Context()

	spec := domain.NodeSpec{
		NodeId:   "test-relay-1",
		NodeType: domain.NodeTypeRelay,
		TreeId:   "test-tree-1",
		Layer:    1,
	}

	nodeInfo, err := h.provisioner.CreateNode(ctx, spec)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "created",
		"node":   nodeInfo,
	})
}

// TestCreateEgress crea egress node di test
func (h *ProvisionerHandler) TestCreateEgress(c *gin.Context) {
	ctx := c.Request.Context()

	spec := domain.NodeSpec{
		NodeId:   "test-egress-1",
		NodeType: domain.NodeTypeEgress,
		TreeId:   "test-tree-1",
		Layer:    2,
	}

	nodeInfo, err := h.provisioner.CreateNode(ctx, spec)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "created",
		"node":   nodeInfo,
	})
}

// TestCreateTree crea tree completo (injection + relay + egress)
func (h *ProvisionerHandler) TestCreateTree(c *gin.Context) {
	ctx := c.Request.Context()

	nodes := []*domain.NodeInfo{}

	// 1. Injection
	injectionSpec := domain.NodeSpec{
		NodeId:   "test-injection-1",
		NodeType: domain.NodeTypeInjection,
		TreeId:   "test-tree-1",
		Layer:    0,
	}
	injectionInfo, err := h.provisioner.CreateNode(ctx, injectionSpec)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "injection: " + err.Error()})
		return
	}
	nodes = append(nodes, injectionInfo)

	// 2. Relay
	relaySpec := domain.NodeSpec{
		NodeId:   "test-relay-1",
		NodeType: domain.NodeTypeRelay,
		TreeId:   "test-tree-1",
		Layer:    1,
	}
	relayInfo, err := h.provisioner.CreateNode(ctx, relaySpec)
	if err != nil {
		// Cleanup injection
		h.provisioner.DestroyNode(ctx, injectionInfo)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "relay: " + err.Error()})
		return
	}
	nodes = append(nodes, relayInfo)

	// 3. Egress
	egressSpec := domain.NodeSpec{
		NodeId:   "test-egress-1",
		NodeType: domain.NodeTypeEgress,
		TreeId:   "test-tree-1",
		Layer:    2,
	}
	egressInfo, err := h.provisioner.CreateNode(ctx, egressSpec)
	if err != nil {
		// Cleanup injection + relay
		h.provisioner.DestroyNode(ctx, injectionInfo)
		h.provisioner.DestroyNode(ctx, relayInfo)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "egress: " + err.Error()})
		return
	}
	nodes = append(nodes, egressInfo)

	c.JSON(http.StatusOK, gin.H{
		"status":      "created",
		"tree_id":     "test-tree-1",
		"nodes_count": len(nodes),
		"nodes":       nodes,
	})
}

// TestDestroyTree distrugge dinamicamente tutti i nodi di un albero
func (h *ProvisionerHandler) TestDestroyTree(c *gin.Context) {
	ctx := c.Request.Context()
	treeId := "test-tree-1"

	// Recupera tutti i nodi provisionati per questo albero
	//    (Usa la funzione che legge tree:ID:controller:node:*)
	nodes, err := h.redisClient.GetAllProvisionedNodes(ctx, treeId)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list nodes: " + err.Error()})
		return
	}

	if len(nodes) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"message": "No nodes found to destroy for tree " + treeId,
		})
		return
	}

	destroyed := []string{}
	errors := []string{}

	// Itera e distruggi
	for _, node := range nodes {
		if err := h.provisioner.DestroyNode(ctx, node); err != nil {
			log.Printf("Error destroying %s: %v", node.NodeId, err)
			errors = append(errors, fmt.Sprintf("%s: %v", node.NodeId, err))
		} else {
			destroyed = append(destroyed, node.NodeId)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"tree_id":   treeId,
		"found":     len(nodes),
		"destroyed": destroyed,
		"errors":    errors,
		"cleanup":   "performed global set cleanup",
	})
}

// TestListProvisioned lista tutti i nodi provisionati
func (h *ProvisionerHandler) TestListProvisioned(c *gin.Context) {
	ctx := c.Request.Context()
	treeId := c.DefaultQuery("tree_id", "test-tree-1")

	nodes, err := h.redisClient.GetAllProvisionedNodes(ctx, treeId)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"tree_id":     treeId,
		"nodes_count": len(nodes),
		"nodes":       nodes,
	})
}

// TestGetProvisionInfo ottiene provisioning info per nodo
func (h *ProvisionerHandler) TestGetProvisionInfo(c *gin.Context) {
	ctx := c.Request.Context()
	treeId := c.DefaultQuery("tree_id", "test-tree-1")
	nodeId := c.Param("nodeId")

	nodeInfo, err := h.redisClient.GetNodeProvisioning(ctx, treeId, nodeId)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"node": nodeInfo,
	})
}

// Close chiude provisioner
func (h *ProvisionerHandler) Close() error {
	return h.provisioner.Close()
}

// GetProvisioner ritorna il provisioner
func (h *ProvisionerHandler) GetProvisioner() provisioner.Provisioner {
	return h.provisioner
}
