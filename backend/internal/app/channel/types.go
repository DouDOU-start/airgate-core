package channel

import (
	"context"
	"time"
)

// 密钥端点状态常量，与 ent schema 的 channel_key.status 枚举一致。
const (
	StatusEnabled        = "enabled"
	StatusDisabledManual = "disabled_manual"
	StatusDisabledAuto   = "disabled_auto"
)

// 密钥端点健康状态常量，与 ent schema 的 channel_key.health_status 枚举一致。
const (
	HealthHealthy    = "healthy"
	HealthDegraded   = "degraded"
	HealthSuspended  = "suspended"
	HealthRecovering = "recovering"
)

// 批量操作动作常量。
const (
	BulkActionEnable      = "enable"
	BulkActionDisable     = "disable"
	BulkActionDelete      = "delete"
	BulkActionSetPriority = "set_priority"
)

// 密钥列表（密钥视图）可排序字段与排序方向常量。
const (
	KeySortByPriority  = "priority"
	KeySortByWeight    = "weight"
	KeySortByName      = "name"
	KeySortByStatus    = "status"
	KeySortByCreatedAt = "created_at"

	SortOrderAsc  = "asc"
	SortOrderDesc = "desc"
)

// Repository 定义渠道域持久化接口。
type Repository interface {
	List(context.Context, ListFilter) ([]Channel, int64, error)
	// ListAll 全量加载（含 keys 及其 groups 边），供注册表 Reload 使用。
	ListAll(context.Context) ([]Channel, error)
	FindByID(context.Context, int) (Channel, error)
	Create(context.Context, CreateInput) (Channel, error)
	Update(context.Context, int, UpdateInput) (Channel, error)
	Delete(context.Context, int) error
	// BulkUpdate 批量启停/删除/调优先级（作用于选中渠道下的全部 key；
	// delete 删渠道），返回受影响渠道数。
	BulkUpdate(context.Context, BulkUpdateInput) (int, error)

	// ListKeys 密钥视图：跨渠道平铺分页查询 key（keyword 同时匹配 key 名与所属渠道名），
	// 支持按 priority/weight/name/status/created_at 排序。
	ListKeys(context.Context, KeyListFilter) ([]ChannelKey, int64, error)

	// FindKeyByID 按密钥端点 ID 查单把 key（含所属渠道 base_url 与 groups 边）。
	FindKeyByID(ctx context.Context, keyID int) (ChannelKey, error)
	// CreateKey 在指定渠道下新增一把 key。
	CreateKey(ctx context.Context, channelID int, key KeyInput) (ChannelKey, error)
	// UpdateKey 单把密钥端点 partial 更新（模型弹窗等按 key 编辑用）。
	UpdateKey(ctx context.Context, keyID int, key KeyInput) (ChannelKey, error)
	// DeleteKey 删除一把 key。
	DeleteKey(ctx context.Context, keyID int) error
	// UpdateKeyState 更新密钥端点调度状态（注册表异步落库与测试恢复共用）。
	UpdateKeyState(ctx context.Context, keyID int, status string, errMsg string) error
	// UpdateKeyTestResult 记录密钥端点测试结果。
	UpdateKeyTestResult(ctx context.Context, keyID int, responseTimeMs int, testedAt time.Time) error
	// UpdateKeyBalance 记录密钥端点余额刷新结果（key 级）。
	UpdateKeyBalance(ctx context.Context, keyID int, balance float64, updatedAt time.Time) error

	// ---- 健康探针 ----
	// UpdateKeyHealthState 更新密钥端点健康状态与计数器。
	UpdateKeyHealthState(ctx context.Context, keyID int, health string, failures, successes int) error
	// UpdateKeyProbeTime 记录最近一次探针执行时间。
	UpdateKeyProbeTime(ctx context.Context, keyID int, at time.Time) error
	// ListProbeEnabledKeys 查询所有 probe_enabled=true 的 key 的健康快照。
	ListProbeEnabledKeys(ctx context.Context) ([]KeyHealthSnapshot, error)
	// ListBalanceSyncTargets 查询 balance_check_enabled=true 且余额过期的已启用 key ID。
	ListBalanceSyncTargets(ctx context.Context, staleBefore time.Time) ([]int, error)

	// ---- 上游倍率探测 ----
	// ListUpstreamRateTargets 查询 upstream_rate_enabled=true 的 key（含 base_url + 解密 API key）。
	ListUpstreamRateTargets(ctx context.Context) ([]UpstreamRateTarget, error)
	// UpdateUpstreamRate 更新密钥端点的上游倍率探测结果。
	UpdateUpstreamRate(ctx context.Context, keyID int, rate float64, at time.Time) error
}

