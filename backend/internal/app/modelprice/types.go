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
	// Upsert 按 model 名 upsert，返回新建与更新条数。
	Upsert(context.Context, []ImportItem) (created int, updated int, err error)
}

// ModelPrice 模型价格领域对象。价格单位 USD / 1M tokens；PerRequestPrice 为 USD / 次。
type ModelPrice struct {
	ID                 int
	Model              string
	InputPrice         float64
	OutputPrice        float64
	CachedInputPrice   float64
	CacheCreationPrice float64
	PerRequestPrice    float64
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// ListFilter 价目表列表查询参数。
type ListFilter struct {
	Page     int
	PageSize int
	Keyword  string
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
	Model              string
	InputPrice         float64
	OutputPrice        float64
	CachedInputPrice   float64
	CacheCreationPrice float64
	PerRequestPrice    float64
}

// UpdateInput 更新价格输入（partial，指针字段）。
type UpdateInput struct {
	Model              *string
	InputPrice         *float64
	OutputPrice        *float64
	CachedInputPrice   *float64
	CacheCreationPrice *float64
	PerRequestPrice    *float64
}

// ImportItem 批量导入条目（按 model 名 upsert）。
type ImportItem struct {
	Model              string
	InputPrice         float64
	OutputPrice        float64
	CachedInputPrice   float64
	CacheCreationPrice float64
	PerRequestPrice    float64
}

// ImportResult 批量导入结果。
type ImportResult struct {
	Created int
	Updated int
}
