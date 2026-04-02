package proxy

import (
	"context"
	"time"
)

const (
	defaultPage     = 1
	defaultPageSize = 20
)

// Repository 定义代理域持久化接口。
type Repository interface {
	List(context.Context, ListFilter) ([]Proxy, int64, error)
	FindByID(context.Context, int) (Proxy, error)
	Create(context.Context, CreateInput) (Proxy, error)
	Update(context.Context, int, UpdateInput) (Proxy, error)
	Delete(context.Context, int) error
}

// Proxy 代理领域对象。
type Proxy struct {
	ID        int
	Name      string
	Protocol  string
	Address   string
	Port      int
	Username  string
	Password  string
	Status    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ListFilter 代理列表查询参数。
type ListFilter struct {
	Page     int
	PageSize int
	Keyword  string
	Status   string
}

// ListResult 代理分页结果。
type ListResult struct {
	List     []Proxy
	Total    int64
	Page     int
	PageSize int
}

// CreateInput 创建代理输入。
type CreateInput struct {
	Name     string
	Protocol string
	Address  string
	Port     int
	Username string
	Password string
}

// UpdateInput 更新代理输入。
type UpdateInput struct {
	Name     *string
	Protocol *string
	Address  *string
	Port     *int
	Username *string
	Password *string
	Status   *string
}

// TestResult 代理连通性测试结果。
type TestResult struct {
	Success     bool
	Latency     int64
	ErrorMsg    string
	IPAddress   string
	Country     string
	CountryCode string
	City        string
}

func normalizePage(page, pageSize int) (int, int) {
	if page <= 0 {
		page = defaultPage
	}
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}
	return page, pageSize
}
