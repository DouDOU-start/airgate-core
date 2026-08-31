package protocol

import "context"

const (
	// CapabilityCodexExecutorV1 marks a plugin that can execute the native
	// Codex provider wire contract after Core has selected an account.
	CapabilityCodexExecutorV1 = "codex_executor.v1"
	CodexExecutorVersion      = "v1"

	CodexTransportHTTP      = "http"
	CodexTransportSSE       = "sse"
	CodexTransportWebSocket = "websocket"

	CodexEventResponseHeaders  = "response_headers"
	CodexEventData             = "data"
	CodexEventUsage            = "usage"
	CodexEventCredentialUpdate = "credential_update"
	CodexEventAuditRequest     = "audit_request"
	CodexEventAuditResult      = "audit_result"
	CodexEventCompleted        = "completed"
	CodexEventError            = "error"
)

// Codex native endpoint identifiers shared by Core and executor plugins. The
// History/Notes and analytics values are optional control-plane contracts;
// they are kept here even though they do not use the WebSocket RPC.
const (
	CodexEndpointResponses              = "responses"
	CodexEndpointCompact                = "compact"
	CodexEndpointAlphaSearch            = "alpha_search"
	CodexEndpointRealtimeCalls          = "realtime_calls"
	CodexEndpointMemoriesTraceSummarize = "memories_trace_summarize"
	CodexEndpointRealtimeSideband       = "realtime_sideband"
	CodexEndpointGuardian               = "guardian"
	CodexEndpointGuardianClassifier     = "guardian_classifier"
	CodexEndpointHistoryListWindows     = "history_list_windows"
	CodexEndpointHistoryListItems       = "history_list_items"
	CodexEndpointHistoryReadItem        = "history_read_item"
	CodexEndpointHistorySearchContents  = "history_search_contents"
	CodexEndpointNotesListFilesByPrefix = "notes_list_files_by_prefix"
	CodexEndpointNotesReadFile          = "notes_read_file"
	CodexEndpointNotesSearchContents    = "notes_search_contents"
	CodexEndpointNotesAppendToFile      = "notes_append_to_file"
	CodexEndpointNotesWriteFile         = "notes_write_file"
	CodexEndpointNotesThreadHint        = "notes_thread_hint"
	CodexEndpointAnalyticsEvents        = "analytics_events"
	// CodexEndpointTurnCosts is the API-key-only turn-cost reconciliation
	// contract used by the official app-server worker.
	CodexEndpointTurnCosts         = "codex_turn_costs"
	CodexEndpointFilesCreate       = "files_create"
	CodexEndpointFilesFinalize     = "files_finalize"
	CodexEndpointImagesGenerations = "images_generations"
	CodexEndpointImagesEdits       = "images_edits"
	// Backend-client management contracts used by current Codex CLI builds.
	// These are deliberately separate endpoint kinds so they cannot be routed
	// through ordinary Responses/CPA translation or an arbitrary proxy path.
	CodexEndpointUsage                        = "codex_usage"
	CodexEndpointThreadUsage                  = "codex_thread_usage"
	CodexEndpointRateLimitResetCredits        = "codex_rate_limit_reset_credits"
	CodexEndpointRateLimitResetCreditsConsume = "codex_rate_limit_reset_credits_consume"
	CodexEndpointAccountsCheck                = "codex_accounts_check"
	CodexEndpointAccountsNudge                = "codex_accounts_send_add_credits_nudge_email"
	CodexEndpointProfilesMe                   = "codex_profiles_me"
	CodexEndpointConfigBundle                 = "codex_config_bundle"
	CodexEndpointSettingsUser                 = "codex_settings_user"
	CodexEndpointTasks                        = "codex_tasks"
	CodexEndpointTasksList                    = "codex_tasks_list"
	CodexEndpointTaskDetails                  = "codex_task_details"
	CodexEndpointTaskSiblingTurns             = "codex_task_sibling_turns"
	CodexEndpointWorkspaceMessages            = "codex_workspace_messages"
	CodexEndpointPSMCP                        = "codex_ps_mcp"
	// Official cloud-tasks environment discovery contracts. The by-repo
	// endpoint has three required opaque segments and one optional ref segment.
	CodexEndpointEnvironments       = "codex_environments"
	CodexEndpointEnvironmentsByRepo = "codex_environments_by_repo"
	// Remote plugin catalog and mutation contracts. They are carried over the
	// same native executor envelope but are restricted to ChatGPT OAuth
	// accounts by Core and the executor transport.
	CodexEndpointPluginsList                      = "codex_plugins_list"
	CodexEndpointPluginsSearch                    = "codex_plugins_search"
	CodexEndpointPluginsSuggested                 = "codex_plugins_suggested"
	CodexEndpointPluginsInstalled                 = "codex_plugins_installed"
	CodexEndpointPluginsWorkspaceShared           = "codex_plugins_workspace_shared"
	CodexEndpointPluginsWorkspaceCreated          = "codex_plugins_workspace_created"
	CodexEndpointPluginDetail                     = "codex_plugin_detail"
	CodexEndpointPluginSkillDetail                = "codex_plugin_skill_detail"
	CodexEndpointPluginInstall                    = "codex_plugin_install"
	CodexEndpointPluginUninstall                  = "codex_plugin_uninstall"
	CodexEndpointPluginShares                     = "codex_plugin_shares"
	CodexEndpointConnectorsDirectoryList          = "codex_connectors_directory_list"
	CodexEndpointConnectorsDirectoryListWorkspace = "codex_connectors_directory_list_workspace"
	CodexEndpointAppsBatch                        = "codex_apps_batch"
	CodexEndpointPluginsFeatured                  = "codex_plugins_featured"
	CodexEndpointPluginLegacyEnable               = "codex_plugin_legacy_enable"
	CodexEndpointPluginLegacyUninstall            = "codex_plugin_legacy_uninstall"
	CodexEndpointPluginsWorkspaceUploadURL        = "codex_plugins_workspace_upload_url"
	CodexEndpointPluginsWorkspaceCreate           = "codex_plugins_workspace_create"
	CodexEndpointPluginsWorkspaceUpdate           = "codex_plugins_workspace_update"
	CodexEndpointPluginsWorkspaceDetail           = "codex_plugins_workspace_detail"
	CodexEndpointPluginsWorkspaceDelete           = "codex_plugins_workspace_delete"
	// Remote Control is a separate app-server transport. It must never be
	// treated as a Responses/Guardian websocket because its first frame is a
	// JSON-RPC envelope and its upstream authentication uses the enrolled
	// remote-control server token.
	CodexEndpointRemoteControlEnroll       = "codex_remote_control_enroll"
	CodexEndpointRemoteControlRefresh      = "codex_remote_control_refresh"
	CodexEndpointRemoteControlPair         = "codex_remote_control_pair"
	CodexEndpointRemoteControlPairStatus   = "codex_remote_control_pair_status"
	CodexEndpointRemoteControlClientsList  = "codex_remote_control_clients_list"
	CodexEndpointRemoteControlClientRevoke = "codex_remote_control_client_revoke"
	CodexEndpointRemoteControl             = "codex_remote_control"
	// Dedicated websocket endpoint identifier.  The HTTP Remote Control
	// contracts use the names above; keeping the duplex server endpoint
	// distinct prevents it from being mistaken for a generic management call.
	CodexEndpointRemoteControlServerWebSocket = "codex_remote_control_server_websocket"
)

