package provisioner

import (
	"context"

	"controller/internal/domain"
)

// Provisioner interface per creazione/distruzione nodi
// Implementazioni: DockerProvisioner
type Provisioner interface {
	// CreateNode crea un nuovo nodo
	CreateNode(ctx context.Context, spec domain.NodeSpec, role string) (*domain.NodeInfo, error)

	// DestroyNode distrugge un nodo esistente
	DestroyNode(ctx context.Context, nodeInfo *domain.NodeInfo) error

	// Close cleanup risorse
	Close() error
}
