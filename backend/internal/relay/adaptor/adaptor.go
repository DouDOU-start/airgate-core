// Package adaptor 定义 relay 协议适配器契约（纯透传，零翻译）：
// adaptor 只做上游 URL 拼接、认证头、渠道模型名重写、param_override，
// 以及从各协议响应中提取 usage 供计费（计量不是翻译，必须精确保留）；
// 请求/响应体一律原样透传，不做任何跨协议翻译。
// 调度、重试、禁用、计费一律在 pipeline（入口协议与渠道协议同构由 registry.Pick 保证）。
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

// 入口端点标识：BuildRequest 据此选上游 URL 与定点改写策略；
// pipeline 据此选流式 usage 提取/观察策略。调用方须显式传端点。
const (
	// EndpointChatCompletions OpenAI chat completions（/v1/chat/completions）。
	EndpointChatCompletions = "chat_completions"
	// EndpointResponses OpenAI Responses API（/v1/responses）。
	EndpointResponses = "responses"
	// EndpointImagesGenerations OpenAI 生图（/v1/images/generations，JSON 透传）。
	EndpointImagesGenerations = "images_generations"
	// EndpointImagesEdits OpenAI 图像编辑（/v1/images/edits，multipart 原样透传）。
	EndpointImagesEdits = "images_edits"
	// EndpointMessages Anthropic Messages API（/v1/messages）。
	EndpointMessages = "messages"
	// EndpointMessagesCountTokens Anthropic token 计数（/v1/messages/count_tokens，零计费）。
	EndpointMessagesCountTokens = "messages_count_tokens"
	// EndpointGenerateContent Gemini generateContent / streamGenerateContent
	//（/v1beta/models/{model}:generateContent）。
	EndpointGenerateContent = "generate_content"
	// EndpointPredict Gemini Imagen 生图（/v1beta/models/{model}:predict）。
	EndpointPredict = "predict"
	// EndpointCountTokens Gemini token 计数（/v1beta/models/{model}:countTokens，零计费）。
	EndpointCountTokens = "count_tokens"
)

// RelayInfo 单次上游调用的上下文（每个 failover attempt 独立构造）。
type RelayInfo struct {
	// ChannelKey 本次选中的密钥端点快照（只读）：BaseURL 来自所属渠道，
	// 类型/模型/param_override/header_override 等均为该把 key 的配置。
	ChannelKey *registry.ChannelKeySnapshot
	// APIKey 本次选中的上游密钥（明文）。
	APIKey string
	// RequestModel 对外模型名（客户端请求原始值）。
	RequestModel string
	// UpstreamModel 上游模型名（经渠道 model_mapping 映射后）。
	UpstreamModel string
	// Stream 是否流式请求。
	Stream bool
	// Endpoint 入口端点（Endpoint* 常量，调用方恒显式传入）。
	Endpoint string
	// RawBody 非 JSON 端点（multipart 等）的原始请求体：非 nil 时 adaptor 用
	// 原始字节直发上游（不重组），req 字段表仅承载调度所需 model/stream。
	RawBody []byte
	// RawContentType 与 RawBody 配套的原始 Content-Type（含 boundary，原样转发上游）。
	RawContentType string
	// Client 出口 HTTP 客户端（管线共享复用）。
	Client *http.Client
}

// Adaptor 协议适配器接口（零翻译契约）。
type Adaptor interface {
	// BuildRequest 构建上游 HTTP 请求：URL 拼接、认证头 + header_override、
	// model 重写、param_override（openai chat 另有 stream_options.include_usage 注入）。
	// 请求体在原始字段表上做定点改写后原样透传，不构造/翻译新请求体。
	// 传入的 req 不会被就地修改（内部 Clone）。
	BuildRequest(ctx context.Context, info *RelayInfo, req *dto.ChatRequest) (*http.Request, error)

	// ParseNonStreamResponse 解析非流式 2xx 响应体：
	// 按渠道协议提取 usage 并归一化到 dto.Usage（无则返回 nil），
	// 把响应 model 字段回写为 RequestModel（隐藏渠道 model_mapping），其余原样。
	// 解析失败时原样返回 body。
	ParseNonStreamResponse(info *RelayInfo, body []byte) ([]byte, *dto.Usage)
}

// StreamObserver 透传型 usage 观察器（每条流一个实例）：
// 管线把上游 SSE 字节原样转发给客户端，观察器旁路逐行解析、不产出/改写任何输出。
//
// ObserveLine 观察一行上游原始行（含 event: / data: / 空行）。
// Usage 返回累积用量（如 Anthropic message_start 已送达的 input/cache token）；
// 上游中途断连时管线据此按已知用量计费，避免记 0。
// Err 返回上游流内错误事件（如 Anthropic overloaded_error）——非 nil 时管线按
// 流中断处理（不 MarkRecovered、落 streamAborted 失败留痕），不把截断响应伪装成完整。
// Done 返回是否观察到协议级完成信号（Anthropic message_stop / Gemini 末 chunk 的
// finishReason）——管线据此区分「完整流」与「上游静默断流」。
type StreamObserver interface {
	ObserveLine(line string)
	Usage() (dto.Usage, bool)
	Err() error
	Done() bool
}

// StreamObserving 可选能力接口：原生协议适配器（anthropic/gemini）提供流观察器；
// 未实现时管线按 OpenAI SSE 语义处理（内联 usage 捕获 + [DONE] 判定）。
type StreamObserving interface {
	NewStreamObserver(info *RelayInfo) StreamObserver
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
