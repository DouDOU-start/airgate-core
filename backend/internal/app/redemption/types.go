package redemption

import (
	"context"
	"time"
)

// 兑换码状态常量（与 ent schema status 字符串一致）。
// expired 不落库：expires_at 已过的 unused 码在列表/统计中现算为过期。
const (
	StatusUnused   = "unused"
	StatusUsed     = "used"
	StatusDisabled = "disabled"
	StatusExpired  = "expired" // 虚拟状态，仅用于列表筛选与统计展示
)

// Code 兑换码领域对象。
type Code struct {
	ID          int
	Code        string
	Value       float64
	Status      string
	Remark      string
	UsedByID    int
	UsedByEmail string
	UsedAt      *time.Time
	ExpiresAt   *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Expired 判断兑换码在 now 时刻是否已过期（仅 unused 码有过期概念）。
func (c Code) Expired(now time.Time) bool {
	return c.Status == StatusUnused && c.ExpiresAt != nil && !c.ExpiresAt.After(now)
}

// GenerateInput 批量生成输入。
type GenerateInput struct {
	Count     int
	Value     float64
	Remark    string
	ExpiresAt *time.Time
}

// ListFilter 管理端列表筛选。
type ListFilter struct {
	Page     int
	PageSize int
	Status   string // unused/used/disabled/expired，空为全部
	Keyword  string // 匹配 code 或批次备注
}

// Stats 管理端统计（unused 已剔除过期码）。
type Stats struct {
	Total       int64
	Unused      int64
	Used        int64
	Disabled    int64
	Expired     int64
	UsedValue   float64 // 已兑换总面值
	UnusedValue float64 // 待兑换总面值（不含过期）
}

// RedeemResult 兑换结果。
type RedeemResult struct {
	Value   float64 // 本次入账金额
	Balance float64 // 入账后余额
}

// Repository 兑换码持久化接口（唯一 ent 落点在 infra/store）。
type Repository interface {
	CreateBatch(ctx context.Context, codes []Code) ([]Code, error)
	List(ctx context.Context, f ListFilter, now time.Time) ([]Code, int64, error)
	Stats(ctx context.Context, now time.Time) (Stats, error)
	FindByID(ctx context.Context, id int) (Code, error)
	// UpdateStatus 条件更新状态（WHERE status=from），并发抢占失败返回 ErrCodeStateConflict。
	UpdateStatus(ctx context.Context, id int, from, to string) error
	Delete(ctx context.Context, id int) error
	// Redeem 单事务完成：码 unused→used（条件更新抢占）+ 用户加余额 + 余额流水。
	// 防重三层：code 唯一约束 → 条件更新 WHERE status='unused' → balance_log 幂等键 redeem:<code>。
	Redeem(ctx context.Context, code string, userID int, now time.Time) (RedeemResult, error)
}
