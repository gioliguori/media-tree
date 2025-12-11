package tree

import (
	"fmt"
)

// Templates predefiniti per creazione alberi
var Templates = map[string]TemplateConfig{
	"minimal": {
		Name:        "minimal",
		Description: "1 injection + 1 relay + 1 egress",
		Nodes: []TemplateNodeSpec{
			{NodeType: "injection", Layer: 0, Count: 1},
			{NodeType: "relay", Layer: 1, Count: 1},
			{NodeType: "egress", Layer: 2, Count: 1},
		},
	},

	"small": {
		Name:        "small",
		Description: "1 injection + 1 relay + 3 egress",
		Nodes: []TemplateNodeSpec{
			{NodeType: "injection", Layer: 0, Count: 1},
			{NodeType: "relay", Layer: 1, Count: 1},
			{NodeType: "egress", Layer: 2, Count: 3},
		},
	},

	"medium": {
		Name:        "medium",
		Description: "2 injection + 2 relay + 6 egress",
		Nodes: []TemplateNodeSpec{
			{NodeType: "injection", Layer: 0, Count: 2},
			{NodeType: "relay", Layer: 1, Count: 2},
			{NodeType: "egress", Layer: 2, Count: 6},
		},
	},

	"large": {
		Name:        "large",
		Description: "1 injection + 3 relay + 9 egress",
		Nodes: []TemplateNodeSpec{
			{NodeType: "injection", Layer: 0, Count: 1},
			{NodeType: "relay", Layer: 1, Count: 3},
			{NodeType: "egress", Layer: 2, Count: 9},
		},
	},

	"deep": {
		Name:        "deep",
		Description: "Multi-tier with relay at layer 2",
		Nodes: []TemplateNodeSpec{
			{NodeType: "injection", Layer: 0, Count: 1},
			{NodeType: "relay", Layer: 1, Count: 2},
			{NodeType: "relay", Layer: 2, Count: 4},
			{NodeType: "egress", Layer: 2, Count: 4},
			{NodeType: "egress", Layer: 3, Count: 8},
		},
	},
	"test-route": {
		Name:        "test-route",
		Description: "testing routing sessions",
		Nodes: []TemplateNodeSpec{
			{NodeType: "injection", Layer: 0, Count: 2},
			{NodeType: "relay", Layer: 1, Count: 1},
			{NodeType: "relay", Layer: 2, Count: 1},
			{NodeType: "egress", Layer: 2, Count: 2},
			{NodeType: "egress", Layer: 3, Count: 2},
		},
	},
}

// GetTemplate ritorna un template per nome
func GetTemplate(name string) (TemplateConfig, error) {
	tmpl, ok := Templates[name]
	if !ok {
		return TemplateConfig{}, fmt.Errorf("template not found: %s", name)
	}

	// Valida template
	if err := tmpl.Validate(); err != nil {
		return TemplateConfig{}, fmt.Errorf("invalid template %s: %w", name, err)
	}

	return tmpl, nil
}

// ListTemplates ritorna tutti i template disponibili
func ListTemplates() []TemplateConfig {
	templates := make([]TemplateConfig, 0, len(Templates))
	for _, tmpl := range Templates {
		templates = append(templates, tmpl)
	}
	return templates
}

// TemplateExists verifica se un template esiste
func TemplateExists(name string) bool {
	_, ok := Templates[name]
	return ok
}
