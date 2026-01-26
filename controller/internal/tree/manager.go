package tree

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"controller/internal/domain"
	"controller/internal/provisioner"
	"controller/internal/redis"
)

type TreeManager struct {
	redis       *redis.Client
	provisioner provisioner.Provisioner
}

func NewTreeManager(redis *redis.Client, prov provisioner.Provisioner) *TreeManager {
	return &TreeManager{
		redis:       redis,
		provisioner: prov,
	}
}

// CreateTree crea un albero da template
func (tm *TreeManager) CreateTree(ctx context.Context, treeId, templateName string) (*Tree, error) {
	log.Printf("[INFO] Creating tree %s with template %s", treeId, templateName)

	// Valida template
	tmpl, err := GetTemplate(templateName)
	if err != nil {
		return nil, fmt.Errorf("invalid template: %w", err)
	}

	minInjectionNodes := 0
	for _, node := range tmpl.Nodes {
		if node.NodeType == "injection" {
			minInjectionNodes += node.Count
		}
	}
	if minInjectionNodes < 1 {
		minInjectionNodes = 1
	} // Safety

	// Check tree non esiste già
	exists, _ := tm.redis.TreeExists(ctx, treeId)
	if exists {
		return nil, fmt.Errorf("tree %s already exists", treeId)
	}

	// Salva metadata tree
	if err := tm.saveTreeMetadata(ctx, treeId, templateName, "creating", minInjectionNodes); err != nil {
		return nil, fmt.Errorf("failed to save tree metadata: %w", err)
	}

	allNodes := make([]*domain.NodeInfo, 0)

	// Crea coppie Injection + RelayRoot
	// Ogni injection viene automaticamente accoppiato con un relay-root dedicato
	injectionSpecs := tmpl.GetNodesByType("injection")
	if len(injectionSpecs) == 0 {
		tm.cleanupPartialTree(ctx, treeId, allNodes)
		return nil, fmt.Errorf("template must have injection nodes")
	}

	for _, spec := range injectionSpecs {
		for i := 0; i < spec.Count; i++ {
			// Genera ID
			injId, err := tm.generateNodeID(ctx, treeId, "injection")
			if err != nil {
				tm.cleanupPartialTree(ctx, treeId, allNodes)
				return nil, err
			}

			rootId, err := tm.generateNodeID(ctx, treeId, "relay-root")
			if err != nil {
				tm.cleanupPartialTree(ctx, treeId, allNodes)
				return nil, err
			}

			createdNodes, err := tm.createInjectionPair(ctx, treeId, injId, rootId)
			if err != nil {
				tm.cleanupPartialTree(ctx, treeId, allNodes)
				return nil, err
			}

			allNodes = append(allNodes, createdNodes...)
		}
	}

	// Crea pool nodi (relay, egress)
	if err := tm.createPoolNodes(ctx, treeId, tmpl, &allNodes); err != nil {
		tm.cleanupPartialTree(ctx, treeId, allNodes)
		return nil, fmt.Errorf("failed to create pool nodes: %w", err)
	}

	// Aggiorna stato tree
	tm.updateTreeStatus(ctx, treeId, "active")

	log.Printf("[SUCCESS] Tree %s created successfully (%d nodes)", treeId, len(allNodes))

	return &Tree{
		TreeId:     treeId,
		Template:   templateName,
		Nodes:      allNodes,
		NodesCount: len(allNodes),
		CreatedAt:  time.Now(),
		Status:     "active",
	}, nil
}

// Crea 1 Injection + 1 RelayRoot, li collega e li mette nei pool
// Viene usato sia all'avvio (CreateTree) sia durante lo scaling (ScaleUpInjection)
func (tm *TreeManager) createInjectionPair(ctx context.Context, treeId, injId, rootId string) ([]*domain.NodeInfo, error) {
	log.Printf("[TreeManager] Provisioning pair: %s <-> %s", injId, rootId)

	created := []*domain.NodeInfo{}

	// Crea Injection
	injSpec := domain.NodeSpec{
		NodeId:   injId,
		NodeType: domain.NodeTypeInjection,
		TreeId:   treeId,
		Layer:    0,
	}
	injNode, err := tm.provisioner.CreateNode(ctx, injSpec)
	if err != nil {
		return nil, fmt.Errorf("failed to create injection %s: %w", injId, err)
	}
	created = append(created, injNode)

	// Crea Relay Root
	relaySpec := domain.NodeSpec{
		NodeId:   rootId,
		NodeType: domain.NodeTypeRelay,
		TreeId:   treeId,
		Layer:    0,
	}
	relayNode, err := tm.provisioner.CreateNode(ctx, relaySpec)
	if err != nil {
		// Rollback
		log.Printf("[WARN] Relay provisioning failed. Rolling back injection %s...", injId)
		_ = tm.provisioner.DestroyNode(ctx, injNode)
		return nil, fmt.Errorf("failed to create relay root %s: %w", rootId, err)
	}
	created = append(created, relayNode)

	//  Redis Topology (Parent/Child)
	if err := tm.redis.AddNodeChild(ctx, treeId, injId, rootId); err != nil {
		log.Printf("[WARN] Failed to link child: %v", err)
	}
	if err := tm.redis.AddNodeParent(ctx, treeId, rootId, injId); err != nil {
		log.Printf("[WARN] Failed to link parent: %v", err)
	}

	// Redis Pools
	if err := tm.redis.AddNodeToPool(ctx, treeId, "injection", 0, injId); err != nil {
		log.Printf("[WARN] Failed to add injection to pool: %v", err)
	}
	if err := tm.redis.AddNodeToPool(ctx, treeId, "relay", 0, rootId); err != nil {
		log.Printf("[WARN] Failed to add relay to pool: %v", err)
	}

	return created, nil
}

