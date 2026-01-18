package redis

import (
	"context"

	goredis "github.com/redis/go-redis/v9"
)

// ZAdd aggiunge elemento a sorted set
func (c *Client) ZAdd(ctx context.Context, key string, score float64, member string) error {
	return c.rdb.ZAdd(ctx, key, goredis.Z{
		Score:  score,
		Member: member,
	}).Err()
}

// ZRem rimuove elemento da sorted set
func (c *Client) ZRem(ctx context.Context, key string, member string) error {
	return c.rdb.ZRem(ctx, key, member).Err()
}

// ZRangeByScore query sorted set per range score
func (c *Client) ZRangeByScore(ctx context.Context, key string, min, max string) ([]string, error) {
	return c.rdb.ZRangeByScore(ctx, key, &goredis.ZRangeBy{
		Min: min,
		Max: max,
	}).Result()
}

// ZScore ottiene score di un membro
func (c *Client) ZScore(ctx context.Context, key string, member string) (float64, error) {
	return c.rdb.ZScore(ctx, key, member).Result()
}
