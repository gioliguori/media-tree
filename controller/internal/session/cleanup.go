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
	PathInactiveThreshold    = 1 * time.Minute  // 5min (1min per test)
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
			// Cleanup globale delle sessioni inattive
			sm.cleanupInactiveSessions(ctx)

			// Cleanup globale dei path inattivi
			sm.cleanupInactiveEgressPaths(ctx)

		case <-ctx.Done():
			log.Printf("[SessionCleanup] Stopped")
			return
		}
	}
}

func (sm *SessionManager) cleanupInactiveSessions(ctx context.Context) error {
	// Threshold timestamp (5min ago)
	thresholdMs := time.Now().Add(-SessionInactiveThreshold).UnixMilli()

	// Query sorted set:  sessioni inactive > 5min
	sortedSetKey := "sessions:inactive"

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

	log.Printf("[SessionCleanup] Found %d expired sessions", len(expiredSessions))

	// Destroy ogni sessione expired
	for _, entry := range expiredSessions {
		// Formato entry: "{sessionId}"
		sessionId := entry

		// Get last activity per logging
		score, _ := sm.redis.GetRedisClient().ZScore(ctx, sortedSetKey, entry).Result()
		inactiveDuration := time.Since(time.UnixMilli(int64(score)))

		log.Printf("[SessionCleanup] Destroying session %s (inactive for %v)", sessionId, inactiveDuration)

		// Destroy session completa
		err := sm.DestroySessionComplete(ctx, sessionId)
		if err != nil {
			log.Printf("[SessionCleanup] Failed to destroy %s: %v", sessionId, err)
			continue
		}

		// Remove from sorted set
		sm.redis.GetRedisClient().ZRem(ctx, sortedSetKey, entry)

		log.Printf("[SessionCleanup] Session %s destroyed", sessionId)
	}

	log.Printf("[SessionCleanup] Cleaned %d sessions", len(expiredSessions))

	return nil
}

// cleanupInactiveEgressPaths scansiona i path che hanno 0 viewer da troppo tempo
func (sm *SessionManager) cleanupInactiveEgressPaths(ctx context.Context) error {
	// Definiamo la soglia
	thresholdMs := time.Now().Add(-PathInactiveThreshold).UnixMilli()

	sortedSetKey := "paths:inactive"

	// Prendiamo i path scaduti (score tra -infinito e threshold)
	expiredEntries, err := sm.redis.GetRedisClient().ZRangeByScore(ctx, sortedSetKey, &redis.ZRangeBy{
		Min: "-inf",
		Max: fmt.Sprintf("%d", thresholdMs),
	}).Result()

	if err != nil || len(expiredEntries) == 0 {
		return err
	}

	log.Printf("[SessionCleanup] Found %d Idle paths to remove", len(expiredEntries))

	for _, entry := range expiredEntries {
		// entry format: "nodeId:sessionId"
		parts := strings.Split(entry, ":")
		if len(parts) != 2 {
			continue
		}
		nodeId := parts[0]
		sessionId := parts[1]

		log.Printf("[SessionCleanup] Path %s for session %s has 0 viewers. Cleaning up.", nodeId, sessionId)

		// Chiamiamo il backtracking (DestroySessionPath)
		// Questo rimuove la sessione dall'Egress e risale i Relay intermedi
		err := sm.DestroySessionPath(ctx, sessionId, nodeId)
		if err != nil {
			log.Printf("[SessionCleanup] Error cleaning path %s: %v", entry, err)
			continue
		}

		// Rimuoviamo l'entry dal Sorted Set
		sm.redis.GetRedisClient().ZRem(ctx, sortedSetKey, entry)
	}

	return nil
}
