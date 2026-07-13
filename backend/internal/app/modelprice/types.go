package modelprice

import (
	"context"
	"time"
)

// Repository 定义模型价目表持久化接口。
type Repository interface {
	List(context.Context, ListFilter) ([]ModelPrice, int64, error)
	// ListAll 全量加载，供 pricing 缓存重载使用。
	ListAll(context.Context) ([]ModelPrice, error)
	FindByID(context.Context, int) (ModelPrice, error)
	Create(context.Context, CreateInput) (ModelPrice, error)
	Update(context.Context, int, UpdateInput) (ModelPrice, error)
	Delete(context.Context, int) error

	// —— 模型标签（家族归类，归属模型管理）——
	ListTags(context.Context) ([]Tag, error)
	CreateTag(ctx context.Context, name string) (Tag, error)
	RenameTag(ctx context.Context, id int, name string) (Tag, error)
	// DeleteTag 删除标签并清空引用该标签的模型 tag_id（事务内）。
	DeleteTag(ctx context.Context, id int) error
}

// Tag 模型标签领域对象；ModelCount 为引用该标签的模型数（列表查询时填充）。
type Tag struct {
	ID         int
	Name       string
	ModelCount int64
}

// ModelPrice 模型价格领域对象。价格单位 USD / 1M tokens；PerRequestPrice 为 USD / 次。
type ModelPrice struct {
	ID                   int
	Model                string
	InputPrice           float64
	OutputPrice          float64
	CachedInputPrice     float64
	CacheCreationPrice   float64 // 缓存写入 5m TTL 单价
	CacheCreation1hPrice float64 // 缓存写入 1h TTL 单价
	PerRequestPrice      float64
	// PricingExtra 服务档倍率 + 长上下文阶梯等长尾维度（多数模型为空）。
	PricingExtra map[string]interface{}
	// TagID / TagName 模型标签（家族归类，可空；TagName 由 store 联查填充）。
	TagID   *int
	TagName string
	// MarketVisible 是否在模型广场（未登录可见的公开价目页）展示。
	MarketVisible bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// ListFilter 价目表列表查询参数。
type ListFilter struct {
	Page     int
	PageSize int
	Keyword  string
	// TagID 按标签过滤（nil = 不过滤）。
	TagID *int
	// MarketVisibleOnly 仅返回 market_visible=true 的条目（模型广场公开查询用）。
	MarketVisibleOnly bool
}

// ListResult 价目表分页结果。
type ListResult struct {
	List     []ModelPrice
	Total    int64
	Page     int
	PageSize int
}

// CreateInput 创建价格输入。
type CreateInput struct {
	Model                string
	InputPrice           float64
	OutputPrice          float64
	CachedInputPrice     float64
	CacheCreationPrice   float64
	CacheCreation1hPrice float64
	PerRequestPrice      float64
	PricingExtra         map[string]interface{}
	// TagID 模型标签（nil = 不挂标签）。
	TagID *int
	// MarketVisible 是否在模型广场展示。
	MarketVisible bool
}

// UpdateInput 更新价格输入（partial，指针字段）。
// PricingExtra 为整体替换语义：非 nil 时整块写入（空 map 清空扩展）。
// TagID 三态：nil = 不改；指向 0 = 清空标签；指向正数 = 设为该标签。
type UpdateInput struct {
	Model                *string
	InputPrice           *float64
	OutputPrice          *float64
	CachedInputPrice     *float64
	CacheCreationPrice   *float64
	CacheCreation1hPrice *float64
	PerRequestPrice      *float64
	PricingExtra         map[string]interface{}
	TagID                *int
	// MarketVisible nil = 不改；非 nil = 设为该值。
	MarketVisible *bool
}
