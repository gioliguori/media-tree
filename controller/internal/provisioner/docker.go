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
func (p *DockerProvisioner) CreateNode(ctx context.Context, spec domain.NodeSpec, role string) (*domain.NodeInfo, error) {
	switch spec.NodeType {
	case domain.NodeTypeInjection:
		return p.createInjectionNode(ctx, spec, role)
	case domain.NodeTypeRelay:
		return p.createRelayNode(ctx, spec,role)
	case domain.NodeTypeEgress:
		return p.createEgressNode(ctx, spec,role)
	default:
		return nil, fmt.Errorf("unknown node type: %s", spec.NodeType)
	}
}

// DestroyNode
func (p *DockerProvisioner) DestroyNode(ctx context.Context, nodeInfo *domain.NodeInfo) error {
	// Se mancano info, recupera da Redis
	if nodeInfo.ContainerId == "" {
		fullInfo, err := p.redisClient.GetNodeProvisioning(ctx, nodeInfo.NodeId)
		if err != nil {
			// se Redis fallisce, ricostruiamo il nome fisico a mano
			nodeInfo.InternalHost = nodeInfo.NodeId
		} else {
			nodeInfo = fullInfo
		}
	}

	targetContainer := nodeInfo.InternalHost
	if nodeInfo.ContainerId != "" {
		targetContainer = nodeInfo.ContainerId
	}
	// (SIGTERM)
	log.Printf("[INFO] Stopping node %s", nodeInfo.NodeId)
	if err := p.dockerStop(ctx, targetContainer); err != nil {
		log.Printf("[WARN] Failed to stop %s: %v", targetContainer, err)
	}

	// Attendi che nodo si unregister (max 3s)
	time.Sleep(3 * time.Second)

	// Rimuovi container principale
	if err := p.dockerRemove(ctx, targetContainer); err != nil {
		return fmt.Errorf("failed to remove node container: %w", err)
	}

	// Se ha Janus, rimuovi anche quello
	if nodeInfo.NeedsJanus() {
		targetJanus := nodeInfo.JanusContainerId
		if targetJanus == "" {
			suffix := "-janus-vr"
			if nodeInfo.IsEgress() {
				suffix = "-janus-streaming"
			}
			targetJanus = nodeInfo.NodeId + suffix
		}
		log.Printf("[Provisioner] Stopping Janus %s", targetJanus)
		p.dockerStop(ctx, targetJanus)
		p.dockerRemove(ctx, targetJanus)
	}

	// Cleanup redis
	if nodeInfo.NodeType != "" {
		p.redisClient.ForceDeleteNode(ctx, nodeInfo.NodeId, string(nodeInfo.NodeType))
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
		if nodeInfo.StreamPortStart > 0 && nodeInfo.StreamPortEnd > 0 {
			p.portAllocator.ReleaseRange(nodeInfo.StreamPortStart, nodeInfo.StreamPortEnd)
		}
	}
	// Cleanup Redis provisioning info
	if err := p.redisClient.DeleteNodeProvisioning(ctx, nodeInfo.NodeId); err != nil {
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

func (p *DockerProvisioner) CreateAgent(ctx context.Context) error {
	log.Println("[Provisioner] Starting Metrics Agent")

	args := []string{
		"-d",
		"--name", "metrics-agent",
		"--network", p.networkName,
		"--restart", "always",
		"-v", "/var/run/docker.sock:/var/run/docker.sock",
		"media-tree/metrics-agent:latest",
	}

	_, err := p.dockerRun(ctx, args)
	return err
}
