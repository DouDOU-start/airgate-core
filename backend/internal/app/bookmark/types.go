package bookmark

import (
	"context"
	"errors"
	"time"
)

var ErrBookmarkNotFound = errors.New("备忘录不存在")

// Repository 定义备忘录持久化接口。
type Repository interface {
	List(ctx context.Context, filter ListFilter) ([]Bookmark, int64, error)
	FindByID(ctx context.Context, id int) (Bookmark, error)
	Create(ctx context.Context, input CreateInput) (Bookmark, error)
	Update(ctx context.Context, id int, input UpdateInput) (Bookmark, error)
	Delete(ctx context.Context, id int) error
}

// Bookmark 描述备忘录领域对象。
type Bookmark struct {
	ID        int
	Name      string
	BaseURL   string
	Remark    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ListFilter 描述备忘录列表查询条件。
type ListFilter struct {
	Page     int
	PageSize int
	Keyword  string
}

// CreateInput 描述创建备忘录输入。
type CreateInput struct {
	Name    string
	BaseURL string
	Remark  string
}

// UpdateInput 描述更新备忘录输入（指针字段 nil = 不修改）。
type UpdateInput struct {
	Name    *string
	BaseURL *string
	Remark  *string
}
