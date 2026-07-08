package channel

import (
	"context"
	"time"
)

// 渠道状态常量，与 ent schema 的 status 枚举一致。
const (
	StatusEnabled        = "enabled"
	StatusDisabledManual = "disabled_manual"
	StatusDisabledAuto   = "disabled_auto"
)

// 批量操作动作常量。
const (
	BulkActionEnable      = "enable"
	BulkActionDisable     = "disable"
	BulkActionDelete      = "delete"
	BulkActionSetPriority = "set_priority"
)

// Repository 定义渠道域持久化接口。
type Repository interface {
	List(context.Context, ListFilter) ([]Channel, int64, error)
	// ListAll 全量加载（含 groups 边），供注册表 Reload 使用。
	ListAll(context.Context) ([]Channel, error)
	FindByID(context.Context, int) (Channel, error)
	Create(context.Context, CreateInput) (Channel, error)
	Update(context.Context, int, UpdateInput) (Channel, error)
	Delete(context.Context, int) error
	// BulkUpdate 批量启停/删除/调优先级，返回受影响行数。
	BulkUpdate(context.Context, BulkUpdateInput) (int, error)
	// UpdateState 更新渠道调度状态（注册表异步落库与测试恢复共用）。
	UpdateState(ctx context.Context, id int, status string, until *time.Time, errMsg string) error
	// UpdateTestResult 记录渠道测试结果。
	UpdateTestResult(ctx context.Context, id int, responseTimeMs int, testedAt time.Time) error
}

// Channel 渠道领域对象。APIKeys 存密文（AES-GCM base64），
// APIKeyHints 由 service 解密生成（尾 4 位提示），不落库。
type Channel struct {
	ID             int
	Name           string
	Type           string
	BaseURL        string
	APIKeys        []string
	APIKeyHints    []string
	Models         []string
	ModelMapping   map[string]string
	ParamOverride  map[string]any
	HeaderOverride map[string]string
	Status         string
	StatusUntil    *time.Time
	ErrorMsg       string
	Priority       int
	Weight         int
	MaxConcurrency int
	MaxRPM         int
	CostRatio      float64
	Tags           []string
	TestModel      string
	CustomConfig   map[string]any
	ResponseTimeMs int
	TestedAt       *time.Time
	LastUsedAt     *time.Time
	GroupIDs       []int
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// ListFilter 渠道列表查询参数。
type ListFilter struct {
	Page     int
	PageSize int
	Keyword  string
	Type     string
	Status   string
	Tag      string
	GroupID  *int
}

// ListResult 渠道分页结果。
type ListResult struct {
	List     []Channel
	Total    int64
	Page     int
	PageSize int
}

// CreateInput 创建渠道输入。APIKeys 传入明文，由 service 加密后落库；
// 可选数值字段用指针表达「未提供 → 取 schema 默认值」。
type CreateInput struct {
	Name           string
	Type           string
	BaseURL        string
	APIKeys        []string
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
	TestModel      string
	CustomConfig   map[string]any
	GroupIDs       []int
}

// UpdateInput 更新渠道输入（partial）：
//   - 指针字段 nil = 不改；
//   - APIKeys/Models 非空 = 整组替换（空 = 不改）；
//   - ModelMapping/ParamOverride/HeaderOverride/Tags/CustomConfig/GroupIDs
//     非 nil = 整组替换（可传空集合清空）。
type UpdateInput struct {
	Name           *string
	Type           *string
	BaseURL        *string
	APIKeys        []string
	Models         []string
	ModelMapping   map[string]string
	ParamOverride  map[string]any
	HeaderOverride map[string]string
	Status         *string
	// ErrorMsg / ClearStatusUntil 由 service 内部填充（status→enabled 时清理状态残留），
	// 不接受外部输入。
	ErrorMsg         *string
	ClearStatusUntil bool
	Priority         *int
	Weight           *int
	MaxConcurrency   *int
	MaxRPM           *int
	CostRatio        *float64
	Tags             []string
	TestModel        *string
	CustomConfig     map[string]any
	GroupIDs         []int
}

// BulkUpdateInput 批量操作输入。
type BulkUpdateInput struct {
	IDs      []int
	Action   string
	Priority *int
}
