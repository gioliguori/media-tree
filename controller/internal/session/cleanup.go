package session

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	SessionInactiveThreshold = 1 * time.Minute  // 5min (1min per test)
	CleanupInterval          = 30 * time.Second // Check ogni 30s
)

// StartCleanupJob avvia background job per cleanup sessioni inactive
func (sm *SessionManager) StartCleanupJob(ctx context.Context) {
	log.Printf("[SessionCleanup] Starting (interval=%v, threshold=%v)",
		CleanupInterval, SessionInactiveThreshold)

	go sm.cleanupLoop(ctx)
}

// cleanupLoop - background loop
func (sm *SessionManager) cleanupLoop(ctx context.Context) {
	ticker := time.NewTicker(CleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// Get all trees
			trees, err := sm.getAllTrees(ctx)
			if err != nil {
				log.Printf("[SessionCleanup] Failed to get trees: %v", err)
				continue
			}

			// Cleanup per ogni tree
			for _, treeID := range trees {
				if err := sm.cleanupInactiveSessions(ctx, treeID); err != nil {
					log.Printf("[SessionCleanup] Failed for tree %s: %v", treeID, err)
				}
			}

		case <-ctx.Done():
			log.Printf("[SessionCleanup] Stopped")
			return
		}
	}
}

// cleanupInactiveSessions - cleanup per singolo tree
func (sm *SessionManager) cleanupInactiveSessions(ctx context.Context, treeID string) error {
	// Threshold timestamp (5min ago)
	thresholdMs := time.Now().Add(-SessionInactiveThreshold).UnixMilli()

	// Query sorted set:  sessioni inactive > 5min
	sortedSetKey := fmt.Sprintf("inactive_sessions:%s", treeID)

	expiredSessions, err := sm.redis.GetRedisClient().ZRangeByScore(ctx, sortedSetKey, &redis.ZRangeBy{
		Min: "-inf",
		Max: fmt.Sprintf("%d", thresholdMs),
	}).Result()

	if err != nil {
		return fmt.Errorf("failed to query sorted set: %w", err)
	}

	if len(expiredSessions) == 0 {
		return nil // Nessuna sessione da cleanup
	}

	log.Printf("[SessionCleanup] Tree %s: found %d expired sessions", treeID, len(expiredSessions))

	// Destroy ogni sessione expired
	for _, entry := range expiredSessions {
		// entry format: "{treeId}:{sessionId}"
		parts := strings.Split(entry, ":")
		if len(parts) != 2 {
			log.Printf("[SessionCleanup] Invalid entry format: %s", entry)
			continue
		}

		sessionID := parts[1]

		// Get last activity per logging
		score, _ := sm.redis.GetRedisClient().ZScore(ctx, sortedSetKey, entry).Result()
		inactiveDuration := time.Since(time.UnixMilli(int64(score)))

		log.Printf("[SessionCleanup] Destroying session %s (inactive for %v)", sessionID, inactiveDuration)

		// Destroy session completa
		err := sm.DestroySessionComplete(ctx, treeID, sessionID)
		if err != nil {
			log.Printf("[SessionCleanup] Failed to destroy %s: %v", sessionID, err)
			continue
		}

		// Remove from sorted set
		sm.redis.GetRedisClient().ZRem(ctx, sortedSetKey, entry)

		log.Printf("[SessionCleanup] Session %s destroyed", sessionID)
	}

	log.Printf("[SessionCleanup] Tree %s: cleaned %d sessions", treeID, len(expiredSessions))

	return nil
}

// getAllTrees ritorna lista tree attivi
func (sm *SessionManager) getAllTrees(ctx context.Context) ([]string, error) {
	// Pattern: tree:*: metadata
	pattern := "tree:*:metadata"
	keys, err := sm.redis.Keys(ctx, pattern)
	if err != nil {
		return nil, err
	}

	trees := make([]string, 0, len(keys))
	for _, key := range keys {
		// Extract treeID da "tree:{treeId}:metadata"
		parts := strings.Split(key, ":")
		if len(parts) >= 2 {
			trees = append(trees, parts[1])
		}
	}

	return trees, nil
}
