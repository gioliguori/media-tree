package provisioner

import (
	"context"
	"fmt"
	"log"

	"controller/internal/domain"
)

func (p *DockerProvisioner) createRelayNode(ctx context.Context, spec domain.NodeSpec) (*domain.NodeInfo, error) {
	// Alloca porta api
	apiPort, err := p.portAllocator.AllocateAPIPort()
	if err != nil {
		return nil, fmt.Errorf("failed to allocate API port: %w", err)
	}

	// Crea Relay Node
	nodeArgs := []string{
		"-d",
		"--name", spec.NodeId,
		"--hostname", spec.NodeId,
		"--network", p.networkName,
		"-e", fmt.Sprintf("NODE_ID=%s", spec.NodeId),
		"-e", fmt.Sprintf("NODE_HOST=%s", spec.NodeId),
		"-e", fmt.Sprintf("API_PORT=%d", apiPort),
		"-e", fmt.Sprintf("TREE_ID=%s", spec.TreeId),
		"-e", fmt.Sprintf("LAYER=%d", spec.Layer),
		"-e", "RTP_AUDIO_PORT=5002",
		"-e", "RTP_VIDEO_PORT=5004",
		"-e", "REDIS_HOST=redis",
		"-e", "REDIS_PORT=6379",
		"-p", fmt.Sprintf("%d:%d/tcp", apiPort, apiPort),
		"media-tree/relay-node:latest",
	}

	nodeID, err := p.dockerRun(ctx, nodeArgs)
	if err != nil {
		p.dockerRemove(ctx, spec.NodeId)
		p.portAllocator.Release(apiPort)
		return nil, fmt.Errorf("failed to create relay node: %w", err)

	}

	// Costruisci NodeInfo
	nodeInfo := &domain.NodeInfo{
		NodeId:           spec.NodeId,
		NodeType:         spec.NodeType,
		TreeId:           spec.TreeId,
		Layer:            spec.Layer,
		ContainerId:      nodeID,
		InternalHost:     spec.NodeId,
		InternalAPIPort:  apiPort,
		InternalRTPAudio: 5002,
		InternalRTPVideo: 5004,
		ExternalHost:     "localhost",
		ExternalAPIPort:  apiPort,
	}

	// Salva in Redis
	if err := p.redisClient.SaveNodeProvisioning(ctx, nodeInfo); err != nil {
		// Rollback
		log.Printf("[WARN] Failed to save Relay to Redis, rolling back %s...", spec.NodeId)
		p.dockerStop(ctx, nodeInfo.NodeId)
		p.dockerRemove(ctx, nodeInfo.NodeId)
		p.portAllocator.Release(apiPort)
		return nil, fmt.Errorf("failed to save provisioning to Redis: %w", err)
	}

	return nodeInfo, nil
}
