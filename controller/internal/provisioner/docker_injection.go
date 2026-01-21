package provisioner

import (
	"context"
	"fmt"
	"log"

	"controller/internal/domain"
)

func (p *DockerProvisioner) createInjectionNode(ctx context.Context, spec domain.NodeSpec) (*domain.NodeInfo, error) {
	// Alloca porte
	apiPort, err := p.portAllocator.AllocateAPIPort()
	if err != nil {
		return nil, fmt.Errorf("failed to allocate API port: %w", err)
	}

	janusHTTP, janusWS, err := p.portAllocator.AllocateJanusPorts()
	if err != nil {
		p.portAllocator.Release(apiPort)
		return nil, fmt.Errorf("failed to allocate Janus ports: %w", err)
	}

	webrtcStart, webrtcEnd, err := p.portAllocator.AllocateWebRTCRange()
	if err != nil {
		p.portAllocator.ReleasePorts(apiPort, janusHTTP, janusWS)
		return nil, fmt.Errorf("failed to allocate WebRTC range: %w", err)
	}

	// Crea Janus Videoroom
	janusName := spec.NodeId + "-janus-vr"

	janusArgs := []string{
		"-d",
		"--name", janusName,
		"--cpus", "1.0",
		"--memory", "512m",
		"--hostname", janusName,
		"--network", p.networkName,
		"-e", fmt.Sprintf("JANUS_RTP_PORT_RANGE=%d-%d", webrtcStart, webrtcEnd),
		"-e", "JANUS_LOG_LEVEL=4",
		"-p", fmt.Sprintf("%d:8088/tcp", janusHTTP),
		"-p", fmt.Sprintf("%d:8188/tcp", janusWS),
		"-p", fmt.Sprintf("%d-%d:%d-%d/udp", webrtcStart, webrtcEnd, webrtcStart, webrtcEnd),
		"media-tree/janus-videoroom:latest",
	}

	janusID, err := p.dockerRun(ctx, janusArgs)
	if err != nil {
		p.portAllocator.ReleasePorts(apiPort, janusHTTP, janusWS)
		p.portAllocator.ReleaseRange(webrtcStart, webrtcEnd)
		return nil, fmt.Errorf("failed to create Janus container: %w", err)
	}

	// Crea Injection Node
	nodeArgs := []string{
		"-d",
		"--name", spec.NodeId,
		"--cpus", "1.0",
		"--memory", "512m",
		"--hostname", spec.NodeId,
		"--network", p.networkName,
		"-e", fmt.Sprintf("NODE_ID=%s", spec.NodeId),
		"-e", fmt.Sprintf("NODE_HOST=%s", spec.NodeId),
		"-e", "API_PORT=7070",
		"-e", fmt.Sprintf("TREE_ID=%s", spec.TreeId),
		"-e", fmt.Sprintf("LAYER=%d", spec.Layer),
		"-e", "RTP_AUDIO_PORT=5000",
		"-e", "RTP_VIDEO_PORT=5002",
		"-e", "REDIS_HOST=redis",
		"-e", "REDIS_PORT=6379",
		"-e", fmt.Sprintf("JANUS_VIDEOROOM_WS_URL=ws://%s:8188", janusName),
		"-e", "JANUS_VIDEOROOM_ROOM_SECRET=adminpwd",
		"-e", "WHIP_BASE_PATH=/whip",
		"-e", "WHIP_TOKEN=verysecret",
		"-p", fmt.Sprintf("%d:7070/tcp", apiPort),
		"media-tree/injection-node:latest",
	}

	nodeID, err := p.dockerRun(ctx, nodeArgs)
	if err != nil {
		// Rollback
		p.dockerRemove(ctx, spec.NodeId)
		p.dockerStop(ctx, janusName)
		p.dockerRemove(ctx, janusName)
		p.portAllocator.ReleasePorts(apiPort, janusHTTP, janusWS)
		p.portAllocator.ReleaseRange(webrtcStart, webrtcEnd)
		return nil, fmt.Errorf("failed to create injection node: %w", err)
	}

	// Costruisci NodeInfo
	nodeInfo := &domain.NodeInfo{
		NodeId:           spec.NodeId,
		NodeType:         spec.NodeType,
		TreeId:           spec.TreeId,
		Layer:            spec.Layer,
		ContainerId:      nodeID,
		InternalHost:     spec.NodeId,
		InternalAPIPort:  7070,
		InternalRTPAudio: 5000,
		InternalRTPVideo: 5002,
		ExternalHost:     "localhost",
		ExternalAPIPort:  apiPort,
		JanusContainerId: janusID,
		JanusHost:        janusName,
		JanusWSPort:      janusWS,
		JanusHTTPPort:    janusHTTP,
		WebRTCPortStart:  webrtcStart,
		WebRTCPortEnd:    webrtcEnd,
	}

	// Salva in Redis
	if err := p.redisClient.SaveNodeProvisioning(ctx, nodeInfo); err != nil {
		// Rollback
		log.Printf("[WARN] Failed to save to Redis, rolling back %s...", spec.NodeId)
		p.dockerStop(ctx, nodeInfo.NodeId)
		p.dockerRemove(ctx, nodeInfo.NodeId)
		p.dockerStop(ctx, janusName)
		p.dockerRemove(ctx, janusName)
		p.portAllocator.ReleasePorts(apiPort, janusHTTP, janusWS)
		p.portAllocator.ReleaseRange(webrtcStart, webrtcEnd)
		return nil, fmt.Errorf("failed to save provisioning to Redis: %w", err)
	}

	return nodeInfo, nil
}