// MoneyStats 渠道金额与延迟统计：
// Cost = Σ(total_cost × account_rate_multiplier) 渠道成本；Revenue = Σ(actual_cost) 平台真实收入。
// Today* 为今日口径（created_at >= 调用方时区的当日零点），其余为累计口径。
// AvgFirstTokenMs 为最近 5 分钟窗口的平均首字延迟（ms），窗口内无样本时为 0。
type MoneyStats struct {
	Cost            float64
	Revenue         float64
	TodayCost       float64
	TodayRevenue    float64
	AvgFirstTokenMs float64
}

// StatsReader 密钥端点金额聚合读取器（由 store 基于 usage_logs 实现），列表页展示成本/收益用。
// todayStart 为今日口径的起点（按调用方时区解析的当日零点）。
type StatsReader interface {
	GetChannelKeyMoneyStats(ctx context.Context, channelKeyIDs []int, todayStart time.Time) (map[int]MoneyStats, error)
}

// Channel 渠道领域对象（供应商级容器）。余额与成本/收益均下沉到 key，
// 渠道层的 Balance / Total* 为其下各 key 的汇总（rollup，非落库字段）。
type Channel struct {
	ID        int
	Name      string
	BaseURL   string
	Keys      []ChannelKey
	CreatedAt time.Time
	UpdatedAt time.Time

	// 以下为各 key 汇总（列表查询时由 service 计算填充，不落库）。
	Balance          float64
	BalanceUpdatedAt *time.Time
	TotalCost        float64
	TotalRevenue     float64
	TodayCost        float64
	TodayRevenue     float64
}

// ChannelKey 渠道下的一把密钥端点领域对象。APIKey 存密文（AES-GCM base64），
// APIKeyHint 由 service 解密生成（尾 4 位提示），不落库；BaseURL 由所属渠道反规范化填充。
type ChannelKey struct {
	ID               int
	ChannelID        int
	ChannelName      string
	BaseURL          string
	Name             string
	Type             string
	APIKey           string
	APIKeyHint       string
	Models           []string
	ModelMapping     map[string]string
	ParamOverride    map[string]any
	HeaderOverride   map[string]string
	Status           string
	ErrorMsg         string
	Priority         int
	Weight           int
	MaxConcurrency   int
	MaxRPM           int
	CostRatio        float64
	Tags             []string
	TestModel        string
	ResponseTimeMs   int
	TestedAt         *time.Time
	LastUsedAt       *time.Time
	Balance          float64
	BalanceUpdatedAt *time.Time
	// BalanceCheckEnabled 是否参与主动余额刷新（自动/批量）；关闭后手动单把查询仍可用。
	BalanceCheckEnabled bool

	// ---- 健康探针 ----
	ProbeEnabled         bool
	ProbeModel           string
	HealthStatus         string
	ConsecutiveFailures  int
	ConsecutiveSuccesses int
	LastProbeAt          *time.Time

	// ---- 上游倍率探测 ----
	UpstreamRateEnabled bool
	UpstreamRatePath    string
	UpstreamRate        float64
	UpstreamRateAt      *time.Time

	GroupIDs  []int
	CreatedAt time.Time
	UpdatedAt time.Time

	// CurrentConcurrency / CurrentRPM 运行时观测指标（在途请求数 / 当前分钟请求数），
	// 仅列表查询时由 SetRuntimeStatsReaders 注入的读取器填充，不落库。
	CurrentConcurrency int
	CurrentRPM         int
	// TotalCost / TotalRevenue 累计金额（key 成本 / 平台真实收入），
	// TodayCost / TodayRevenue 为今日口径，AvgFirstTokenMs 为最近 5 分钟平均首字延迟（ms），
	// 列表查询时由 StatsReader 填充，不落库。
	TotalCost       float64
	TotalRevenue    float64
	TodayCost       float64
	TodayRevenue    float64
	AvgFirstTokenMs float64
}

