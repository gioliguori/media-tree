package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"controller/internal/config"

	"github.com/redis/go-redis/v9"
)

type Client struct {
	rdb *redis.Client
}

// Crea connessione
func NewClient(cfg *config.Config) *Client {
	rdb := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", cfg.RedisHost, cfg.RedisPort),
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	})

	return &Client{
		rdb: rdb,
	}
}

func (c *Client) Ping(ctx context.Context) error {
	return c.rdb.Ping(ctx).Err()
}

func (c *Client) Close() error {
	return c.rdb.Close()
}

// GetRedisClient restituisce il client Redis nativo
func (c *Client) GetRedisClient() *redis.Client {
	return c.rdb
}

// PublishJSON pubblica un evento JSON su un canale
func (c *Client) PublishJSON(ctx context.Context, channel string, data map[string]any) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}

	// Pubblica su Redis
	if err := c.rdb.Publish(ctx, channel, jsonData).Err(); err != nil {
		return fmt.Errorf("failed to publish to %s: %w", channel, err)
	}

	return nil
}

// Controlla se c'è una chiave
func (c *Client) Exists(ctx context.Context, key string) (bool, error) {
	result, err := c.rdb.Exists(ctx, key).Result()
	if err != nil {
		return false, err
	}
	return result > 0, nil
}

// HASH OPERATIONS

// HMSet salva hash multipli
// HMSet è deprecato in go-redis v9 quindi usiamo lo stesso HSet
func (c *Client) HMSet(ctx context.Context, key string, values map[string]any) error {
	return c.rdb.HSet(ctx, key, values).Err()
}

// HSet salva singolo campo hash
func (c *Client) HSet(ctx context.Context, key, field string, value any) error {
	return c.rdb.HSet(ctx, key, field, value).Err()
}

// HGetAll legge tutti campi hash
func (c *Client) HGetAll(ctx context.Context, key string) (map[string]string, error) {
	return c.rdb.HGetAll(ctx, key).Result()
}

// SetNX imposta una chiave solo se non esiste (Locking)
func (c *Client) SetNX(ctx context.Context, key string, value any, ttl time.Duration) (bool, error) {
	return c.rdb.SetNX(ctx, key, value, ttl).Result()
}

// HGet legge un singolo campo hash
func (c *Client) HGet(ctx context.Context, key, field string) (string, error) {
	return c.rdb.HGet(ctx, key, field).Result()
}

// KEY OPERATIONS

// Keys cerca pattern
func (c *Client) Keys(ctx context.Context, pattern string) ([]string, error) {
	return c.rdb.Keys(ctx, pattern).Result()
}

// Del cancella chiave
func (c *Client) Del(ctx context.Context, key string) error {
	return c.rdb.Del(ctx, key).Err()
}

func (c *Client) Pipeline() redis.Pipeliner {
	return c.rdb.Pipeline()
}
