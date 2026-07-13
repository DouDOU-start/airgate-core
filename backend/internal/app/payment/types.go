package payment

import (
	"context"
	"time"
)

// 订单状态常量（与 ent schema status 字符串一致）。
const (
	StatusPending = "pending"
	StatusPaid    = "paid"
	StatusExpired = "expired"
)

// Order 充值订单领域对象。
type Order struct {
	ID            int
	OutTradeNo    string
	UserID        int
	UserEmail     string // 仅管理端列表填充
	Method        string
	ProviderID    string
	Amount        float64
	Status        string
	Subject       string
	ClientIP      string
	PaymentURL    string
	QRCodeContent string
	PaidAt        *time.Time
	ExpiresAt     time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// ProviderConfig 服务商实例配置领域对象。
// Config 为落库形态：敏感字段（password/textarea 类型）已 AES-256-GCM 加密。
type ProviderConfig struct {
	ProviderKey string
	Kind        string
	Enabled     bool
	Config      map[string]string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// CreateOrderInput 用户下单输入。
type CreateOrderInput struct {
	UserID   int
	Amount   float64
	Method   string
	Subject  string
	ClientIP string
}

// UserOrderFilter 用户充值记录分页筛选。
type UserOrderFilter struct {
	UserID   int
	Page     int
	PageSize int
}

// AdminOrderFilter 管理端订单列表筛选。
type AdminOrderFilter struct {
	Page     int
	PageSize int
	Email    string
	Status   string
}

// OrderStats 管理端订单统计。
type OrderStats struct {
	Total       int64
	Paid        int64
	Pending     int64
	Expired     int64
	TotalAmount float64 // 累计已支付金额
	TodayAmount float64 // 今日已支付金额
}

// CreditInput 回调入账输入。
type CreditInput struct {
	OutTradeNo    string
	Amount        float64 // 渠道回调告知的实际付款金额（店内与订单金额二次校验）
	NotifyPayload string
	Remark        string // 余额流水备注（如「在线充值（支付宝）」）
}

// CreditResult 回调入账结果。AlreadyPaid=true 为幂等命中（不重复入账、不发通知）；
// 首次成功入账时带上用户 ID、邮箱、入账金额与入账后余额，供充值成功邮件通知与
// 邀请返利计提使用。
type CreditResult struct {
	AlreadyPaid  bool
	UserID       int
	Email        string
	Amount       float64
	BalanceAfter float64
}

// Repository 支付域持久化接口（唯一 ent 落点在 infra/store）。
type Repository interface {
	CreateOrder(ctx context.Context, o Order) (Order, error)
	GetOrder(ctx context.Context, outTradeNo string) (Order, error)
	ListUserOrders(ctx context.Context, f UserOrderFilter) ([]Order, int64, error)
	AdminListOrders(ctx context.Context, f AdminOrderFilter) ([]Order, int64, error)
	OrderStats(ctx context.Context, todayStart time.Time) (OrderStats, error)
	// PaidAmountSince 用户自 since 起累计已支付金额（单日限额校验）。
	PaidAmountSince(ctx context.Context, userID int, since time.Time) (float64, error)
	// CreditPaidOrder 单事务完成：订单 pending→paid + 用户加余额 + 余额流水。
	// 订单已是 paid 时幂等返回 alreadyPaid=true 且不做任何写入；
	// 非 pending/paid 状态或回调金额与订单金额不符（容差 0.01 元）时报错。
	CreditPaidOrder(ctx context.Context, in CreditInput) (CreditResult, error)
	// ExpirePendingOrders 将 expires_at 已过期的 pending 订单置为 expired，返回条数。
	ExpirePendingOrders(ctx context.Context, now time.Time) (int, error)

	ListProviderConfigs(ctx context.Context) ([]ProviderConfig, error)
	GetProviderConfig(ctx context.Context, providerKey string) (ProviderConfig, error)
	UpsertProviderConfig(ctx context.Context, c ProviderConfig) error
	// RenameProviderConfig 改实例 ID，事务内同步更新历史订单的 provider_id 引用。
	RenameProviderConfig(ctx context.Context, oldKey, newKey string) error
	DeleteProviderConfig(ctx context.Context, providerKey string) error
	// NextProviderKeyForKind 生成 {kind}_{N} 形式的自增实例 ID。
	NextProviderKeyForKind(ctx context.Context, kind string) (string, error)
}

// SettingItem 模块配置项（settings 表 payment 组）。
type SettingItem struct {
	Key   string
	Value string
}

// SettingsLister 模块配置读取接口（由 settings service 适配实现）。
type SettingsLister interface {
	List(ctx context.Context, group string) ([]SettingItem, error)
}
