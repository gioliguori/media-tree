package tree

import (
	"fmt"
)

// Templates predefiniti per creazione alberi
// relay-root vengono creati automaticamente per ogni injection
var Templates = map[string]TemplateConfig{
	"minimal": {
		Name:        "minimal",
		Description: "1 injection + 1 egress",
		Nodes: []TemplateNodeSpec{
			{NodeType: "injection", Layer: 0, Count: 1},
			{NodeType: "egress", Layer: 1, Count: 1},
			//{NodeType: "relay", Layer: 1, Count: 1},
			//{NodeType: "relay", Layer: 2, Count: 1},
			//{NodeType: "egress", Layer: 3, Count: 1},
		},
	},
	"test-overload": {
		Name:        "test-overload",
		Description: "1 injection + 3 egress",
		Nodes: []TemplateNodeSpec{
			{NodeType: "injection", Layer: 0, Count: 1},
			{NodeType: "egress", Layer: 1, Count: 3},
		},
	},
	"small": {
		Name:        "small",
		Description: "1 injection + 3 egress",
		Nodes: []TemplateNodeSpec{
			{NodeType: "injection", Layer: 0, Count: 2},
			{NodeType: "egress", Layer: 1, Count: 2},
		},
	},

	"medium": {
		Name:        "medium",
		Description: "2 injection + 2 relay + 6 egress",
		Nodes: []TemplateNodeSpec{
			{NodeType: "injection", Layer: 0, Count: 2},
			{NodeType: "relay", Layer: 1, Count: 2},
			{NodeType: "egress", Layer: 1, Count: 3},
			{NodeType: "egress", Layer: 2, Count: 3},
		},
	},

	"large": {
		Name:        "large",
		Description: "1 injection + 3 relay + 9 egress",
		Nodes: []TemplateNodeSpec{
			{NodeType: "injection", Layer: 0, Count: 1},
			{NodeType: "relay", Layer: 1, Count: 3},
			{NodeType: "egress", Layer: 1, Count: 5},
			{NodeType: "egress", Layer: 2, Count: 4},
		},
	},

	"deep": {
		Name:        "deep",
		Description: "Multi-tier",
		Nodes: []TemplateNodeSpec{
			{NodeType: "injection", Layer: 0, Count: 1},
			{NodeType: "relay", Layer: 1, Count: 2},
			{NodeType: "relay", Layer: 2, Count: 2},
			{NodeType: "egress", Layer: 1, Count: 2},
			{NodeType: "egress", Layer: 2, Count: 2},
			{NodeType: "egress", Layer: 3, Count: 4},
		},
	},

	"test-route": {
		Name:        "test-route",
		Description: "Testing routing with multiple injection pairs",
		Nodes: []TemplateNodeSpec{
			{NodeType: "injection", Layer: 0, Count: 2},
			{NodeType: "relay", Layer: 1, Count: 2},
			{NodeType: "egress", Layer: 1, Count: 2},
			{NodeType: "egress", Layer: 2, Count: 2},
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
