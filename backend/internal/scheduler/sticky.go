package scheduler

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// stickyTTL 粘性会话默认过期时间
	stickyTTL = 30 * time.Minute
)

// StickySession 粘性会话管理
// 通过 Redis 缓存 session → account 映射，实现对话上下文连续性
type StickySession struct {
	rdb *redis.Client
	ttl time.Duration
}

// NewStickySession 创建粘性会话管理器
func NewStickySession(rdb *redis.Client) *StickySession {
	return &StickySession{
		rdb: rdb,
		ttl: stickyTTL,
	}
}

// stickyKey 生成 Redis Key
// 格式：sticky:{user_id}:{platform}:{session_id}
func stickyKey(userID int, platform, sessionID string) string {
	return fmt.Sprintf("sticky:%d:%s:%s", userID, platform, sessionID)
}

// Get 获取粘性会话绑定的账户 ID
func (s *StickySession) Get(ctx context.Context, userID int, platform, sessionID string) (accountID int, found bool) {
	if s.rdb == nil {
		return 0, false
	}

	key := stickyKey(userID, platform, sessionID)
	val, err := s.rdb.Get(ctx, key).Result()
	if err != nil {
		return 0, false
	}

	id, err := strconv.Atoi(val)
	if err != nil {
		return 0, false
	}
	return id, true
}

// Set 设置粘性会话绑定（同时续期 TTL）
func (s *StickySession) Set(ctx context.Context, userID int, platform, sessionID string, accountID int) {
	if s.rdb == nil {
		return
	}

	key := stickyKey(userID, platform, sessionID)
	s.rdb.Set(ctx, key, strconv.Itoa(accountID), s.ttl)
}