// CodexWebSocketFrameType values are the transport-neutral frame kinds used
// by the bidirectional ExecuteWebSocket RPC. Data frames carry the exact
// upstream/downstream WebSocket payload in Data; control frames retain their
// close metadata so a relay can preserve shutdown semantics.
const (
	CodexWebSocketFrameText   = "text"
	CodexWebSocketFrameBinary = "binary"
	CodexWebSocketFramePing   = "ping"
	CodexWebSocketFramePong   = "pong"
	CodexWebSocketFrameClose  = "close"
	// Metadata frames are emitted by the executor, never sent by the client.
	// They keep credential refresh and audit state out of provider JSON while
	// allowing the data-plane socket to stay byte-for-byte transparent.
	CodexWebSocketFrameHeaders          = "headers"
	CodexWebSocketFrameCredentialUpdate = "credential_update"
	CodexWebSocketFrameAuditRequest     = "audit_request"
	CodexWebSocketFrameAuditResult      = "audit_result"
	CodexWebSocketFrameError            = "error"
)

// CodexCredentialLease is short-lived credential material issued by Core for
// one selected account. Plugins must not persist it or include it in events.
type CodexCredentialLease struct {
	LeaseID      string `json:"lease_id,omitempty"`
	AuthKind     string `json:"auth_kind,omitempty"`
	AccessToken  string `json:"access_token,omitempty"`
	APIKey       string `json:"api_key,omitempty"`
	AccountID    string `json:"account_id,omitempty"`
	HeaderName   string `json:"header_name,omitempty"`
	HeaderPrefix string `json:"header_prefix,omitempty"`
	// HeaderPrefixSet preserves an explicitly configured empty prefix across
	// the Core/plugin JSON boundary. A missing field retains the v1 defaulting
	// behavior for older peers.
	HeaderPrefixSet bool   `json:"header_prefix_set,omitempty"`
	RefreshToken    string `json:"refresh_token,omitempty"`
	TokenURL        string `json:"token_url,omitempty"`
	// SessionToken/SessionURL support accounts imported from the ChatGPT
	// browser session. The plugin uses the account's proxy for this exchange.
	SessionToken            string `json:"session_token,omitempty"`
	SessionURL              string `json:"session_url,omitempty"`
	ChatGPTAccountIsFedramp bool   `json:"chatgpt_account_is_fedramp,omitempty"`
	// ChatGPTAccountIsFedrampSet distinguishes an explicit false claim from an
	// omitted claim when a refreshed credential is merged back into Core.
	ChatGPTAccountIsFedrampSet bool   `json:"chatgpt_account_is_fedramp_set,omitempty"`
	ClientID                   string `json:"client_id,omitempty"`
	Expired                    string `json:"expired,omitempty"`
	ExpiresAt                  string `json:"expires_at,omitempty"`
}