// createPoolNodes crea pool nodi
// Questi nodi non hanno topologia statica, vengono collegati on-demand durante provisioning sessioni
func (tm *TreeManager) createPoolNodes(
	ctx context.Context,
	treeId string,
	tmpl TemplateConfig,
	allNodes *[]*domain.NodeInfo,
) error {
	nodeCounter := make(map[string]int)

	for _, spec := range tmpl.Nodes {
		// Skip injection (già gestiti in createInjectionPair)
		if spec.NodeType == "injection" {
			continue
		}

		log.Printf("[INFO] Creating %d %s nodes at layer %d", spec.Count, spec.NodeType, spec.Layer)

		for i := 0; i < spec.Count; i++ {
			nodeCounter[spec.NodeType]++
			counter := nodeCounter[spec.NodeType]

			// Genera nodeId
			nodeId := fmt.Sprintf("%s-%d", spec.NodeType, counter)

			// Crea nodo via provisioner
			node, err := tm.provisioner.CreateNode(ctx, domain.NodeSpec{
				NodeId:   nodeId,
				NodeType: domain.NodeType(spec.NodeType),
				TreeId:   treeId,
				Layer:    spec.Layer,
			})
			if err != nil {
				return fmt.Errorf("failed to create node: %w", err)
			}
			*allNodes = append(*allNodes, node)

			// Aggiungi a pool
			if err := tm.redis.AddNodeToPool(ctx, treeId, spec.NodeType, spec.Layer, nodeId); err != nil {
				return fmt.Errorf("failed to add to pool: %w", err)
			}

			log.Printf("[INFO] Created pool node: %s (layer %d)", nodeId, spec.Layer)
		}
	}

	return nil
}

// GetTree legge un tree da Redis
func (tm *TreeManager) GetTree(ctx context.Context, treeId string) (*Tree, error) {
	// Leggi metadata
	metadata, err := tm.getTreeMetadata(ctx, treeId)
	if err != nil {
		return nil, fmt.Errorf("tree not found: %w", err)
	}

	if len(metadata) == 0 {
		return nil, fmt.Errorf("tree not found: %s", treeId)
	}

	// Leggi tutti i nodi
	nodes, err := tm.redis.GetAllProvisionedNodes(ctx, treeId)
	if err != nil {
		return nil, fmt.Errorf("failed to get nodes: %w", err)
	}

	// Parse timestamp
	createdAt := time.Unix(parseInt64(metadata["createdAt"]), 0)

	return &Tree{
		TreeId:     treeId,
		Template:   metadata["template"],
		Nodes:      nodes,
		NodesCount: len(nodes),
		CreatedAt:  createdAt,
		Status:     metadata["status"],
	}, nil
}

// DestroyTree distrugge un tree completo
func (tm *TreeManager) DestroyTree(ctx context.Context, treeId string) error {
	// Check tree esiste
	exists, _ := tm.redis.TreeExists(ctx, treeId)
	if !exists {
		return fmt.Errorf("tree %s not found", treeId)
	}

	log.Printf("[INFO] Destroying tree %s", treeId)

	// Aggiorna stato
	tm.updateTreeStatus(ctx, treeId, "destroying")

	// Leggi tutti i nodi
	nodes, err := tm.redis.GetAllProvisionedNodes(ctx, treeId)
	if err != nil {
		return fmt.Errorf("failed to get nodes: %w", err)
	}

	//log.Printf("[INFO] Found %d nodes to destroy", len(nodes))
	// Raggruppa per layer (distruggi dal più alto al più basso)
	nodesByLayer := make(map[int][]*domain.NodeInfo)
	maxLayer := 0

	for _, node := range nodes {
		nodesByLayer[node.Layer] = append(nodesByLayer[node.Layer], node)
		if node.Layer > maxLayer {
			maxLayer = node.Layer
		}
	}

	// Distruggi layer per layer
	var errs []error

	for layer := maxLayer; layer >= 0; layer-- {
		layerNodes := nodesByLayer[layer]

		log.Printf("[INFO] Destroying layer %d (%d nodes)", layer, len(layerNodes))

		for _, node := range layerNodes {
			if err := tm.provisioner.DestroyNode(ctx, node); err != nil {
				errs = append(errs, fmt.Errorf("node %s: %w", node.NodeId, err))
				log.Printf("[ERROR] Failed to destroy %s: %v", node.NodeId, err)
			} else {
				log.Printf("[INFO] Destroyed node %s", node.NodeId)
			}
		}
	}

	// Cleanup Redis (metadata + topologia)
	tm.cleanupTreeRedis(ctx, treeId)

	if len(errs) > 0 {
		return fmt.Errorf("failed to destroy some nodes: %v", errs)
	}

	log.Printf("[SUCCESS] Tree %s destroyed successfully", treeId)
	return nil
}

