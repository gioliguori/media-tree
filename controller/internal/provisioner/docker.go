package provisioner

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"

	"controller/internal/domain"
	"controller/internal/redis"
)

// DockerProvisioner gestisce creazione/distruzione container via CLI
type DockerProvisioner struct {
	portAllocator *PortAllocator
	networkName   string
	redisClient   *redis.Client
}

// NewDockerProvisioner crea provisioner
func NewDockerProvisioner(networkName string, redisClient *redis.Client) (*DockerProvisioner, error) {
	return &DockerProvisioner{
		portAllocator: NewPortAllocator(),
		networkName:   networkName,
		redisClient:   redisClient,
	}, nil
}

// CreateNode: Smistatore.
// Riceve una richiesta generica (NodeSpec) e decide quale funzione specifica chiamare
func (p *DockerProvisioner) CreateNode(ctx context.Context, spec domain.NodeSpec) (*domain.NodeInfo, error) {
	switch spec.NodeType {
	case domain.NodeTypeInjection:
		return p.createInjectionNode(ctx, spec)
	case domain.NodeTypeRelay:
		return p.createRelayNode(ctx, spec)
	case domain.NodeTypeEgress:
		return p.createEgressNode(ctx, spec)
	default:
		return nil, fmt.Errorf("unknown node type: %s", spec.NodeType)
	}
}

// DestroyNode
func (p *DockerProvisioner) DestroyNode(ctx context.Context, nodeInfo *domain.NodeInfo) error {
	// Se mancano info, recupera da Redis
	if nodeInfo.ContainerId == "" {
		fullInfo, err := p.redisClient.GetNodeProvisioning(ctx, nodeInfo.TreeId, nodeInfo.NodeId)
		if err != nil {
			return fmt.Errorf("failed to get provisioning info: %w", err)
		}
		nodeInfo = fullInfo
	}

	// (SIGTERM)
	log.Printf("[INFO] Stopping node %s gracefully...", nodeInfo.NodeId)
	if err := p.dockerStop(ctx, nodeInfo.NodeId); err != nil {
		log.Printf("[WARN] Failed to stop %s gracefully: %v", nodeInfo.NodeId, err)
	}

	// Attendi che nodo si unregister (max 3s)
	time.Sleep(3 * time.Second)

	// Rimuovi container principale
	if err := p.dockerRemove(ctx, nodeInfo.NodeId); err != nil {
		return fmt.Errorf("failed to remove node container: %w", err)
	}

	// Se ha Janus, rimuovi anche quello
	if nodeInfo.NeedsJanus() && nodeInfo.JanusHost != "" {
		log.Printf("[INFO] Stopping Janus %s...", nodeInfo.JanusHost)
		p.dockerStop(ctx, nodeInfo.JanusHost)
		if err := p.dockerRemove(ctx, nodeInfo.JanusHost); err != nil {
			log.Printf("[WARN] Failed to remove Janus: %v", err)
		}
	}

	// Cleanup redis
	if nodeInfo.NodeType != "" {
		err := p.redisClient.ForceDeleteNode(ctx, nodeInfo.TreeId, nodeInfo.NodeId, string(nodeInfo.NodeType))
		if err != nil {
			log.Printf("[WARN] ForceDeleteNode error: %v", err)
		}
	}
	// Rilascia porte
	if nodeInfo.ExternalAPIPort > 0 {
		p.portAllocator.Release(nodeInfo.ExternalAPIPort)
	}

	if nodeInfo.NeedsJanus() {
		if nodeInfo.JanusHTTPPort > 0 {
			p.portAllocator.Release(nodeInfo.JanusHTTPPort)
		}
		if nodeInfo.JanusWSPort > 0 {
			p.portAllocator.Release(nodeInfo.JanusWSPort)
		}
		if nodeInfo.WebRTCPortStart > 0 && nodeInfo.WebRTCPortEnd > 0 {
			p.portAllocator.ReleaseRange(nodeInfo.WebRTCPortStart, nodeInfo.WebRTCPortEnd)
		}
	}
	// Cleanup Redis provisioning info
	if err := p.redisClient.DeleteNodeProvisioning(ctx, nodeInfo.TreeId, nodeInfo.NodeId); err != nil {
		log.Printf("[WARN] Failed to delete provisioning info: %v", err)
	}

	log.Printf("[INFO] Node %s destroyed successfully", nodeInfo.NodeId)
	return nil
}

// dockerRun esegue docker run e ritorna container ID
func (p *DockerProvisioner) dockerRun(ctx context.Context, args []string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", append([]string{"run"}, args...)...)
	output, err := cmd.CombinedOutput() // Cattura stdout e stderr
	if err != nil {
		return "", fmt.Errorf("docker run failed: %w, output: %s", err, string(output))
	}

	containerID := strings.TrimSpace(string(output))
	return containerID, nil
}

// dockerStop: Esegue "docker stop -t 10" (aspetta 10s prima di killare)
func (p *DockerProvisioner) dockerStop(ctx context.Context, containerName string) error {
	cmd := exec.CommandContext(ctx, "docker", "stop", "-t", "10", containerName)
	_, err := cmd.CombinedOutput()
	return err
}

// dockerRemove: Esegue "docker rm -f" (forza rimozione)
func (p *DockerProvisioner) dockerRemove(ctx context.Context, containerName string) error {
	cmd := exec.CommandContext(ctx, "docker", "rm", "-f", containerName)
	_, err := cmd.CombinedOutput()
	return err
}

// GetPortAllocator ritorna port allocator
func (p *DockerProvisioner) GetPortAllocator() *PortAllocator {
	return p.portAllocator
}

// Close cleanup
func (p *DockerProvisioner) Close() error {
	return nil
}
