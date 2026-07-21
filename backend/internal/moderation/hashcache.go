package moderation

import (
	"context"

	"github.com/redis/go-redis/v9"
)

// HashCache 已命中输入哈希缓存：flagged 过的输入 hash 入集合，
// pre_hash_check 开启时同内容再次提交直接拦截（免外部 API 调用）。
type HashCache interface {
	Record(ctx context.Context, inputHash string) error
	Has(ctx context.Context, inputHash string) (bool, error)
	Delete(ctx context.Context, inputHash string) (bool, error)
	Clear(ctx context.Context) (int64, error)
	Count(ctx context.Context) (int64, error)
}

const flaggedHashSetKey = "airgate:moderation:flagged_hashes"

// RedisHashCache Redis Set 实现。
type RedisHashCache struct {
	client *redis.Client
}

func NewRedisHashCache(client *redis.Client) *RedisHashCache {
	return &RedisHashCache{client: client}
}

func (c *RedisHashCache) Record(ctx context.Context, inputHash string) error {
	return c.client.SAdd(ctx, flaggedHashSetKey, inputHash).Err()
}

func (c *RedisHashCache) Has(ctx context.Context, inputHash string) (bool, error) {
	return c.client.SIsMember(ctx, flaggedHashSetKey, inputHash).Result()
}

func (c *RedisHashCache) Delete(ctx context.Context, inputHash string) (bool, error) {
	n, err := c.client.SRem(ctx, flaggedHashSetKey, inputHash).Result()
	return n > 0, err
}

func (c *RedisHashCache) Clear(ctx context.Context) (int64, error) {
	n, err := c.client.SCard(ctx, flaggedHashSetKey).Result()
	if err != nil {
		return 0, err
	}
	if err := c.client.Del(ctx, flaggedHashSetKey).Err(); err != nil {
		return 0, err
	}
	return n, nil
}

func (c *RedisHashCache) Count(ctx context.Context) (int64, error) {
	return c.client.SCard(ctx, flaggedHashSetKey).Result()
}

var _ HashCache = (*RedisHashCache)(nil)