// ListTrees lista tutti gli alberi
func (tm *TreeManager) ListTrees(ctx context.Context) ([]*TreeSummary, error) {
	// Cerca pattern tree:*:metadata
	// KEYS è lento su grandi DB.  Servirebbe SET 'allTrees'
	pattern := "tree:*:metadata"
	keys, err := tm.redis.Keys(ctx, pattern)
	if err != nil {
		return nil, fmt.Errorf("failed to list trees: %w", err)
	}

	summaries := make([]*TreeSummary, 0, len(keys))

	for _, key := range keys {
		// Estrai treeId da key (tree:{treeId}:metadata)
		treeId := extractTreeIdFromKey(key)
		if treeId == "" {
			continue
		}

		metadata, err := tm.redis.HGetAll(ctx, key)
		if err != nil {
			continue
		}

		// Conta nodi
		nodes, _ := tm.redis.GetAllProvisionedNodes(ctx, treeId)

		var injectionCount, relayCount, egressCount int

		for _, node := range nodes {
			switch node.NodeType {
			case domain.NodeTypeInjection:
				injectionCount++
			case domain.NodeTypeRelay:
				relayCount++
			case domain.NodeTypeEgress:
				egressCount++
			}
		}

		currentMaxLayer := GetCurrentMaxLayer(nodes)

		summaries = append(summaries, &TreeSummary{
			TreeId:          treeId,
			Template:        metadata["template"],
			NodesCount:      len(nodes),
			InjectionCount:  injectionCount,
			RelayCount:      relayCount,
			EgressCount:     egressCount,
			CurrentMaxLayer: currentMaxLayer,
			Status:          metadata["status"],
			CreatedAt:       time.Unix(parseInt64(metadata["createdAt"]), 0),
			UpdatedAt:       time.Unix(parseInt64(metadata["updatedAt"]), 0),
		})
	}

	return summaries, nil
}

// HELPER PRIVATI - REDIS

func (tm *TreeManager) saveTreeMetadata(ctx context.Context, treeId, template, status string, minNodes int) error {
	key := fmt.Sprintf("tree:%s:metadata", treeId)

	data := map[string]any{
		"treeId":            treeId,
		"template":          template,
		"status":            status,
		"minInjectionNodes": minNodes,
		"createdAt":         time.Now().Unix(),
		"updatedAt":         time.Now().Unix(),
	}

	return tm.redis.HMSet(ctx, key, data)
}

func (tm *TreeManager) getTreeMetadata(ctx context.Context, treeId string) (map[string]string, error) {
	key := fmt.Sprintf("tree:%s:metadata", treeId)
	return tm.redis.HGetAll(ctx, key)
}

func (tm *TreeManager) updateTreeStatus(ctx context.Context, treeId, status string) {
	key := fmt.Sprintf("tree:%s:metadata", treeId)
	tm.redis.HSet(ctx, key, "status", status)
	tm.redis.HSet(ctx, key, "updatedAt", time.Now().Unix())
}

func (tm *TreeManager) cleanupTreeRedis(ctx context.Context, treeId string) {
	log.Printf("[INFO] Cleaning up ALL Redis keys for tree %s", treeId)

	// Pattern: tree:{treeId}: *
	pattern := fmt.Sprintf("tree:%s:*", treeId)
	keys, err := tm.redis.Keys(ctx, pattern)
	if err != nil {
		log.Printf("[WARN] Failed to get keys for cleanup: %v", err)
		return
	}

	log.Printf("[INFO] Found %d keys to cleanup for tree %s", len(keys), treeId)

	// Delete tutte le chiavi
	if len(keys) > 0 {
		for _, key := range keys {
			if err := tm.redis.Del(ctx, key); err != nil {
				log.Printf("[WARN] Failed to delete key %s: %v", key, err)
			}
		}
	}

	log.Printf("[INFO] Redis cleanup completed for tree %s (%d keys removed)", treeId, len(keys))
}

func (tm *TreeManager) cleanupPartialTree(ctx context.Context, treeId string, nodes []*domain.NodeInfo) {
	log.Printf("[WARN] Cleaning up partial tree %s", treeId)

	for _, node := range nodes {
		tm.provisioner.DestroyNode(ctx, node)
	}

	tm.cleanupTreeRedis(ctx, treeId)
}

func parseInt64(s string) int64 {
	var i int64
	fmt.Sscanf(s, "%d", &i)
	return i
}

func extractTreeIdFromKey(key string) string {
	// key = "tree:{treeId}:metadata"
	parts := strings.Split(key, ":")
	if len(parts) >= 2 {
		return parts[1]
	}
	return ""
}
