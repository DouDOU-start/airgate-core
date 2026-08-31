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
	// EndpointCompact Codex/OpenAI Responses compact API (/v1/responses/compact).
	// Compact is a distinct unary endpoint and must not be translated as a
	// normal Responses request.
	EndpointCompact = "compact"
	// EndpointImagesGenerations OpenAI 生图（/v1/images/generations，JSON 透传）。
	EndpointImagesGenerations = "images_generations"
	// EndpointImagesEdits OpenAI 图像编辑（/v1/images/edits，JSON 与 multipart 双形态）。
	EndpointImagesEdits = "images_edits"

	// Official Codex backend-client management contracts. These are raw native
	// requests and intentionally do not share the ordinary text translation
	// endpoint identifiers.
	EndpointCodexUsage                        = "codex_usage"
	EndpointCodexThreadUsage                  = "codex_thread_usage"
	EndpointCodexRateLimitResetCredits        = "codex_rate_limit_reset_credits"
	EndpointCodexRateLimitResetCreditsConsume = "codex_rate_limit_reset_credits_consume"
	EndpointCodexAccountsCheck                = "codex_accounts_check"
	EndpointCodexAccountsNudge                = "codex_accounts_send_add_credits_nudge_email"
	EndpointCodexProfilesMe                   = "codex_profiles_me"
	EndpointCodexConfigBundle                 = "codex_config_bundle"
	EndpointCodexSettingsUser                 = "codex_settings_user"
	EndpointCodexTasks                        = "codex_tasks"
	EndpointCodexTasksList                    = "codex_tasks_list"
	EndpointCodexTaskDetails                  = "codex_task_details"
	EndpointCodexTaskSiblingTurns             = "codex_task_sibling_turns"
	EndpointCodexWorkspaceMessages            = "codex_workspace_messages"
	EndpointCodexPSMCP                        = "codex_ps_mcp"
	// Official cloud-tasks environment discovery contracts. The global list
	// and repository-scoped lookup are raw/native GETs; the latter carries
	// provider/owner/repo and an optional ref as opaque path segments.
	EndpointCodexEnvironments       = "codex_environments"
	EndpointCodexEnvironmentsByRepo = "codex_environments_by_repo"
	// Official ChatGPT remote-plugin catalog and mutation contracts. These
	// endpoints are OAuth-only control-plane calls; their dynamic path
	// segments are validated by the pipeline route allowlist before forwarding.
	EndpointCodexPluginsList             = "codex_plugins_list"
	EndpointCodexPluginsSearch           = "codex_plugins_search"
	EndpointCodexPluginsSuggested        = "codex_plugins_suggested"
	EndpointCodexPluginsInstalled        = "codex_plugins_installed"
	EndpointCodexPluginsWorkspaceShared  = "codex_plugins_workspace_shared"
	EndpointCodexPluginsWorkspaceCreated = "codex_plugins_workspace_created"
	EndpointCodexPluginDetail            = "codex_plugin_detail"
	EndpointCodexPluginSkillDetail       = "codex_plugin_skill_detail"
	EndpointCodexPluginInstall           = "codex_plugin_install"
	EndpointCodexPluginUninstall         = "codex_plugin_uninstall"
	EndpointCodexPluginShares            = "codex_plugin_shares"
	// Connector directory and app metadata calls are part of the ChatGPT
	// backend Apps surface used by current Codex CLI builds. They are raw
	// control-plane requests, not model-generation traffic.
	EndpointCodexConnectorsDirectoryList          = "codex_connectors_directory_list"
	EndpointCodexConnectorsDirectoryListWorkspace = "codex_connectors_directory_list_workspace"
	EndpointCodexAppsBatch                        = "codex_apps_batch"
	// Legacy featured-plugin discovery/mutation remains in newer CLI builds
	// as a compatibility path alongside the /ps/plugins API.
	EndpointCodexPluginsFeatured       = "codex_plugins_featured"
	EndpointCodexPluginLegacyEnable    = "codex_plugin_legacy_enable"
	EndpointCodexPluginLegacyUninstall = "codex_plugin_legacy_uninstall"
	// Workspace plugin sharing uses the public plugin service path. Upload URL
	// issuance/finalization/delete are distinct endpoint IDs so method and
	// account-policy checks remain explicit at the Core boundary.
	EndpointCodexPluginsWorkspaceUploadURL = "codex_plugins_workspace_upload_url"
	EndpointCodexPluginsWorkspaceCreate    = "codex_plugins_workspace_create"
	EndpointCodexPluginsWorkspaceUpdate    = "codex_plugins_workspace_update"
	EndpointCodexPluginsWorkspaceDetail    = "codex_plugins_workspace_detail"
	EndpointCodexPluginsWorkspaceDelete    = "codex_plugins_workspace_delete"
	// Official Codex Remote Control HTTP contracts. These are ChatGPT OAuth
	// control-plane calls; the websocket server endpoint is intentionally kept
	// separate because it uses a duplex transport and a dedicated handshake.
	EndpointCodexRemoteControlEnroll          = "codex_remote_control_enroll"
	EndpointCodexRemoteControlRefresh         = "codex_remote_control_refresh"
	EndpointCodexRemoteControlPair            = "codex_remote_control_pair"
	EndpointCodexRemoteControlPairStatus      = "codex_remote_control_pair_status"
	EndpointCodexRemoteControlClientsList     = "codex_remote_control_clients_list"
	EndpointCodexRemoteControlClientRevoke    = "codex_remote_control_client_revoke"
	EndpointCodexRemoteControlServerWebSocket = "codex_remote_control_server_websocket"
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
	// EndpointAlphaSearch codex CLI 内置联网搜索（/v1/alpha/search，POST 非流式，按次计费）。
	EndpointAlphaSearch = "alpha_search"
	// EndpointRealtimeCalls is the Codex/OpenAI WebRTC call bootstrap endpoint
	// (/v1/realtime/calls). Its SDP, JSON, or multipart body is a native wire
	// contract and must never enter CPA translation.
	EndpointRealtimeCalls = "realtime_calls"
	// EndpointMemoriesTraceSummarize is the Codex memory-generation unary
	// endpoint (/v1/memories/trace_summarize). It is native-only and its JSON
	// body/response are forwarded without Responses translation.
	EndpointMemoriesTraceSummarize = "memories_trace_summarize"
	// EndpointRealtimeSideband is the provider WebSocket joined after a
	// realtime call is created. It is a native duplex contract, never CPA.
	EndpointRealtimeSideband = "realtime_sideband"
	// EndpointGuardian is the official Codex Guardian approval-review
	// Responses-compatible endpoint (/guardian). It is a native control-plane
	// contract and must not be sent through CPA translation.
	EndpointGuardian = "guardian"
	// EndpointGuardianClassifier is the official lightweight Guardian risk
	// classifier endpoint (/guardian-classifier). It is native-only for the same
	// reason as EndpointGuardian.
	EndpointGuardianClassifier = "guardian_classifier"
	// Codex control-plane endpoints used by the optional official History/Notes
	// extension. These are raw JSON POST contracts: they must never enter CPA
	// translation or model-token billing.
	EndpointHistoryListWindows     = "history_list_windows"
	EndpointHistoryListItems       = "history_list_items"
	EndpointHistoryReadItem        = "history_read_item"
	EndpointHistorySearchContents  = "history_search_contents"
	EndpointNotesListFilesByPrefix = "notes_list_files_by_prefix"
	EndpointNotesReadFile          = "notes_read_file"
	EndpointNotesSearchContents    = "notes_search_contents"
	EndpointNotesAppendToFile      = "notes_append_to_file"
	EndpointNotesWriteFile         = "notes_write_file"
	EndpointNotesThreadHint        = "notes_thread_hint"
	// EndpointAnalyticsEvents is the asynchronous Codex analytics sink. It is
	// intentionally zero-billing and native-only; failures are surfaced as the
	// upstream response but never retried through CPA translation.
	EndpointAnalyticsEvents = "analytics_events"
	// EndpointFilesCreate and EndpointFilesFinalize implement the official
	// Codex file-registration lifecycle. The large blob PUT is handled by
	// Core's opaque-token upload relay; these two JSON calls still run through
	// the native Codex executor so OAuth refresh, account proxy, and provider
	// headers stay identical to the rest of the native surface.
	EndpointFilesCreate   = "files_create"
	EndpointFilesFinalize = "files_finalize"
	// EndpointCodexTurnCosts is the API-key billing reconciliation endpoint
	// used by the official app-server turn-cost worker. It is a raw native
	// contract (POST /v1/analytics/codex/turn-costs), not a model-generation
	// request and therefore must never enter CPA translation or billing.
	EndpointCodexTurnCosts = "codex_turn_costs"
	// EndpointXAIVideosGenerations xAI 原生异步视频提交（/v1/videos/generations）。
	EndpointXAIVideosGenerations = "xai_videos_generations"
	// EndpointXAIVideosRetrieve xAI 原生异步视频查询（/v1/videos/{request_id}）。
	EndpointXAIVideosRetrieve = "xai_videos_retrieve"
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
	// ProviderPath and ProviderQuery carry the validated upstream shape for
	// byte-oriented native channel contracts such as OpenAI Realtime call
	// creation. Adaptors must continue to enforce their own finite path set.
	ProviderPath  string
	ProviderQuery map[string][]string
	// RequestHeaders carries safe end-to-end protocol metadata. Adaptors must
	// still replace authentication with the selected upstream credential.
	RequestHeaders http.Header
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
