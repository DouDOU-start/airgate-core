// Package accounttesthook 定义账号连接测试与插件运行时之间的稳定窄契约。
package accounttesthook

import (
	"context"
	"encoding/json"
	"errors"
)

const (
	// VersionV1 是账号测试请求变换协议的当前版本。
	VersionV1 = "v1"
	// TransformPath 是 Core 通过插件通用请求接口调用的逻辑路径。
	TransformPath = "/account-test-transform/v1/request"
)

// ErrUnavailable 表示当前没有可处理指定测试模式的运行中插件。
var ErrUnavailable = errors.New("账号测试请求变换插件不可用")

// Transformer 是账号服务使用的插件扩展点。与普通转发 Hook 不同，显式选择插件
// 测试模式后必须失败关闭，避免插件未生效却被误判为测试成功。
type Transformer interface {
	TransformAccountTest(ctx context.Context, request Request) (Decision, error)
}

// Request 是发给插件的版本化账号测试请求，只包含待发送的请求体和必要路由元数据。
// 账号凭证、代理和上游地址不会暴露给插件。
type Request struct {
	Version  string          `json:"version"`
	Mode     string          `json:"mode"`
	Platform string          `json:"platform"`
	Endpoint string          `json:"endpoint"`
	Model    string          `json:"model"`
	Body     json.RawMessage `json:"body"`
}

// Decision 是插件返回的完整替换请求体。
type Decision struct {
	Version     string          `json:"version"`
	RequestBody json.RawMessage `json:"request_body,omitempty"`
}
