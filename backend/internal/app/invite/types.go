package invite

import (
	"context"
	"time"
)

// Profile 用户邀请画像（get-or-create，每用户一条）。
type Profile struct {
	UserID             int
	InviteCode         string
	InviterID          *int
	RebateRateOverride *float64 // 专属返利比例（百分比 0-100），NULL 沿用全局比例
	InvitedCount       int
	RebateBalance      float64 // 待转入余额的返利额度
	RebateTotal        float64 // 历史累计返利
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// InviteRelation 邀请关系条目（用户端"我邀请的人" / 管理端全量列表复用）。
type InviteRelation struct {
	InviterID       int
	InviterEmail    string
	InviterUsername string
	InviteeID       int
	InviteeEmail    string
	InviteeUsername string
	CreatedAt       time.Time
	TotalRebate     float64 // 该邀请关系累计产生的返利（accrue 流水求和）
}

// RebateLog 返利流水条目（accrue / transfer）。
type RebateLog struct {
	ID              int
	UserID          int
	UserEmail       string // 管理端展示用
	Action          string // accrue | transfer
	Amount          float64
	SourceUserID    *int
	SourceUserEmail string
	SourceOrderNo   *string
	BalanceAfter    *float64
	CreatedAt       time.Time
}

// OverrideEntry 专属返利比例覆盖列表条目（管理端）。
type OverrideEntry struct {
	UserID             int
	Email              string
	Username           string
	InviteCode         string
	RebateRateOverride *float64
	InvitedCount       int
}

// MyInfo 用户端"我的邀请"聚合信息。
type MyInfo struct {
	Enabled              bool
	InviteCode           string
	InviterID            *int
	EffectiveRatePercent float64
	InvitedCount         int
	RebateBalance        float64
	RebateTotal          float64
}

// ListFilter 分页筛选。
type ListFilter struct {
	Page     int
	PageSize int
	Keyword  string // 仅管理端列表使用：匹配邮箱/用户名
}

// Repository 邀请返利域持久化接口（唯一 ent 落点在 infra/store）。
type Repository interface {
	// EnsureProfile get-or-create：不存在则生成随机邀请码并创建。
	EnsureProfile(ctx context.Context, userID int) (Profile, error)
	// GetProfile 查询画像，不存在返回 ErrProfileNotFound（不自动创建）。
	GetProfile(ctx context.Context, userID int) (Profile, error)
	GetByCode(ctx context.Context, code string) (Profile, error)
	// BindInviter 条件更新 WHERE inviter_id IS NULL，返回是否绑定成功（false=已绑定过）。
	BindInviter(ctx context.Context, userID, inviterID int) (bool, error)
	// AccrueRebate 单事务：邀请人 rebate_balance/rebate_total 累加 + 写 accrue 流水（幂等键兜底）。
	AccrueRebate(ctx context.Context, inviterID, sourceUserID int, amount float64, sourceOrderNo, idempotencyKey string) (bool, error)
	// TransferToBalance 单事务：清零 rebate_balance → 用户加余额 + BalanceLog + transfer 流水。
	TransferToBalance(ctx context.Context, userID int) (transferred, newBalance float64, err error)
	SetRateOverride(ctx context.Context, userID int, ratePercent *float64) error

	// ListInvitees 用户端：inviterID 邀请的人（分页）。
	ListInvitees(ctx context.Context, inviterID int, f ListFilter) ([]InviteRelation, int64, error)
	// AdminListInvitees 管理端：全量邀请关系（分页，keyword 匹配邀请人/被邀请人邮箱）。
	AdminListInvitees(ctx context.Context, f ListFilter) ([]InviteRelation, int64, error)

	// ListRebateLogs 用户端：userID 自己的流水（分页）。
	ListRebateLogs(ctx context.Context, userID int, f ListFilter) ([]RebateLog, int64, error)
	// AdminListRebateLogs 管理端：全量流水（分页）。
	AdminListRebateLogs(ctx context.Context, f ListFilter) ([]RebateLog, int64, error)

	// AdminListOverrides 管理端：有专属比例覆盖的用户列表（分页，keyword 搜索）。
	AdminListOverrides(ctx context.Context, f ListFilter) ([]OverrideEntry, int64, error)
}

// SettingItem 模块配置项（settings 表 invite 组）。
type SettingItem struct {
	Key   string
	Value string
}

// SettingsLister 模块配置读取接口（由 settings service 适配实现）。
type SettingsLister interface {
	List(ctx context.Context, group string) ([]SettingItem, error)
}
