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
	Group string
}

// TestSMTPInput 测试 SMTP 连接所需参数。
type TestSMTPInput struct {
	Host     string
	Port     int
	Username string
	Password string
	UseTLS   bool
	From     string
	To       string
}

// GenerateAdminAPIKeyResult 生成管理员 API Key 的返回结果。
type GenerateAdminAPIKeyResult struct {
	Hint string // 脱敏显示
	Key  string // 明文密钥（仅生成时返回一次）
}

// Repository 定义设置域持久化接口。
type Repository interface {
	List(context.Context, string) ([]Setting, error)
	UpsertMany(context.Context, []ItemInput) error
}
