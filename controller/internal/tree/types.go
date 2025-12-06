package tree

import (
	"fmt"
	"time"

	"controller/internal/domain"
)

// Tree rappresenta un albero completo
type Tree struct {
	TreeId     string             `json:"tree_id"`
	Template   string             `json:"template"`
	Nodes      []*domain.NodeInfo `json:"nodes"`
	NodesCount int                `json:"nodes_count"`
	CreatedAt  time.Time          `json:"created_at"`
	Status     string             `json:"status"` // creating, active, unhealthy, destroying
}

// TreeSummary è un riassunto per listing
type TreeSummary struct {
	TreeId         string    `json:"tree_id"`
	Template       string    `json:"template"`
	NodesCount     int       `json:"nodes_count"`
	InjectionCount int       `json:"injection_count"`
	RelayCount     int       `json:"relay_count"`
	EgressCount    int       `json:"egress_count"`
	MaxLayer       int       `json:"max_layer"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// TreeStats statistiche tree
type TreeStats struct {
	TreeId         string `json:"tree_id"`
	TotalNodes     int    `json:"total_nodes"`
	HealthyNodes   int    `json:"healthy_nodes"`
	UnhealthyNodes int    `json:"unhealthy_nodes"`
	Layers         int    `json:"layers"`
	MaxFanout      int    `json:"max_fanout"` // Max children per nodo
	// Per tipo
	InjectionCount int `json:"injection_count"`
	RelayCount     int `json:"relay_count"`
	EgressCount    int `json:"egress_count"`
	// Per layer
	NodesByLayer map[int]int `json:"nodes_by_layer"` // layer -> count
}

// TreeHealth stato salute tree
type TreeHealth struct {
	TreeId     string          `json:"tree_id"`
	Healthy    bool            `json:"healthy"`
	Issues     []string        `json:"issues,omitempty"`
	NodeHealth map[string]bool `json:"node_health"`
	CheckedAt  time.Time       `json:"checked_at"`
}

// TreeStatus stato generale
type TreeStatus struct {
	TreeId    string    `json:"tree_id"`
	Status    string    `json:"status"`
	Nodes     int       `json:"nodes"`
	Sessions  int       `json:"sessions"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TemplateConfig configurazione template
type TemplateConfig struct {
	Name        string             `json:"name"`
	Description string             `json:"description"`
	Nodes       []TemplateNodeSpec `json:"nodes"`
}

// Specifica per creazione nodi nel template
type TemplateNodeSpec struct {
	NodeType string `json:"node_type"`
	Layer    int    `json:"layer"`
	Count    int    `json:"count"`
}

// ValidateTemplate
func (tc *TemplateConfig) Validate() error {
	if tc.Name == "" {
		return fmt.Errorf("template name is required")
	}

	if len(tc.Nodes) == 0 {
		return fmt.Errorf("template must have at least one node spec")
	}

	hasInjection := false
	hasEgress := false

	for _, spec := range tc.Nodes {
		// Valida NodeType
		if spec.NodeType != "injection" && spec.NodeType != "relay" && spec.NodeType != "egress" {
			return fmt.Errorf("invalid node type: %s", spec.NodeType)
		}

		// Valida Layer
		if spec.Layer < 0 {
			return fmt.Errorf("layer must be >= 0")
		}

		// Valida Count
		if spec.Count <= 0 {
			return fmt.Errorf("count must be > 0")
		}

		// Regole specifiche per tipo
		switch spec.NodeType {
		case "injection":
			if spec.Layer != 0 {
				return fmt.Errorf("injection nodes must be at layer 0")
			}
			hasInjection = true

		case "relay":
			if spec.Layer < 1 {
				return fmt.Errorf("relay nodes must be at layer >= 1")
			}

		case "egress":
			if spec.Layer < 2 {
				return fmt.Errorf("egress nodes must be at layer >= 2")
			}
			hasEgress = true
		}
	}

	if !hasInjection {
		return fmt.Errorf("template must have at least one injection node")
	}

	if !hasEgress {
		return fmt.Errorf("template must have at least one egress node")
	}

	return nil
}

// GetMaxLayer ritorna il layer più alto nel template
func (tc *TemplateConfig) GetMaxLayer() int {
	maxLayer := 0
	for _, spec := range tc.Nodes {
		if spec.Layer > maxLayer {
			maxLayer = spec.Layer
		}
	}
	return maxLayer
}

// GetTotalNodes ritorna il numero totale di nodi nel template
func (tc *TemplateConfig) GetTotalNodes() int {
	total := 0
	for _, spec := range tc.Nodes {
		total += spec.Count
	}
	return total
}

// GetNodesByType ritorna specs per tipo
func (tc *TemplateConfig) GetNodesByType(nodeType string) []TemplateNodeSpec {
	specs := []TemplateNodeSpec{}
	for _, spec := range tc.Nodes {
		if spec.NodeType == nodeType {
			specs = append(specs, spec)
		}
	}
	return specs
}

// GetNodesByLayer ritorna specs per layer
func (tc *TemplateConfig) GetNodesByLayer(layer int) []TemplateNodeSpec {
	specs := []TemplateNodeSpec{}
	for _, spec := range tc.Nodes {
		if spec.Layer == layer {
			specs = append(specs, spec)
		}
	}
	return specs
}