// ListFilter 渠道列表查询参数。type/status/tag/group 作用于渠道下的 key。
type ListFilter struct {
	Page     int
	PageSize int
	Keyword  string
	Type     string
	Status   string
	Tag      string
	GroupID  *int
	// TZ 调用方 IANA 时区名，决定今日金额口径的当日起点；为空时用服务器本地时区。
	TZ string
}

// ListResult 渠道分页结果。
type ListResult struct {
	List     []Channel
	Total    int64
	Page     int
	PageSize int
}

// KeyListFilter 密钥视图（跨渠道平铺）列表查询参数。
// Keyword 同时匹配 key 名与所属渠道名；SortBy 为空时按 created_at desc（同渠道列表默认序）。
type KeyListFilter struct {
	Page      int
	PageSize  int
	Keyword   string
	Type      string
	Status    string
	Tag       string
	ChannelID *int
	GroupID   *int
	SortBy    string
	SortOrder string
	// TZ 调用方 IANA 时区名，决定今日金额口径的当日起点；为空时用服务器本地时区。
	TZ string
}

// KeyListResult 密钥视图分页结果。
type KeyListResult struct {
	List     []ChannelKey
	Total    int64
	Page     int
	PageSize int
}

// KeyInput 单把密钥端点的写入输入（新增/更新共用）。
//   - APIKey 传明文；更新既有 key 时空串 = 保持原密钥不变。
//   - 指针标量 nil = 新增取默认 / 更新不改。
//   - Models/ModelMapping/ParamOverride/HeaderOverride/Tags/GroupIDs
//     非 nil = 整组替换；更新既有 key 时 nil = 不改。
type KeyInput struct {
	Name           string
	Type           string
	APIKey         string
	Models         []string
	ModelMapping   map[string]string
	ParamOverride  map[string]any
	HeaderOverride map[string]string
	Status         *string
	Priority       *int
	Weight         *int
	MaxConcurrency *int
	MaxRPM         *int
	CostRatio      *float64
	Tags           []string
	TestModel      *string
	// BalanceCheckEnabled nil = 新增取默认 true / 更新不改。
	BalanceCheckEnabled *bool
	// ProbeEnabled nil = 新增取默认 false / 更新不改。
	ProbeEnabled *bool
	ProbeModel   *string
	// UpstreamRateEnabled nil = 新增取默认 false / 更新不改。
	UpstreamRateEnabled *bool
	UpstreamRatePath    *string
	GroupIDs            []int
}

// CreateInput 创建渠道输入（仅供应商级字段；key 建后单独添加）。
type CreateInput struct {
	Name    string
	BaseURL string
}

// UpdateInput 更新渠道输入（partial，仅 Name/BaseURL；key 单独增删改）。
type UpdateInput struct {
	Name    *string
	BaseURL *string
}

// BulkUpdateInput 批量操作输入（IDs 为渠道 ID）。
type BulkUpdateInput struct {
	IDs      []int
	Action   string
	Priority *int
}

// ImportChannelInput 导入渠道输入（含其下密钥列表）。
type ImportChannelInput struct {
	Name    string
	BaseURL string
	Keys    []KeyInput
}

// ImportResult 导入结果统计。
type ImportResult struct {
	Channels int
	Keys     int
}

// UpstreamRateTarget 上游倍率探测目标（store 层返回，含密文 API key 和 base_url）。
type UpstreamRateTarget struct {
	KeyID            int
	BaseURL          string
	APIKeyCipher     string // AES-GCM 密文
	UpstreamRatePath string
}

// KeyHealthSnapshot 密钥端点健康状态快照（探针调度用）。
type KeyHealthSnapshot struct {
	KeyID                int
	HealthStatus         string
	ConsecutiveFailures  int
	ConsecutiveSuccesses int
	LastProbeAt          *time.Time
}
