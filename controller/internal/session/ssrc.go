package session

import (
	"context"
	"fmt"

	"controller/internal/redis"
)

// GenerateSSRC genera SSRC univoco globale
func GenerateSSRC(
	ctx context.Context,
	redisClient *redis.Client,
) (int, error) {
	ssrc, err := redisClient.GetNextSSRC(ctx)
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
) (audioSsrc int, videoSsrc int, err error) {
	// Genera audio SSRC
	audioSsrc, err = GenerateSSRC(ctx, redisClient)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to generate audio SSRC: %w", err)
	}

	// Genera video SSRC
	videoSsrc, err = GenerateSSRC(ctx, redisClient)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to generate video SSRC: %w", err)
	}

	return audioSsrc, videoSsrc, nil
}

// GenerateRoomId genera room Id univoco per tree
func GenerateRoomId(
	ctx context.Context,
	redisClient *redis.Client,
) (int, error) {
	roomId, err := redisClient.GetNextRoomId(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to generate room Id: %w", err)
	}

	if roomId > 999999 {
		return 0, fmt.Errorf("room Id overflow: %d", roomId)
	}

	return roomId, nil
}
