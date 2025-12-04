package provisioner

import (
	"context"
	"fmt"
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

// CreateNode crea un nodo
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

// DestroyNode distrugge un nodo con graceful shutdown
func (p *DockerProvisioner) DestroyNode(ctx context.Context, nodeInfo *domain.NodeInfo) error {
	// Se mancano info, recupera da Redis
	if nodeInfo.ContainerId == "" {
		fullInfo, err := p.redisClient.GetNodeProvisioning(ctx, nodeInfo.TreeId, nodeInfo.NodeId)
		if err != nil {
			return fmt.Errorf("failed to get provisioning info: %w", err)
		}
		nodeInfo = fullInfo
	}

	// 1. Graceful shutdown (SIGTERM)
	fmt.Printf("[INFO] Stopping node %s gracefully...\n", nodeInfo.NodeId)
	if err := p.dockerStop(ctx, nodeInfo.NodeId); err != nil {
		fmt.Printf("[WARN] Failed to stop %s gracefully: %v\n", nodeInfo.NodeId, err)
	}

	// 2. Attendi che nodo si unregister (max 3s)
	time.Sleep(3 * time.Second)

	// 3. Rimuovi container principale
	if err := p.dockerRemove(ctx, nodeInfo.NodeId); err != nil {
		return fmt.Errorf("failed to remove node container: %w", err)
	}

	// 4. Se ha Janus, rimuovi anche quello
	if nodeInfo.NeedsJanus() && nodeInfo.JanusHost != "" {
		fmt.Printf("[INFO] Stopping Janus %s...\n", nodeInfo.JanusHost)
		p.dockerStop(ctx, nodeInfo.JanusHost)
		if err := p.dockerRemove(ctx, nodeInfo.JanusHost); err != nil {
			fmt.Printf("[WARN] Failed to remove Janus: %v\n", err)
		}
	}

	// 5. CLEANUP REDIS
	if nodeInfo.NodeType != "" {
		err := p.redisClient.ForceDeleteNode(ctx, nodeInfo.TreeId, nodeInfo.NodeId, string(nodeInfo.NodeType))
		if err != nil {
			fmt.Printf("[WARN] ForceDeleteNode error: %v\n", err)
		}
	}
	// 6. Rilascia porte
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

	// 7. Cleanup Redis provisioning info
	if err := p.redisClient.DeleteNodeProvisioning(ctx, nodeInfo.TreeId, nodeInfo.NodeId); err != nil {
		fmt.Printf("[WARN] Failed to delete provisioning info: %v\n", err)
	}

	fmt.Printf("[INFO] Node %s destroyed successfully\n", nodeInfo.NodeId)
	return nil
}

// dockerRun esegue docker run e ritorna container ID
func (p *DockerProvisioner) dockerRun(ctx context.Context, args []string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", append([]string{"run"}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker run failed: %w, output: %s", err, string(output))
	}

	containerID := strings.TrimSpace(string(output))
	return containerID, nil
}

// dockerStop esegue docker stop
func (p *DockerProvisioner) dockerStop(ctx context.Context, containerName string) error {
	cmd := exec.CommandContext(ctx, "docker", "stop", "-t", "10", containerName)
	_, err := cmd.CombinedOutput()
	return err
}

// dockerRemove esegue docker rm
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
