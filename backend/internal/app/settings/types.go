package settings

import (
	"context"
	"time"
)

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

// TestWeChatInput 测试微信公众号模板消息所需参数。
type TestWeChatInput struct {
	AppID      string
	AppSecret  string
	TemplateID string
	OpenID     string
	DetailURL  string
}

// WeChatTester 由基础设施层实现，避免设置用例直接依赖微信客户端。
type WeChatTester interface {
	SendTest(ctx context.Context, input TestWeChatInput) error
}

// WeChatBindSession 管理员扫码绑定会话。
type WeChatBindSession struct {
	ID        string
	OAuthURL  string
	ExpiresAt time.Time
}

// WeChatBindStatus 管理员扫码绑定状态。
type WeChatBindStatus struct {
	Status     string
	OpenIDHint string
}

// WeChatBinder 管理员微信扫码绑定编排接口。
type WeChatBinder interface {
	CreateBind(ctx context.Context) (WeChatBindSession, error)
	CompleteBind(ctx context.Context, code, state string) (WeChatBindStatus, error)
	BindStatus(ctx context.Context, id string) (WeChatBindStatus, error)
	Unbind(ctx context.Context) error
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
