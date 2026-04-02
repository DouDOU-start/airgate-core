package settings

import "context"

// Setting 表示系统设置领域对象。
type Setting struct {
	Key   string
	Value string
	Group string
}

// ItemInput 表示单条设置更新输入。
type ItemInput struct {
	Key   string
	Value string
}

// Repository 定义设置域持久化接口。
type Repository interface {
	List(context.Context, string) ([]Setting, error)
	UpsertMany(context.Context, []ItemInput) error
}
