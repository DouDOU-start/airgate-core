package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/ent/account"
	"github.com/DouDOU-start/airgate-core/ent/usagelog"
)

const (
	windowCostCacheTTL      = 30 * time.Second
	defaultWindowHours      = 5.0
	windowCostThreshold     = 0.8 // 80% 进入 StickyOnly
	defaultStickyReserve    = 10.0 // 为粘性会话预留的额外额度
)

// WindowCostChecker 滑动窗口费用检查器
// 检查账户在最近 N 小时内的费用是否超过阈值
type WindowCostChecker struct {
	db  *ent.Client
	rdb *redis.Client
}

// NewWindowCostChecker 创建窗口费用检查器
func NewWindowCostChecker(db *ent.Client, rdb *redis.Client) *WindowCostChecker {
	return &WindowCostChecker{db: db, rdb: rdb}
}

// windowCostKey 生成 Redis 缓存键
func windowCostKey(accountID int) string {
	return fmt.Sprintf("window_cost:account:%d", accountID)
}

// GetSchedulability 检查账户窗口费用调度状态
// max_window_cost <= 0 表示不限制
func (w *WindowCostChecker) GetSchedulability(ctx context.Context, accountID int, extra map[string]interface{}) Schedulability {
	maxCost := ExtraFloat64(extra, "max_window_cost")
	if maxCost <= 0 {
		return Normal
	}

	windowHours := ExtraFloat64(extra, "window_hours")
	if windowHours <= 0 {
		windowHours = defaultWindowHours
	}

	cost, err := w.GetWindowCost(ctx, accountID, windowHours)
	if err != nil {
		slog.Debug("获取窗口费用失败，放行", "account_id", accountID, "error", err)
		return Normal // fail-open
	}

	stickyReserve := ExtraFloat64(extra, "sticky_reserve")
	if stickyReserve <= 0 {
		stickyReserve = defaultStickyReserve
	}

	ratio := cost / maxCost
	if cost >= maxCost+stickyReserve {
		return NotSchedulable // 超过预留额度，完全不可调度
	}
	if ratio >= windowCostThreshold {
		return StickyOnly // 接近上限，仅粘性会话
	}
	return Normal
}

// GetWindowCost 获取账户在指定窗口内的费用（带 Redis 缓存）
func (w *WindowCostChecker) GetWindowCost(ctx context.Context, accountID int, windowHours float64) (float64, error) {
	// 先查 Redis 缓存
	if w.rdb != nil {
		key := windowCostKey(accountID)
		val, err := w.rdb.Get(ctx, key).Result()
		if err == nil {
			if cost, parseErr := strconv.ParseFloat(val, 64); parseErr == nil {
				return cost, nil
			}
		}
	}

	// 缓存未命中，查数据库
	windowStart := time.Now().Add(-time.Duration(windowHours * float64(time.Hour)))

	var costs []struct {
		Sum float64 `json:"sum"`
	}

	err := w.db.UsageLog.Query().
		Where(
			usagelog.HasAccountWith(account.ID(accountID)),
			usagelog.CreatedAtGTE(windowStart),
		).
		Aggregate(ent.Sum(usagelog.FieldActualCost)).
		Scan(ctx, &costs)

	if err != nil {
		return 0, fmt.Errorf("查询窗口费用失败: %w", err)
	}

	cost := 0.0
	if len(costs) > 0 {
		cost = costs[0].Sum
	}

	// 写入 Redis 缓存
	if w.rdb != nil {
		key := windowCostKey(accountID)
		w.rdb.Set(ctx, key, strconv.FormatFloat(cost, 'f', 8, 64), windowCostCacheTTL)
	}

	return cost, nil
}

// addCostScript 仅当 key 存在时增量更新，避免创建无 TTL 的 key
var addCostScript = redis.NewScript(`
	if redis.call('EXISTS', KEYS[1]) == 1 then
		return redis.call('INCRBYFLOAT', KEYS[1], ARGV[1])
	end
	return nil
`)

// AddCost 在请求计费后增量更新缓存的窗口费用
// 仅当缓存 key 存在时才增量更新，不存在则等下次 GetWindowCost 查询时从 DB 重建
func (w *WindowCostChecker) AddCost(ctx context.Context, accountID int, cost float64) {
	if w.rdb == nil || cost <= 0 {
		return
	}
	key := windowCostKey(accountID)
	addCostScript.Run(ctx, w.rdb, []string{key}, cost)
}
