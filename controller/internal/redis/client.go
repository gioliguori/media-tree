package redis

import (
	"context"
	"encoding/json"
	"fmt"

	"controller/internal/config"

	"github.com/redis/go-redis/v9"
)

type Client struct {
	rdb *redis.Client
}

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
// (utile per operazioni avanzate non wrappate)
func (c *Client) GetRedisClient() *redis.Client {
	return c.rdb
}

// PublishJSON pubblica un evento JSON su un canale
func (c *Client) PublishJSON(ctx context.Context, channel string, data map[string]interface{}) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}

	if err := c.rdb.Publish(ctx, channel, jsonData).Err(); err != nil {
		return fmt.Errorf("failed to publish to %s: %w", channel, err)
	}

	return nil
}

func (c *Client) Exists(ctx context.Context, key string) (bool, error) {
	result, err := c.rdb.Exists(ctx, key).Result()
	if err != nil {
		return false, err
	}
	return result > 0, nil
}