// CodexExecuteRequest describes one native provider exchange. Body is encoded
// as the binary part of the plugin envelope instead of JSON/base64.
type CodexExecuteRequest struct {
	Version    string               `json:"version"`
	RequestID  string               `json:"request_id,omitempty"`
	Client     string               `json:"client,omitempty"`
	GroupID    int                  `json:"group_id,omitempty"`
	Model      string               `json:"model,omitempty"`
	Endpoint   string               `json:"endpoint,omitempty"`
	Stream     bool                 `json:"stream,omitempty"`
	Method     string               `json:"method"`
	BaseURL    string               `json:"base_url"`
	Path       string               `json:"path"`
	Query      map[string][]string  `json:"query,omitempty"`
	Header     map[string][]string  `json:"header,omitempty"`
	Body       []byte               `json:"body,omitempty"`
	Transport  string               `json:"transport,omitempty"`
	ProxyURL   string               `json:"proxy_url,omitempty"`
	Audit      bool                 `json:"audit,omitempty"`
	Credential CodexCredentialLease `json:"credential,omitempty"`
	// Remote Control metadata is explicit rather than smuggled through the
	// caller header map. RemoteControlToken is only accepted for pair,
	// pair/status and the remote-control websocket; enroll/refresh/client
	// management continue to use the selected OAuth lease.
	RemoteControlToken           string `json:"remote_control_token,omitempty"`
	RemoteControlServerID        string `json:"remote_control_server_id,omitempty"`
	RemoteControlName            string `json:"remote_control_name,omitempty"`
	RemoteControlProtocolVersion string `json:"remote_control_protocol_version,omitempty"`
	InstallationID               string `json:"installation_id,omitempty"`
	// Optional app-server websocket metadata. These fields are intentionally
	// outside Header so the executor can apply the official values after
	// dropping caller-supplied look-alikes.
	RemoteControlHostDeviceKind  string `json:"remote_control_host_device_kind,omitempty"`
	RemoteControlSubscribeCursor string `json:"remote_control_subscribe_cursor,omitempty"`
}

type CodexUsage struct {
	InputTokens           int64 `json:"input_tokens,omitempty"`
	CachedInputTokens     int64 `json:"cached_input_tokens,omitempty"`
	OutputTokens          int64 `json:"output_tokens,omitempty"`
	ReasoningOutputTokens int64 `json:"reasoning_output_tokens,omitempty"`
	TotalTokens           int64 `json:"total_tokens,omitempty"`
}

type CodexExecutorError struct {
	Code              string `json:"code"`
	Message           string `json:"message"`
	Phase             string `json:"phase,omitempty"`
	UpstreamStatus    int    `json:"upstream_status,omitempty"`
	Retryable         bool   `json:"retryable,omitempty"`
	RetryAfter        string `json:"retry_after,omitempty"`
	DownstreamStarted bool   `json:"downstream_started,omitempty"`
	UpstreamRequestID string `json:"upstream_request_id,omitempty"`
}

