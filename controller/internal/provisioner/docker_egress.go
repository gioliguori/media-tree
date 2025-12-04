package provisioner

import (
	"context"
	"fmt"

	"controller/internal/domain"
)

func (p *DockerProvisioner) createEgressNode(ctx context.Context, spec domain.NodeSpec) (*domain.NodeInfo, error) {
	// ========================================
	// 1. ALLOCA PORTE
	// ========================================
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

	// ========================================
	// 2. CREA JANUS STREAMING (CLI)
	// ========================================
	janusName := spec.NodeId + "-janus-streaming"

	janusArgs := []string{
		"-d",
		"--name", janusName,
		"--hostname", janusName,
		"--network", p.networkName,
		"-e", "JANUS_LOG_LEVEL=4",
		"-p", fmt.Sprintf("%d:8088/tcp", janusHTTP),
		"-p", fmt.Sprintf("%d:8188/tcp", janusWS),
		"-p", fmt.Sprintf("%d-%d:%d-%d/udp", webrtcStart, webrtcEnd, webrtcStart, webrtcEnd),
		"media-tree/janus-streaming:latest",
	}

	janusID, err := p.dockerRun(ctx, janusArgs)
	if err != nil {
		p.portAllocator.ReleasePorts(apiPort, janusHTTP, janusWS)
		p.portAllocator.ReleaseRange(webrtcStart, webrtcEnd)
		return nil, fmt.Errorf("failed to create Janus container: %w", err)
	}

	// ========================================
	// 3. CREA EGRESS NODE (CLI)
	// ========================================
	nodeArgs := []string{
		"-d",
		"--name", spec.NodeId,
		"--hostname", spec.NodeId,
		"--network", p.networkName,
		"-e", fmt.Sprintf("NODE_ID=%s", spec.NodeId),
		"-e", fmt.Sprintf("NODE_HOST=%s", spec.NodeId),
		"-e", "API_PORT=7073",
		"-e", fmt.Sprintf("TREE_ID=%s", spec.TreeId),
		"-e", fmt.Sprintf("LAYER=%d", spec.Layer),
		"-e", "RTP_AUDIO_PORT=5002",
		"-e", "RTP_VIDEO_PORT=5004",
		"-e", "REDIS_HOST=redis",
		"-e", "REDIS_PORT=6379",
		"-e", fmt.Sprintf("JANUS_STREAMING_WS_URL=ws://%s:8188", janusName),
		"-e", "JANUS_STREAMING_MOUNTPOINT_SECRET=adminpwd",
		"-e", "WHEP_BASE_PATH=/whep",
		"-e", "WHEP_TOKEN=verysecret",
		"-p", fmt.Sprintf("%d:7073/tcp", apiPort),
		"-p", "6000-6200:6000-6200/udp",
		"media-tree/egress-node:latest",
	}

	nodeID, err := p.dockerRun(ctx, nodeArgs)
	if err != nil {
		p.dockerStop(ctx, janusName)
		p.dockerRemove(ctx, janusName)
		p.portAllocator.ReleasePorts(apiPort, janusHTTP, janusWS)
		p.portAllocator.ReleaseRange(webrtcStart, webrtcEnd)
		return nil, fmt.Errorf("failed to create egress node: %w", err)
	}

	// ========================================
	// 4. COSTRUISCI NodeInfo
	// ========================================
	nodeInfo := &domain.NodeInfo{
		NodeId:           spec.NodeId,
		NodeType:         spec.NodeType,
		TreeId:           spec.TreeId,
		Layer:            spec.Layer,
		ContainerId:      nodeID,
		InternalHost:     spec.NodeId,
		InternalAPIPort:  7073,
		InternalRTPAudio: 5002,
		InternalRTPVideo: 5004,
		ExternalHost:     "localhost",
		ExternalAPIPort:  apiPort,
		JanusContainerId: janusID,
		JanusHost:        janusName,
		JanusWSPort:      8188,
		JanusHTTPPort:    janusHTTP,
		WebRTCPortStart:  webrtcStart,
		WebRTCPortEnd:    webrtcEnd,
	}

	// ========================================
	// 5. SALVA IN REDIS
	// ========================================
	if err := p.redisClient.SaveNodeProvisioning(ctx, nodeInfo); err != nil {
		// Rollback
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
