package scheduler

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// defaultOverloadDuration 默认限流冷却时间（当上游未返回 RetryAfter 时使用）
	defaultOverloadDuration = 60 * time.Second
	// maxOverloadDuration 最大限流冷却时间，防止异常值
	maxOverloadDuration = 10 * time.Minute
	// defaultDegradedDuration 账号池耗尽时的默认降级窗口
	// 和 overload 不同：降级账号仍然可被选中，只是优先级被打到最低，
	// 只要还有其它非降级账号就会优先调度它们；窗口过后自动恢复正常优先级
	defaultDegradedDuration = 60 * time.Second
	// maxDegradedDuration 最大降级窗口
	maxDegradedDuration = 10 * time.Minute
)

// OverloadManager 账户临时限流管理
// 基于 Redis KEY + TTL 实现，收到 429 时设置冷却期，冷却期内不调度该账户
type OverloadManager struct {
	rdb *redis.Client
}

// NewOverloadManager 创建限流管理器
func NewOverloadManager(rdb *redis.Client) *OverloadManager {
	return &OverloadManager{rdb: rdb}
}

// overloadKey 生成 Redis Key
func overloadKey(accountID int) string {
	return fmt.Sprintf("overload:%d", accountID)
}

// degradedKey 降级窗口的 Redis Key
func degradedKey(accountID int) string {
	return fmt.Sprintf("degraded:%d", accountID)
}

// MarkOverloaded 标记账户为临时限流状态
// retryAfter 为上游返回的建议等待时间，<= 0 时使用默认值
func (m *OverloadManager) MarkOverloaded(ctx context.Context, accountID int, retryAfter time.Duration) {
	if m.rdb == nil {
		return
	}

	if retryAfter <= 0 {
		retryAfter = defaultOverloadDuration
	}
	if retryAfter > maxOverloadDuration {
		retryAfter = maxOverloadDuration
	}

	m.rdb.Set(ctx, overloadKey(accountID), "1", retryAfter)
}

// IsOverloaded 检查账户是否处于限流冷却期
func (m *OverloadManager) IsOverloaded(ctx context.Context, accountID int) bool {
	if m.rdb == nil {
		return false
	}

	exists, err := m.rdb.Exists(ctx, overloadKey(accountID)).Result()
	if err != nil {
		return false // fail-open
	}
	return exists > 0
}

// ClearOverload 清除账户限流状态（如需手动恢复）
func (m *OverloadManager) ClearOverload(ctx context.Context, accountID int) {
	if m.rdb == nil {
		return
	}
	m.rdb.Del(ctx, overloadKey(accountID))
}

// GetSchedulability 返回限流调度状态
func (m *OverloadManager) GetSchedulability(ctx context.Context, accountID int) Schedulability {
	if m.IsOverloaded(ctx, accountID) {
		return NotSchedulable
	}
	return Normal
}

// MarkDegraded 把账号打入临时降级窗口。
//
// 与 MarkOverloaded 的区别：
//   - MarkOverloaded: 硬排除，窗口内调度器完全跳过该账号
//   - MarkDegraded:   软降级，窗口内该账号仍然可被选中，但 score 被打到最低，
//     只要组内还有非降级账号就优先使用其它账号；组内全部降级时
//     才退化到降级账号兜底（避免池子抖动一次就全组没账号用）
//
// 典型场景：上游是账号池（sub2api 之类）时，池子返回
// "No available accounts" / 403 之类的耗尽错误，降级一个窗口
// 期让下一个请求优先尝试其它账号，窗口过期自动恢复。
func (m *OverloadManager) MarkDegraded(ctx context.Context, accountID int, duration time.Duration) {
	if m.rdb == nil {
		return
	}

	if duration <= 0 {
		duration = defaultDegradedDuration
	}
	if duration > maxDegradedDuration {
		duration = maxDegradedDuration
	}

	m.rdb.Set(ctx, degradedKey(accountID), "1", duration)
}

// IsDegraded 检查账号是否在降级窗口内。
func (m *OverloadManager) IsDegraded(ctx context.Context, accountID int) bool {
	if m.rdb == nil {
		return false
	}
	exists, err := m.rdb.Exists(ctx, degradedKey(accountID)).Result()
	if err != nil {
		return false // fail-open
	}
	return exists > 0
}

// ClearDegraded 手动清除降级状态
func (m *OverloadManager) ClearDegraded(ctx context.Context, accountID int) {
	if m.rdb == nil {
		return
	}
	m.rdb.Del(ctx, degradedKey(accountID))
}