type CodexCredentialUpdate struct {
	LeaseID                    string `json:"lease_id"`
	AccessToken                string `json:"access_token"`
	RefreshToken               string `json:"refresh_token,omitempty"`
	SessionToken               string `json:"session_token,omitempty"`
	IDToken                    string `json:"id_token,omitempty"`
	AccountID                  string `json:"account_id,omitempty"`
	Email                      string `json:"email,omitempty"`
	PlanType                   string `json:"plan_type,omitempty"`
	SubscriptionActiveUntil    string `json:"subscription_active_until,omitempty"`
	ChatGPTAccountIsFedramp    bool   `json:"chatgpt_account_is_fedramp,omitempty"`
	ChatGPTAccountIsFedrampSet bool   `json:"chatgpt_account_is_fedramp_set,omitempty"`
	ExpiresAt                  int64  `json:"expires_at,omitempty"`
}

// CodexAuditEvent describes one native provider HTTP attempt without carrying
// credential headers. Request bodies use CodexExecuteEvent.Data so the wire
// codec keeps them binary instead of base64-expanding them. OAuth token
// exchanges are deliberately excluded because their JSON body contains the refresh
// token; credential_update is the only observable refresh signal.
type CodexAuditEvent struct {
	AttemptID       string              `json:"attempt_id"`
	Method          string              `json:"method,omitempty"`
	URL             string              `json:"url,omitempty"`
	Header          map[string][]string `json:"header,omitempty"`
	StatusCode      int                 `json:"status_code,omitempty"`
	RetryAfter      string              `json:"retry_after,omitempty"`
	LatencyMs       int64               `json:"latency_ms,omitempty"`
	FirstTokenMs    int64               `json:"first_token_ms,omitempty"`
	ResponseStarted bool                `json:"response_started,omitempty"`
	StreamCompleted bool                `json:"stream_completed,omitempty"`
	NetworkError    bool                `json:"network_error,omitempty"`
	ErrorCode       string              `json:"error_code,omitempty"`
}

// CodexExecuteEvent is emitted in wire order: optional credential_update,
// one response_headers event, zero or more data/usage events and one terminal.
type CodexExecuteEvent struct {
	Version          string                 `json:"version"`
	Type             string                 `json:"type"`
	StatusCode       int                    `json:"status_code,omitempty"`
	Header           map[string][]string    `json:"header,omitempty"`
	Data             []byte                 `json:"data,omitempty"`
	Usage            *CodexUsage            `json:"usage,omitempty"`
	CredentialUpdate *CodexCredentialUpdate `json:"credential_update,omitempty"`
	Audit            *CodexAuditEvent       `json:"audit,omitempty"`
	Error            *CodexExecutorError    `json:"error,omitempty"`
}

// CodexWebSocketFrame is one message exchanged over a native Responses
// WebSocket. The frame type is deliberately independent of gorilla/tungstenite
// so the Core↔plugin wire remains stable and old plugins can ignore the new
// optional RPC entirely.
type CodexWebSocketFrame struct {
	Version          string                 `json:"version,omitempty"`
	Type             string                 `json:"type"`
	Data             []byte                 `json:"data,omitempty"`
	Code             int                    `json:"code,omitempty"`
	Reason           string                 `json:"reason,omitempty"`
	StatusCode       int                    `json:"status_code,omitempty"`
	Header           map[string][]string    `json:"header,omitempty"`
	CredentialUpdate *CodexCredentialUpdate `json:"credential_update,omitempty"`
	Audit            *CodexAuditEvent       `json:"audit,omitempty"`
	Error            *CodexExecutorError    `json:"error,omitempty"`
}

// CodexExecutor is an optional streaming capability. Keeping it separate from
// Plugin preserves compatibility with existing v2 plugins.
type CodexExecutor interface {
	ExecuteStream(context.Context, CodexExecuteRequest, func(CodexExecuteEvent) error) error
}

// CodexWebSocketExecutor is optional. It is a true duplex session: input
// frames may contain multiple response.create events and output frames may
// interleave across stream_id lanes exactly as the provider emits them.
type CodexWebSocketExecutor interface {
	ExecuteWebSocket(context.Context, CodexExecuteRequest, <-chan CodexWebSocketFrame, func(CodexWebSocketFrame) error) error
}
