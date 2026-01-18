package tree

import (
	"fmt"
	"time"

	"controller/internal/domain"
)

// Tree rappresenta un albero completo
type Tree struct {
	TreeId     string             `json:"treeId"`
	Template   string             `json:"template"`
	Nodes      []*domain.NodeInfo `json:"nodes"`
	NodesCount int                `json:"nodesCount"`
	CreatedAt  time.Time          `json:"createdAt"`
	Status     string             `json:"status"` // creating, active, unhealthy, destroying
}

// TreeSummary è un riassunto per listing
type TreeSummary struct {
	TreeId          string    `json:"treeId"`
	Template        string    `json:"template"`
	NodesCount      int       `json:"nodesCount"`
	InjectionCount  int       `json:"injectionCount"`
	RelayCount      int       `json:"relayCount"`
	EgressCount     int       `json:"egressCount"`
	CurrentMaxLayer int       `json:"currentMaxLayer"`
	Status          string    `json:"status"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

// TemplateConfig configurazione template
type TemplateConfig struct {
	Name        string             `json:"name"`
	Description string             `json:"description"`
	Nodes       []TemplateNodeSpec `json:"nodes"`
}

// TemplateNodeSpec specifica per creazione nodi nel template
// I relay-root vengono creati automaticamente per ogni injection
type TemplateNodeSpec struct {
	NodeType string `json:"nodeType"`
	Layer    int    `json:"layer"`
	Count    int    `json:"count"`
}

// Validate valida configurazione template
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
			// Injection devono essere al layer 0
			if spec.Layer != 0 {
				return fmt.Errorf("injection nodes must be at layer 0")
			}
			hasInjection = true

		case "relay":
			// Relay al layer 0 (relay-root) sono creati automaticamente
			if spec.Layer == 0 {
				return fmt.Errorf("relay at layer 0 are auto-created with injection (remove from template)")
			}
			if spec.Layer < 1 {
				return fmt.Errorf("relay nodes must be at layer >= 1")
			}

		case "egress":
			// Egress possono iniziare dal layer 1 (direttamente sotto relay-root)
			if spec.Layer < 1 {
				return fmt.Errorf("egress nodes must be at layer >= 1")
			}
			hasEgress = true
		}
	}

	// Template deve avere almeno 1 injection e 1 egress
	if !hasInjection {
		return fmt.Errorf("template must have at least one injection node")
	}

	if !hasEgress {
		return fmt.Errorf("template must have at least one egress node")
	}

	return nil
}

// GetMaxLayer ritorna il layer più alto nel template
func GetCurrentMaxLayer(nodes []*domain.NodeInfo) int {
	currentMaxLayer := 0
	for _, node := range nodes {
		if node.Layer > currentMaxLayer {
			currentMaxLayer = node.Layer
		}
	}
	return currentMaxLayer
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
