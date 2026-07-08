// Package adaptor 定义 relay 协议适配器契约：
// adaptor 只做协议翻译（URL 拼接 / 认证头 / 请求体改写 / 响应解析），
// 调度、重试、禁用、计费一律在 pipeline。
//
// 实现注册：各协议子包（如 adaptor/openai）在 init 中调 Register 自注册，
// pipeline 经 GetAdaptor 按渠道类型取实现。
package adaptor

import (
	"context"
	"fmt"
	"net/http"

	"github.com/DouDOU-start/airgate-core/internal/relay/dto"
	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
)

// 入口端点标识：BuildRequest 据此选上游 URL 与请求改写策略；
// pipeline 据此选流式 usage 提取函数。空值按 EndpointChatCompletions 兼容。
const (
	// EndpointChatCompletions OpenAI chat completions（/v1/chat/completions）。
	EndpointChatCompletions = "chat_completions"
	// EndpointResponses OpenAI Responses API（/v1/responses）。
	EndpointResponses = "responses"
)

// RelayInfo 单次上游调用的上下文（每个 failover attempt 独立构造）。
type RelayInfo struct {
	// Channel 本次选中的渠道快照（只读）。
	Channel *registry.ChannelSnapshot
	// APIKey 本次轮询到的上游密钥（明文）。
	APIKey string
	// RequestModel 对外模型名（客户端请求原始值）。
	RequestModel string
	// UpstreamModel 上游模型名（经渠道 model_mapping 映射后）。
	UpstreamModel string
	// Stream 是否流式请求。
	Stream bool
	// Endpoint 入口端点（EndpointChatCompletions / EndpointResponses；空值按 chat_completions 兼容）。
	Endpoint string
	// EntryProtocol 入口协议（本阶段恒 "openai"）。
	EntryProtocol string
	// Client 出口 HTTP 客户端（管线共享复用）。
	Client *http.Client
}

// Adaptor 协议适配器接口。
type Adaptor interface {
	// BuildRequest 构建上游 HTTP 请求：URL 拼接、认证头 + header_override、
	// model 重写、param_override、stream_options.include_usage 注入。
	// 传入的 req 不会被就地修改（内部 Clone）。
	BuildRequest(ctx context.Context, info *RelayInfo, req *dto.ChatRequest) (*http.Request, error)

	// ParseNonStreamResponse 解析非流式 2xx 响应体：
	// 提取 usage（无则返回 nil），并把响应 model 字段回写为 RequestModel。
	// 解析失败时原样返回 body。
	ParseNonStreamResponse(info *RelayInfo, body []byte) ([]byte, *dto.Usage)
}

// factories 渠道类型 → 适配器工厂。注册发生在各子包 init，运行期只读，无需加锁。
var factories = map[string]func() Adaptor{}

// Register 注册渠道类型适配器工厂（由协议子包 init 调用）。
func Register(channelType string, factory func() Adaptor) {
	factories[channelType] = factory
}

// GetAdaptor 按渠道类型获取适配器；未注册类型报错。
func GetAdaptor(channelType string) (Adaptor, error) {
	factory, ok := factories[channelType]
	if !ok {
		return nil, fmt.Errorf("不支持的渠道类型: %s", channelType)
	}
	return factory(), nil
}
