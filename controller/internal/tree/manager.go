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

// CRUD ALBERI

// CreateTree crea un albero da template
func (tm *TreeManager) CreateTree(ctx context.Context, treeId, templateName string) (*Tree, error) {
	log.Printf("[INFO] Creating tree %s with template %s", treeId, templateName)

	// Valida template
	tmpl, err := GetTemplate(templateName)
	if err != nil {
		return nil, fmt.Errorf("invalid template: %w", err)
	}

	// Check tree non esiste già
	exists, _ := tm.redis.TreeExists(ctx, treeId)
	if exists {
		return nil, fmt.Errorf("tree %s already exists", treeId)
	}

	// Salva metadata tree
	if err := tm.saveTreeMetadata(ctx, treeId, templateName, "creating"); err != nil {
		return nil, fmt.Errorf("failed to save tree metadata: %w", err)
	}

	// Crea nodi secondo template
	allNodes := make([]*domain.NodeInfo, 0)
	nodeCounter := make(map[string]int) // Contatore per tipo (injection-1, injection-2, etc.)

	for _, spec := range tmpl.Nodes {
		log.Printf("[INFO] Creating %d nodes of type %s at layer %d", spec.Count, spec.NodeType, spec.Layer)

		for i := 0; i < spec.Count; i++ {
			// Incrementa contatore per questo tipo
			nodeCounter[spec.NodeType]++
			counter := nodeCounter[spec.NodeType]

			// Genera nodeId: tree-1-injection-1, tree-1-relay-1, etc.
			nodeId := fmt.Sprintf("%s-%s-%d", treeId, spec.NodeType, counter)

			log.Printf("[INFO] Creating node %s (type=%s, layer=%d, #%d of %d)",
				nodeId, spec.NodeType, spec.Layer, i+1, spec.Count)

			// Crea nodo via provisioner
			node, err := tm.provisioner.CreateNode(ctx, domain.NodeSpec{
				NodeId:   nodeId,
				NodeType: domain.NodeType(spec.NodeType),
				TreeId:   treeId,
				Layer:    spec.Layer,
			})
			log.Printf("[INFO] Calling provisioner.CreateNode for %s...", nodeId)

			if err != nil {
				log.Printf("[ERROR] Failed to create node %s: %v", nodeId, err)
				log.Printf("[ROLLBACK] Cleaning up %d partial nodes", len(allNodes))
				// Rollback: distruggi nodi creati
				tm.cleanupPartialTree(ctx, treeId, allNodes)
				return nil, fmt.Errorf("failed to create node %s: %w", nodeId, err)
			}

			allNodes = append(allNodes, node)
			log.Printf("[INFO] Created node %s (layer %d)", nodeId, spec.Layer)
		}
	}

	log.Printf("[INFO] Created %d nodes for tree %s", len(allNodes), treeId)

	// Configura topologia Redis
	if err := tm.configureTopology(ctx, treeId, allNodes); err != nil {
		tm.cleanupPartialTree(ctx, treeId, allNodes)
		return nil, fmt.Errorf("failed to configure topology: %w", err)
	}

	// Pubblica eventi topologia
	if err := tm.publishTopologyEvents(ctx, treeId, allNodes); err != nil {
		log.Printf("[WARN] Failed to publish topology events: %v", err)
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
	createdAt := time.Unix(parseInt64(metadata["created_at"]), 0)

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

	// Check tree non esiste già
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

	// Raggruppa per layer (distruggi dal più alto al più basso)
	nodesByLayer := make(map[int][]*domain.NodeInfo)
	maxLayer := 0

	for _, node := range nodes {
		nodesByLayer[node.Layer] = append(nodesByLayer[node.Layer], node)
		if node.Layer > maxLayer {
			maxLayer = node.Layer
		}
	}

	// Distruggi layer per layer (dall'alto verso il basso)
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
	// 'KEYS' è lento su grandi DB. Servirebbe SET 'all_trees'.
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

		var injectionCount, relayCount, egressCount, maxLayer int

		for _, node := range nodes {
			switch node.NodeType {
			case domain.NodeTypeInjection:
				injectionCount++
			case domain.NodeTypeRelay:
				relayCount++
			case domain.NodeTypeEgress:
				egressCount++
			}

			if node.Layer > maxLayer {
				maxLayer = node.Layer
			}
		}

		summaries = append(summaries, &TreeSummary{
			TreeId:         treeId,
			Template:       metadata["template"],
			NodesCount:     len(nodes),
			InjectionCount: injectionCount,
			RelayCount:     relayCount,
			EgressCount:    egressCount,
			MaxLayer:       maxLayer,
			Status:         metadata["status"],
			CreatedAt:      time.Unix(parseInt64(metadata["created_at"]), 0),
			UpdatedAt:      time.Unix(parseInt64(metadata["updated_at"]), 0),
		})
	}

	return summaries, nil
}

// HELPER PRIVATI - REDIS

func (tm *TreeManager) saveTreeMetadata(ctx context.Context, treeId, template, status string) error {
	key := fmt.Sprintf("tree:%s:metadata", treeId)

	data := map[string]any{
		"tree_id":    treeId,
		"template":   template,
		"status":     status,
		"created_at": time.Now().Unix(),
		"updated_at": time.Now().Unix(),
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
	tm.redis.HSet(ctx, key, "updated_at", time.Now().Unix())
}

func (tm *TreeManager) cleanupTreeRedis(ctx context.Context, treeId string) {
	// Rimuovi metadata
	tm.redis.Del(ctx, fmt.Sprintf("tree:%s:metadata", treeId))

	// Rimuovi topologia (parents/children keys)
	// Anche qui problema KEYS
	for _, kind := range []string{"parents", "children"} {
		pattern := fmt.Sprintf("tree:%s:%s:*", treeId, kind)
		if keys, _ := tm.redis.Keys(ctx, pattern); len(keys) > 0 {
			for _, k := range keys {
				tm.redis.Del(ctx, k)
			}
		}
	}
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
