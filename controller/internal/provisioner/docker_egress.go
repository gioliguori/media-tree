package provisioner

import (
	"context"
	"fmt"
	"log"

	"controller/internal/domain"
)

func (p *DockerProvisioner) createEgressNode(ctx context.Context, spec domain.NodeSpec) (*domain.NodeInfo, error) {
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

	streamStart, streamEnd, err := p.portAllocator.AllocateStreamingRange()
	if err != nil {
		p.portAllocator.ReleasePorts(apiPort, janusHTTP, janusWS)
		p.portAllocator.ReleaseRange(webrtcStart, webrtcEnd)
		return nil, fmt.Errorf("failed to allocate Streaming range: %w", err)
	}

	// Crea Janus Streaming
	dockerName := fmt.Sprintf("%s-%s", spec.TreeId, spec.NodeId)
	janusDockerName := dockerName + "-janus-streaming"

	janusArgs := []string{
		"-d",
		"--name", janusDockerName,
		"--cpus", "1.0",
		"--memory", "512m",
		"--hostname", janusDockerName,
		"--network", p.networkName,
		"-e", fmt.Sprintf("JANUS_RTP_PORT_RANGE=%d-%d", webrtcStart, webrtcEnd),
		"-e", fmt.Sprintf("JANUS_STREAMING_RTP_PORT_RANGE=%d-%d", streamStart, streamEnd),
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
		p.portAllocator.ReleaseRange(streamStart, streamEnd)
		return nil, fmt.Errorf("failed to create Janus container: %w", err)
	}

	// Crea egress node
	nodeArgs := []string{
		"-d",
		"--name", dockerName,
		"--cpus", "1.0",
		"--memory", "512m",
		"--hostname", dockerName,
		"--network", p.networkName,
		"-e", fmt.Sprintf("NODE_ID=%s", spec.NodeId),
		"-e", fmt.Sprintf("NODE_HOST=%s", dockerName),
		"-e", "API_PORT=7070",
		"-e", fmt.Sprintf("TREE_ID=%s", spec.TreeId),
		"-e", fmt.Sprintf("LAYER=%d", spec.Layer),
		"-e", "RTP_AUDIO_PORT=5002",
		"-e", "RTP_VIDEO_PORT=5004",
		"-e", "REDIS_HOST=redis",
		"-e", "REDIS_PORT=6379",
		"-e", fmt.Sprintf("JANUS_STREAMING_WS_URL=ws://%s:8188", janusDockerName),
		"-e", "JANUS_STREAMING_MOUNTPOINT_SECRET=adminpwd",
		"-e", "WHEP_BASE_PATH=/whep",
		"-e", "WHEP_TOKEN=verysecret",
		"-p", fmt.Sprintf("%d:7070/tcp", apiPort),
		"media-tree/egress-node:latest",
	}

	nodeID, err := p.dockerRun(ctx, nodeArgs)
	if err != nil {
		// Rollback
		p.dockerRemove(ctx, dockerName)
		p.dockerStop(ctx, janusDockerName)
		p.dockerRemove(ctx, janusDockerName)
		p.portAllocator.ReleasePorts(apiPort, janusHTTP, janusWS)
		p.portAllocator.ReleaseRange(webrtcStart, webrtcEnd)
		p.portAllocator.ReleaseRange(streamStart, streamEnd)
		return nil, fmt.Errorf("failed to create egress node: %w", err)
	}

	// Costruisci NodeInfo
	nodeInfo := &domain.NodeInfo{
		NodeId:           spec.NodeId,
		NodeType:         spec.NodeType,
		TreeId:           spec.TreeId,
		Layer:            spec.Layer,
		ContainerId:      nodeID,
		InternalHost:     dockerName,
		InternalAPIPort:  7070,
		InternalRTPAudio: 5002,
		InternalRTPVideo: 5004,
		ExternalHost:     "localhost",
		ExternalAPIPort:  apiPort,
		JanusContainerId: janusID,
		JanusHost:        janusDockerName,
		JanusWSPort:      janusWS,
		JanusHTTPPort:    janusHTTP,
		WebRTCPortStart:  webrtcStart,
		WebRTCPortEnd:    webrtcEnd,
		StreamPortStart:  streamStart,
		StreamPortEnd:    streamEnd,
	}
	// Salva in Redis
	if err := p.redisClient.SaveNodeProvisioning(ctx, nodeInfo); err != nil {
		// Rollback
		log.Printf("[WARN] Failed to save Egress to Redis, rolling back %s...", spec.NodeId)
		p.dockerStop(ctx, dockerName)
		p.dockerRemove(ctx, dockerName)
		p.dockerStop(ctx, janusDockerName)
		p.dockerRemove(ctx, janusDockerName)
		p.portAllocator.ReleasePorts(apiPort, janusHTTP, janusWS)
		p.portAllocator.ReleaseRange(webrtcStart, webrtcEnd)
		p.portAllocator.ReleaseRange(streamStart, streamEnd)
		return nil, fmt.Errorf("failed to save provisioning to Redis: %w", err)
	}

	return nodeInfo, nil
}
