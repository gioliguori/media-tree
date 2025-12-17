package session

import (
	"context"
	"fmt"

	"controller/internal/redis"
)

// GenerateSSRC genera SSRC univoco per tree
// Range: 10000-999999999 (counter incrementale)
func GenerateSSRC(
	ctx context.Context,
	redisClient *redis.Client,
	treeId string,
) (int, error) {
	ssrc, err := redisClient.GetNextSSRC(ctx, treeId)
	if err != nil {
		return 0, fmt.Errorf("failed to generate SSRC: %w", err)
	}

	// Range check
	if ssrc > 999999999 {
		return 0, fmt.Errorf("SSRC overflow: %d", ssrc)
	}

	return ssrc, nil
}

// GenerateSSRCPair genera coppia audio/video SSRC
func GenerateSSRCPair(
	ctx context.Context,
	redisClient *redis.Client,
	treeId string,
) (audioSsrc int, videoSsrc int, err error) {
	// Genera audio SSRC
	audioSsrc, err = GenerateSSRC(ctx, redisClient, treeId)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to generate audio SSRC: %w", err)
	}

	// Genera video SSRC
	videoSsrc, err = GenerateSSRC(ctx, redisClient, treeId)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to generate video SSRC: %w", err)
	}

	return audioSsrc, videoSsrc, nil
}

// GenerateRoomId genera room ID univoco per tree
func GenerateRoomId(
	ctx context.Context,
	redisClient *redis.Client,
	treeId string,
) (int, error) {
	roomId, err := redisClient.GetNextRoomId(ctx, treeId)
	if err != nil {
		return 0, fmt.Errorf("failed to generate room ID: %w", err)
	}

	if roomId > 999999 {
		return 0, fmt.Errorf("room ID overflow: %d", roomId)
	}

	return roomId, nil
}
