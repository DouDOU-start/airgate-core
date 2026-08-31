package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/pluginruntime"
	"github.com/DouDOU-start/airgate-core/internal/pluginruntime/protocol"
	"github.com/DouDOU-start/airgate-core/internal/relay/dto"
	"github.com/DouDOU-start/airgate-core/internal/requestaudit"
)

var (
	ErrCodexPluginUnavailable = errors.New("codex executor plugin unavailable")
	ErrCodexPluginUnsupported = errors.New("codex executor plugin unsupported")
	ErrCodexCPAUnsupported    = errors.New("codex endpoint is unsupported by CPA in cpa_only mode")
	// ErrCodexRemoteControlPathMissing is returned before invoking a plugin
	// when a dynamic Remote Control endpoint was assembled without its required
	// environment/client path segments. Sending a guessed collection path can
	// target the wrong tenant, so this is intentionally a fail-closed build
	// error rather than a network request.
	ErrCodexRemoteControlPathMissing = errors.New("codex remote-control dynamic path is missing or invalid")
	errCodexBufferedResponseTooLarge = errors.New("native Codex buffered response exceeds 33554432 byte limit")
)

const maxCodexBufferedResponseBytes = 32 << 20

// CodexTransportMode controls how Codex requests are dispatched when both the
// native executor and the CPA translation bridge are configured. The value is
// supplied by the installed executor plugin configuration, never by account
// credentials. Empty and auto are intentionally equivalent.
type CodexTransportMode string

const (
	CodexModeAuto         CodexTransportMode = "auto"
	CodexModeNative       CodexTransportMode = "native"
	CodexModeNativeOnly   CodexTransportMode = "native_only"
	CodexModeCPATranslate CodexTransportMode = "cpa_translate"
	CodexModeCPAOnly      CodexTransportMode = "cpa_only"
)

// IsCodexPlatform reports whether an account uses the canonical Codex
// platform. The native executor is intentionally scoped to this single
// account platform; OpenAI and compatibility accounts retain their existing
// CPA/channel behavior.
func IsCodexPlatform(platform string) bool {
	return strings.EqualFold(strings.TrimSpace(platform), "codex")
}

// IsCodexOAuthAuthType reports whether authKind is the canonical ChatGPT
// OAuth credential family accepted by the native Codex executor. Account
// records are normalized before they reach the registry, so aliases are not
// accepted here; keeping this predicate strict makes malformed/directly
// constructed snapshots fail closed instead of being treated as OAuth.
func IsCodexOAuthAuthType(authKind string) bool {
	return strings.EqualFold(strings.TrimSpace(authKind), "oauth")
}

// IsCodexAPIKeyAuthType reports whether authKind is the canonical API-key
// credential family accepted by the native Codex executor. The account
// service normalizes account types before persistence; accepting aliases here
// would re-introduce a second, inconsistent compatibility boundary at runtime.
func IsCodexAPIKeyAuthType(authKind string) bool {
	return strings.EqualFold(strings.TrimSpace(authKind), "api_key")
}

// IsCodexNativeAuthType is the shared account-eligibility predicate used by
// Core's native route picker and the plugin transport. Keeping platform and
// credential-family checks together prevents the picker from selecting an
// unknown/empty auth type that the transport would reject only after a slot or
// upstream attempt had been acquired.
func IsCodexNativeAuthType(authKind string) bool {
	return IsCodexOAuthAuthType(authKind) || IsCodexAPIKeyAuthType(authKind)
}

// NormalizeCodexTransportMode accepts a plugin-level configuration value; it
// never inspects per-account credentials and fails safe to auto when unknown.
func NormalizeCodexTransportMode(raw string) CodexTransportMode {
	switch CodexTransportMode(strings.ToLower(strings.TrimSpace(raw))) {
	case CodexModeNative:
		return CodexModeNative
	case CodexModeNativeOnly:
		return CodexModeNativeOnly
	case CodexModeCPATranslate:
		return CodexModeCPATranslate
	case CodexModeCPAOnly:
		return CodexModeCPAOnly
	default:
		return CodexModeAuto
	}
}

// codexCPAEligibleEndpoint is intentionally kept local to the transport
// package so native eligibility does not depend on the pipeline package. Keep
// this list in sync with pipeline.providerCPAEligibleEndpoint.
func codexCPAEligibleEndpoint(endpoint string) bool {
	switch strings.ToLower(strings.TrimSpace(endpoint)) {
	case "chat_completions", "responses", "messages", "messages_count_tokens",
		"generate_content", "count_tokens", "images_generations", "images_edits":
		return true
	default:
		return false
	}
}

// CodexCPATranslationContract reports whether an endpoint has a stable CPA
// translation contract. Keep this classification in the transport package so
// native fallback and pipeline dispatch cannot drift apart.
func CodexCPATranslationContract(endpoint string) bool {
	return codexCPAEligibleEndpoint(endpoint)
}

// codexManagementEndpoint identifies the official backend-client control
// plane. It is kept separate from the CPA contract list because these calls
// are raw/native and must never be translated through a text adaptor.
func codexManagementEndpoint(endpoint string) bool {
	switch strings.ToLower(strings.TrimSpace(endpoint)) {
	case "codex_usage", "codex_thread_usage", "codex_rate_limit_reset_credits",
		"codex_rate_limit_reset_credits_consume", "codex_accounts_check",
		"codex_accounts_send_add_credits_nudge_email", "codex_profiles_me",
		"codex_config_bundle", "codex_settings_user", "codex_tasks",
		"codex_tasks_list", "codex_task_details", "codex_task_sibling_turns",
		"codex_environments", "codex_environments_by_repo",
		"codex_workspace_messages", "codex_ps_mcp",
		"codex_plugins_list", "codex_plugins_search", "codex_plugins_suggested",
		"codex_plugins_installed", "codex_plugins_workspace_shared",
		"codex_plugins_workspace_created", "codex_plugin_detail",
		"codex_plugin_skill_detail", "codex_plugin_install",
		"codex_plugin_uninstall", "codex_plugin_shares",
		"codex_connectors_directory_list", "codex_connectors_directory_list_workspace",
		"codex_apps_batch", "codex_plugins_featured",
		"codex_plugin_legacy_enable", "codex_plugin_legacy_uninstall",
		"codex_plugins_workspace_upload_url", "codex_plugins_workspace_create",
		"codex_plugins_workspace_update", "codex_plugins_workspace_detail",
		"codex_plugins_workspace_delete",
		protocol.CodexEndpointRemoteControlEnroll, protocol.CodexEndpointRemoteControlRefresh,
		protocol.CodexEndpointRemoteControlPair, protocol.CodexEndpointRemoteControlPairStatus,
		protocol.CodexEndpointRemoteControlClientsList, protocol.CodexEndpointRemoteControlClientRevoke,
		protocol.CodexEndpointRemoteControl, protocol.CodexEndpointRemoteControlServerWebSocket:
		return true
	default:
		return false
	}
}

// CodexOAuthOnlyEndpoint reports native control-plane contracts implemented by
// ChatGPT's OAuth backend. API-key Codex accounts must never be selected for
// these paths: the upstream plugin service rejects API-key auth, and sending
// an API key to a /backend-api/ps endpoint can also leak the wrong credential
// family through a reverse proxy.
func CodexOAuthOnlyEndpoint(endpoint string) bool {
	if codexHistoryNotesEndpoint(endpoint) {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(endpoint)) {
	case "codex_plugins_list", "codex_plugins_search", "codex_plugins_suggested",
		"codex_plugins_installed", "codex_plugins_workspace_shared",
		"codex_plugins_workspace_created", "codex_plugin_detail",
		"codex_plugin_skill_detail", "codex_plugin_install",
		"codex_plugin_uninstall", "codex_plugin_shares",
		"codex_connectors_directory_list", "codex_connectors_directory_list_workspace",
		"codex_apps_batch",
		"codex_plugin_legacy_enable", "codex_plugin_legacy_uninstall",
		"codex_plugins_workspace_upload_url", "codex_plugins_workspace_create",
		"codex_plugins_workspace_update", "codex_plugins_workspace_detail",
		"codex_plugins_workspace_delete",
		protocol.CodexEndpointRemoteControlEnroll, protocol.CodexEndpointRemoteControlRefresh,
		protocol.CodexEndpointRemoteControlPair, protocol.CodexEndpointRemoteControlPairStatus,
		protocol.CodexEndpointRemoteControlClientsList, protocol.CodexEndpointRemoteControlClientRevoke,
		protocol.CodexEndpointRemoteControl, protocol.CodexEndpointRemoteControlServerWebSocket:
		return true
	default:
		return false
	}
}

func codexHistoryNotesEndpoint(endpoint string) bool {
	// The history/notes extension is enabled by the official CLI only when
	// the selected provider is OpenAI and the active auth uses the Codex
	// backend (that is, ChatGPT OAuth/session auth). API-key auth is a direct
	// OpenAI API credential and must not be sent to these private routes.
	switch strings.ToLower(strings.TrimSpace(endpoint)) {
	case protocol.CodexEndpointHistoryListWindows, protocol.CodexEndpointHistoryListItems,
		protocol.CodexEndpointHistoryReadItem, protocol.CodexEndpointHistorySearchContents,
		protocol.CodexEndpointNotesListFilesByPrefix, protocol.CodexEndpointNotesReadFile,
		protocol.CodexEndpointNotesSearchContents, protocol.CodexEndpointNotesAppendToFile,
		protocol.CodexEndpointNotesWriteFile, protocol.CodexEndpointNotesThreadHint:
		return true
	default:
		return false
	}
}

const (
	defaultCodexOAuthTokenURL = "https://auth.openai.com/oauth/token"
	defaultCodexOAuthClientID = "app_EMoamEEZ73f0CkXaXp7hrann"
	defaultCodexSessionURL    = "https://chatgpt.com/api/auth/session"
)

type CodexExecutorManager interface {
	ExecuteCodex(context.Context, protocol.CodexExecuteRequest, func(protocol.CodexExecuteEvent) error) error
}

// CodexWebSocketManager is the duplex companion to CodexExecutorManager. The
// input channel stays open for the lifetime of one upstream Responses socket;
// output frames may be interleaved across stream_id lanes.
type CodexWebSocketManager interface {
	ExecuteCodexWebSocket(context.Context, protocol.CodexExecuteRequest, <-chan protocol.CodexWebSocketFrame, func(protocol.CodexWebSocketFrame) error) error
}

// CodexWebSocketTransport is implemented by native Codex transports that can
// relay a real WebSocket. The returned map contains any refreshed account
// credentials observed before/while the socket was established.
type CodexWebSocketTransport interface {
	ExecuteWebSocket(context.Context, Request, <-chan protocol.CodexWebSocketFrame, func(protocol.CodexWebSocketFrame) error) (map[string]string, error)
}

// CodexPluginTransport executes native Codex requests through a running
// codex_executor.v1 plugin managed by Core.
type CodexPluginTransport struct{ manager CodexExecutorManager }

var _ AccountEligibility = (*CodexPluginTransport)(nil)

func NewCodexPluginTransport(manager CodexExecutorManager) *CodexPluginTransport {
	if manager == nil {
		return nil
	}
	return &CodexPluginTransport{manager: manager}
}

// CodexTransportMode exposes the effective plugin-wide policy to the relay
// pipeline. Managers that do not implement the optional policy interface
// default to auto; no account credential fallback is attempted.
func (t *CodexPluginTransport) CodexTransportMode() string {
	if t == nil || t.manager == nil {
		return string(CodexModeAuto)
	}
	policy, ok := t.manager.(CodexTransportPolicy)
	if !ok {
		return string(CodexModeAuto)
	}
	return string(NormalizeCodexTransportMode(policy.CodexTransportMode()))
}

func (t *CodexPluginTransport) Capabilities() Capabilities {
	caps := Capabilities{HTTP: true, Streaming: true}
	if t == nil || t.manager == nil {
		return caps
	}
	// The production runtime exposes the concrete set of running plugin
	// capabilities, which is more precise than merely checking whether the
	// manager type has the optional RPC method (old plugins can lack the
	// WebSocket implementation). Test/third-party managers that do not expose
	// the probe retain the source-compatible interface-based fallback.
	if probe, ok := t.manager.(interface{ SupportsCodexWebSocket() bool }); ok {
		caps.WebSocket = probe.SupportsCodexWebSocket()
		return caps
	}
	_, caps.WebSocket = t.manager.(CodexWebSocketManager)
	return caps
}

// SupportsAccount keeps the native Codex executor scoped to canonical Codex
// accounts. CPA continues to own all other provider account families.
func (t *CodexPluginTransport) SupportsAccount(account Account) bool {
	return IsCodexPlatform(account.Platform) && IsCodexNativeAuthType(account.Type)
}

func (t *CodexPluginTransport) Execute(ctx context.Context, req Request) Result {
	if t == nil || t.manager == nil {
		return Result{BuildErr: ErrCodexPluginUnavailable}
	}
	if !nativeEligible(req, NormalizeCodexTransportMode(t.CodexTransportMode())) {
		return Result{BuildErr: ErrCodexPluginUnsupported}
	}
	if !codexRemoteControlPathPresent(req.Endpoint, req.Path) {
		return Result{BuildErr: ErrCodexRemoteControlPathMissing}
	}
	headerName, headerPrefix, headerPrefixSet := credentialHeader(req.Account.Credentials)
	accountID := credentialChatGPTAccountID(req.Account)
	tokenURL, clientID := credentialOAuthEndpoint(req.Account)
	sessionURL := credentialSessionEndpoint(req.Account)
	query := cloneValues(req.Query)
	if strings.EqualFold(strings.TrimSpace(req.Endpoint), protocol.CodexEndpointTurnCosts) {
		query = nil
	}
	body := req.Payload
	if req.RawBody != nil {
		body = req.RawBody
	}
	wire := protocol.CodexExecuteRequest{
		Version: protocol.CodexExecutorVersion, RequestID: req.RequestID, Client: req.Client, GroupID: req.GroupID,
		Model: req.Model, Endpoint: req.Endpoint, Stream: req.Stream, Method: req.Method,
		BaseURL: req.BaseURL, Path: req.Path, Query: query, Header: codexProviderHeadersForEndpoint(req.Endpoint, req.Headers),
		Body: append([]byte(nil), body...), Transport: req.Transport, ProxyURL: req.Account.ProxyURL, Audit: req.UpstreamAudit != nil,
		Credential: protocol.CodexCredentialLease{LeaseID: req.RequestID + ":" + strconv.Itoa(req.Account.ID), AccountID: accountID, AuthKind: req.Account.Type,
			AccessToken: credentialAccessToken(req.Account.Type, req.Account.Credentials), RefreshToken: strings.TrimSpace(req.Account.Credentials["refresh_token"]),
			APIKey: strings.TrimSpace(req.Account.Credentials["api_key"]), Expired: strings.TrimSpace(req.Account.Credentials["expired"]),
			ExpiresAt: strings.TrimSpace(req.Account.Credentials["expires_at"]),
			TokenURL:  tokenURL, SessionToken: strings.TrimSpace(req.Account.Credentials["session_token"]), SessionURL: sessionURL,
			ChatGPTAccountIsFedramp: codexFedrampCredential(req.Account.Credentials), ChatGPTAccountIsFedrampSet: codexFedrampCredentialPresent(req.Account.Credentials), ClientID: clientID,
			HeaderName: headerName, HeaderPrefix: headerPrefix, HeaderPrefixSet: headerPrefixSet},
		RemoteControlToken: req.RemoteControlToken, RemoteControlServerID: req.RemoteControlServerID,
		RemoteControlName: req.RemoteControlName, RemoteControlProtocolVersion: req.RemoteControlProtocolVersion,
		InstallationID: req.InstallationID, RemoteControlHostDeviceKind: req.RemoteControlHostDeviceKind,
		RemoteControlSubscribeCursor: req.RemoteControlSubscribeCursor,
	}
	var c *gin.Context
	if req.LegacyContext != nil {
		c, _ = req.LegacyContext.(*gin.Context)
	}
	var result Result
	var started bool
	attemptStarted := time.Now()
	// A third-party Codex manager may deliver events concurrently. Serialize
	// the callback so result state, audit bookkeeping, image usage observation,
	// and the downstream streaming writer remain race-free and wire-ordered.
	var eventMu sync.Mutex
	auditAttempts := make(map[string]UpstreamAuditAttempt)
	var imageUsageObserver *codexImageUsageObserver
	if isCodexImageEndpoint(req.Endpoint) {
		imageUsageObserver = newCodexImageUsageObserver()
	}
	err := t.manager.ExecuteCodex(ctx, wire, func(event protocol.CodexExecuteEvent) error {
		eventMu.Lock()
		defer eventMu.Unlock()
		switch event.Type {
		case protocol.CodexEventAuditRequest:
			if req.UpstreamAudit == nil {
				return nil
			}
			if event.Audit == nil || strings.TrimSpace(event.Audit.AttemptID) == "" {
				return fmt.Errorf("%w: native audit request metadata missing", requestaudit.ErrWrite)
			}
			method := strings.TrimSpace(event.Audit.Method)
			if method == "" {
				method = http.MethodPost
			}
			auditReq, err := http.NewRequestWithContext(ctx, method, event.Audit.URL, bytes.NewReader(event.Data))
			if err != nil {
				return fmt.Errorf("%w: native audit request invalid: %v", requestaudit.ErrWrite, err)
			}
			auditReq.Header = safeAuditHeader(event.Audit.Header)
			attempt, err := req.UpstreamAudit.BeginUpstreamAttempt(ctx, UpstreamAuditRequest{
				Method: auditReq.Method, URL: auditReq.URL.String(), Headers: auditReq.Header, Body: event.Data,
			})
			if err != nil {
				return fmt.Errorf("%w: native upstream audit begin failed: %v", requestaudit.ErrWrite, err)
			}
			if attempt == nil {
				return fmt.Errorf("%w: native upstream audit attempt unavailable", requestaudit.ErrWrite)
			}
			if previous := auditAttempts[event.Audit.AttemptID]; previous != nil {
				attempt.FinishUpstreamAttempt(UpstreamAuditResult{NetworkError: true, ErrorCode: "duplicate_audit_attempt"})
				return fmt.Errorf("%w: duplicate native audit attempt", requestaudit.ErrWrite)
			}
			auditAttempts[event.Audit.AttemptID] = attempt
		case protocol.CodexEventAuditResult:
			if event.Audit != nil && (event.Audit.ResponseStarted || event.Audit.StatusCode != 0 || event.Audit.StreamCompleted) {
				result.ResponseStarted = true
			}
			if event.Audit == nil {
				return nil
			}
			if attempt := auditAttempts[strings.TrimSpace(event.Audit.AttemptID)]; attempt != nil {
				attempt.FinishUpstreamAttempt(UpstreamAuditResult{
					StatusCode: event.Audit.StatusCode, RetryAfter: event.Audit.RetryAfter,
					LatencyMs: event.Audit.LatencyMs, FirstTokenMs: event.Audit.FirstTokenMs,
					ResponseStarted: event.Audit.ResponseStarted, StreamCompleted: event.Audit.StreamCompleted,
					NetworkError: event.Audit.NetworkError, ErrorCode: event.Audit.ErrorCode,
				})
				delete(auditAttempts, strings.TrimSpace(event.Audit.AttemptID))
			}
		case protocol.CodexEventResponseHeaders:
			result.ResponseStarted = true
			result.StatusCode = event.StatusCode
			result.Headers = safeResponseHeader(event.Header)
			result.ContentType = result.Headers.Get("Content-Type")
		case protocol.CodexEventData:
			result.ResponseStarted = true
			result.DataReceived = true
			streamToClient := req.Stream && c != nil && (result.StatusCode == 0 || result.StatusCode >= 200 && result.StatusCode < 300)
			if !streamToClient && len(event.Data) > maxCodexBufferedResponseBytes-len(result.Body) {
				// Keep the same cumulative 32 MiB boundary as the native
				// executor, but enforce it again at Core's trust boundary for old
				// or misbehaving plugins. Never return a truncated JSON/error body.
				result.Body = nil
				result.NetErr = errCodexBufferedResponseTooLarge
				return errCodexBufferedResponseTooLarge
			}
			if imageUsageObserver != nil {
				imageUsageObserver.Feed(event.Data)
			}
			if streamToClient {
				if !started {
					for key, values := range result.Headers {
						for _, value := range values {
							c.Header(key, value)
						}
					}
					if result.StatusCode == 0 {
						result.StatusCode = http.StatusOK
					}
					c.Status(result.StatusCode)
					started = true
				}
				if _, err := c.Writer.Write(event.Data); err != nil {
					return err
				}
				if f, ok := c.Writer.(http.Flusher); ok {
					f.Flush()
				}
				result.Written = true
				started = true
				markCodexFirstContent(&result, req, event.Data, attemptStarted)
			} else {
				result.Body = append(result.Body, event.Data...)
			}
		case protocol.CodexEventUsage:
			result.Usage = usageFromEvent(event.Usage)
		case protocol.CodexEventCredentialUpdate:
			if event.CredentialUpdate != nil {
				if (event.CredentialUpdate.LeaseID != "" && event.CredentialUpdate.LeaseID != wire.Credential.LeaseID) || strings.TrimSpace(event.CredentialUpdate.AccessToken) == "" {
					return errors.New("invalid Codex credential update")
				}
				if result.RefreshedCredentials == nil {
					result.RefreshedCredentials = cloneCodexRefreshCredentials(req.Account.Credentials)
				}
				result.RefreshedCredentials["access_token"] = event.CredentialUpdate.AccessToken
				if event.CredentialUpdate.RefreshToken != "" {
					result.RefreshedCredentials["refresh_token"] = event.CredentialUpdate.RefreshToken
				}
				if event.CredentialUpdate.SessionToken != "" {
					result.RefreshedCredentials["session_token"] = event.CredentialUpdate.SessionToken
				}
				if event.CredentialUpdate.IDToken != "" {
					result.RefreshedCredentials["id_token"] = event.CredentialUpdate.IDToken
				}
				if event.CredentialUpdate.AccountID != "" {
					result.RefreshedCredentials["chatgpt_account_id"] = event.CredentialUpdate.AccountID
				}
				if event.CredentialUpdate.Email != "" {
					result.RefreshedCredentials["email"] = event.CredentialUpdate.Email
				}
				if event.CredentialUpdate.PlanType != "" {
					result.RefreshedCredentials["plan_type"] = event.CredentialUpdate.PlanType
				}
				if event.CredentialUpdate.SubscriptionActiveUntil != "" {
					result.RefreshedCredentials["subscription_active_until"] = event.CredentialUpdate.SubscriptionActiveUntil
				}
				if event.CredentialUpdate.ChatGPTAccountIsFedrampSet {
					result.RefreshedCredentials["chatgpt_account_is_fedramp"] = strconv.FormatBool(event.CredentialUpdate.ChatGPTAccountIsFedramp)

				} else if event.CredentialUpdate.ChatGPTAccountIsFedramp {
					// Backwards-compatible handling for older plugins that only
					// emitted the true value and had no presence bit.
					result.RefreshedCredentials["chatgpt_account_is_fedramp"] = "true"
				}
				if event.CredentialUpdate.ExpiresAt > 0 {
					result.RefreshedCredentials["expires_at"] = strconv.FormatInt(event.CredentialUpdate.ExpiresAt, 10)
					result.RefreshedCredentials["expired"] = time.Unix(event.CredentialUpdate.ExpiresAt, 0).UTC().Format(time.RFC3339)
				}
			}
		case protocol.CodexEventCompleted:
			result.Done = true
		case protocol.CodexEventError:
			if event.Error != nil {
				if event.Error.DownstreamStarted || event.Error.UpstreamStatus != 0 {
					result.ResponseStarted = true
				}
				pluginErr := &CodexPluginError{Info: *event.Error}
				switch {
				case started || result.Written:
					result.StreamErr = pluginErr
				case event.Error.UpstreamStatus >= 400:
					result.StatusCode = event.Error.UpstreamStatus
					if len(result.Body) == 0 {
						result.Body, _ = json.Marshal(map[string]any{"error": map[string]any{"code": event.Error.Code, "message": event.Error.Message}})
					}
					if event.Error.RetryAfter != "" {
						if result.Headers == nil {
							result.Headers = make(http.Header)
						}
						result.Headers.Set("Retry-After", event.Error.RetryAfter)
					}
				case event.Error.Code == "unsupported_capability":
					result.BuildErr = ErrCodexPluginUnsupported
				default:
					result.NetErr = pluginErr
				}
			} else {
				result.BuildErr = ErrCodexPluginUnsupported
			}
		}
		return nil
	})
	// Wait for any callback that may have been in flight before finalizing
	// observer state and audit attempts. The normal gRPC implementation invokes
	// callbacks synchronously, but this also keeps in-process managers from
	// racing the post-processing below.
	eventMu.Lock()
	if imageUsageObserver != nil {
		imageUsageObserver.Finish()
		observed := imageUsageObserver.Usage()
		if !req.Stream && len(result.Body) > 0 {
			observed = mergeCodexImageUsage(observed, parseCodexImageResponseUsage(result.Body))
		}
		if observed != nil {
			result.Usage = mergeCodexImageUsage(result.Usage, observed)
		}
	}
	providerResponseStarted := result.ResponseStarted || result.StatusCode != 0 || len(result.Headers) > 0 || result.DataReceived
	if err != nil && result.BuildErr == nil && result.StreamErr == nil && result.NetErr == nil {
		if result.Written {
			result.StreamErr = err
		} else if providerResponseStarted {
			result.NetErr = err
		} else {
			result.BuildErr = err
		}
	}
	if errors.Is(err, pluginruntime.ErrCodexExecutorUnavailable) {
		if result.Written {
			result.StreamErr = ErrCodexPluginUnavailable
		} else if providerResponseStarted {
			result.NetErr = ErrCodexPluginUnavailable
		} else {
			result.BuildErr = ErrCodexPluginUnavailable
		}
	}
	for _, attempt := range auditAttempts {
		attempt.FinishUpstreamAttempt(UpstreamAuditResult{NetworkError: true, ErrorCode: "native_executor_terminated"})
	}
	eventMu.Unlock()
	rewriteCodexRetryableStatus(&result)
	return result
}

// ExecuteWebSocket relays one persistent Responses WebSocket through the
// native Codex executor. Unlike Execute, it never buffers or interprets
// provider data: text/binary/control frames are delivered to emit unchanged.
// Executor metadata frames (credential refresh, audit, handshake headers and
// structured errors) are consumed at the Core boundary.
func (t *CodexPluginTransport) ExecuteWebSocket(ctx context.Context, req Request, frames <-chan protocol.CodexWebSocketFrame, emit func(protocol.CodexWebSocketFrame) error) (map[string]string, error) {
	if t == nil || t.manager == nil {
		return nil, ErrCodexPluginUnavailable
	}
	wsManager, ok := t.manager.(CodexWebSocketManager)
	if !ok {
		return nil, ErrCodexPluginUnsupported
	}
	endpoint := strings.ToLower(strings.TrimSpace(req.Endpoint))
	if !nativeEligible(req, NormalizeCodexTransportMode(t.CodexTransportMode())) ||
		(endpoint != "responses" && endpoint != "realtime_sideband" && endpoint != "guardian" && endpoint != "guardian_classifier" && endpoint != protocol.CodexEndpointRemoteControl && endpoint != protocol.CodexEndpointRemoteControlServerWebSocket) {
		return nil, ErrCodexPluginUnsupported
	}
	if !codexRemoteControlPathPresent(req.Endpoint, req.Path) {
		return nil, ErrCodexRemoteControlPathMissing
	}
	if isCodexRemoteControlWebSocketRequest(req.Endpoint, req.Path) {
		if err := validateCodexRemoteControlWebSocketOptionalHeaders(req.RemoteControlHostDeviceKind, req.RemoteControlSubscribeCursor); err != nil {
			return nil, err
		}
	}
	headerName, headerPrefix, headerPrefixSet := credentialHeader(req.Account.Credentials)
	tokenURL, clientID := credentialOAuthEndpoint(req.Account)
	sessionURL := credentialSessionEndpoint(req.Account)
	wire := protocol.CodexExecuteRequest{
		Version: protocol.CodexExecutorVersion, RequestID: req.RequestID, Client: req.Client, GroupID: req.GroupID,
		Model: req.Model, Endpoint: req.Endpoint, Stream: true, Method: http.MethodGet,
		BaseURL: req.BaseURL, Path: req.Path, Query: cloneValues(req.Query), Header: codexProviderHeadersForEndpoint(req.Endpoint, req.Headers),
		Transport: protocol.CodexTransportWebSocket, ProxyURL: req.Account.ProxyURL, Audit: req.UpstreamAudit != nil,
		Credential: protocol.CodexCredentialLease{LeaseID: req.RequestID + ":" + strconv.Itoa(req.Account.ID), AccountID: credentialChatGPTAccountID(req.Account), AuthKind: req.Account.Type,
			AccessToken: credentialAccessToken(req.Account.Type, req.Account.Credentials), RefreshToken: strings.TrimSpace(req.Account.Credentials["refresh_token"]),
			APIKey: strings.TrimSpace(req.Account.Credentials["api_key"]), Expired: strings.TrimSpace(req.Account.Credentials["expired"]),
			ExpiresAt: strings.TrimSpace(req.Account.Credentials["expires_at"]), TokenURL: tokenURL,
			SessionToken: strings.TrimSpace(req.Account.Credentials["session_token"]), SessionURL: sessionURL,
			ChatGPTAccountIsFedramp: codexFedrampCredential(req.Account.Credentials), ChatGPTAccountIsFedrampSet: codexFedrampCredentialPresent(req.Account.Credentials), ClientID: clientID,
			HeaderName: headerName, HeaderPrefix: headerPrefix, HeaderPrefixSet: headerPrefixSet},
		RemoteControlToken: req.RemoteControlToken, RemoteControlServerID: req.RemoteControlServerID,
		RemoteControlName: req.RemoteControlName, RemoteControlProtocolVersion: req.RemoteControlProtocolVersion,
		InstallationID: req.InstallationID, RemoteControlHostDeviceKind: req.RemoteControlHostDeviceKind,
		RemoteControlSubscribeCursor: req.RemoteControlSubscribeCursor,
	}
	refreshed := map[string]string(nil)
	// ExecuteCodexWebSocket may be backed by an in-process duplex executor that
	// emits frames from concurrent read pumps. Serialize the callback so audit
	// maps, credential refresh state, and the downstream Gorilla writer remain
	// race-free and preserve provider frame order.
	var eventMu sync.Mutex
	auditAttempts := make(map[string]UpstreamAuditAttempt)
	handleAuditRequest := func(frame protocol.CodexWebSocketFrame) error {
		if req.UpstreamAudit == nil || frame.Audit == nil || strings.TrimSpace(frame.Audit.AttemptID) == "" {
			return nil
		}
		method := strings.TrimSpace(frame.Audit.Method)
		if method == "" {
			method = http.MethodGet
		}
		auditReq, err := http.NewRequestWithContext(ctx, method, frame.Audit.URL, bytes.NewReader(frame.Data))
		if err != nil {
			return fmt.Errorf("%w: native websocket audit request invalid: %v", requestaudit.ErrWrite, err)
		}
		auditReq.Header = safeAuditHeader(frame.Audit.Header)
		attempt, err := req.UpstreamAudit.BeginUpstreamAttempt(ctx, UpstreamAuditRequest{Method: auditReq.Method, URL: auditReq.URL.String(), Headers: auditReq.Header, Body: frame.Data})
		if err != nil {
			return fmt.Errorf("%w: native websocket audit begin failed: %v", requestaudit.ErrWrite, err)
		}
		if attempt == nil {
			return fmt.Errorf("%w: native websocket audit attempt unavailable", requestaudit.ErrWrite)
		}
		id := strings.TrimSpace(frame.Audit.AttemptID)
		if _, exists := auditAttempts[id]; exists {
			attempt.FinishUpstreamAttempt(UpstreamAuditResult{NetworkError: true, ErrorCode: "duplicate_audit_attempt"})
			return fmt.Errorf("%w: duplicate native websocket audit attempt", requestaudit.ErrWrite)
		}
		auditAttempts[id] = attempt
		return nil
	}
	handleAuditResult := func(frame protocol.CodexWebSocketFrame) {
		if frame.Audit == nil {
			return
		}
		if attempt := auditAttempts[strings.TrimSpace(frame.Audit.AttemptID)]; attempt != nil {
			attempt.FinishUpstreamAttempt(UpstreamAuditResult{StatusCode: frame.Audit.StatusCode, RetryAfter: frame.Audit.RetryAfter, LatencyMs: frame.Audit.LatencyMs, FirstTokenMs: frame.Audit.FirstTokenMs, ResponseStarted: frame.Audit.ResponseStarted, StreamCompleted: frame.Audit.StreamCompleted, NetworkError: frame.Audit.NetworkError, ErrorCode: frame.Audit.ErrorCode})
			delete(auditAttempts, strings.TrimSpace(frame.Audit.AttemptID))
		}
	}
	err := wsManager.ExecuteCodexWebSocket(ctx, wire, frames, func(frame protocol.CodexWebSocketFrame) error {
		eventMu.Lock()
		defer eventMu.Unlock()
		switch frame.Type {
		case protocol.CodexWebSocketFrameHeaders:
			// Preserve the upstream handshake marker at the Core boundary.  The
			// downstream HTTP 101 is already committed by the time model-specific
			// account selection can dial upstream, so these headers cannot be
			// copied into that response.  Pipeline still needs the marker to know
			// that provider work began (RPM/audit accounting), and its frame writer
			// deliberately treats metadata frames as non-wire events.
			if emit != nil {
				return emit(frame)
			}
			return nil
		case protocol.CodexWebSocketFrameCredentialUpdate:
			update := frame.CredentialUpdate
			if update == nil || strings.TrimSpace(update.AccessToken) == "" || (update.LeaseID != "" && update.LeaseID != wire.Credential.LeaseID) {
				return errors.New("invalid Codex websocket credential update")
			}
			if refreshed == nil {
				refreshed = cloneCodexRefreshCredentials(req.Account.Credentials)
				if refreshed == nil {
					refreshed = make(map[string]string)
				}
			}
			applyCodexCredentialUpdateMap(refreshed, *update)
			return nil
		case protocol.CodexWebSocketFrameAuditRequest:
			return handleAuditRequest(frame)
		case protocol.CodexWebSocketFrameAuditResult:
			handleAuditResult(frame)
			return nil
		case protocol.CodexWebSocketFrameError:
			if frame.Error != nil {
				return &CodexPluginError{Info: *frame.Error}
			}
			return errors.New("Codex websocket executor error")
		default:
			if emit == nil {
				return nil
			}
			return emit(frame)
		}
	})
	eventMu.Lock()
	for id, attempt := range auditAttempts {
		if attempt != nil {
			attempt.FinishUpstreamAttempt(UpstreamAuditResult{NetworkError: true, ErrorCode: "native_websocket_terminated"})
		}
		delete(auditAttempts, id)
	}
	eventMu.Unlock()
	return refreshed, err
}

func applyCodexCredentialUpdateMap(dst map[string]string, update protocol.CodexCredentialUpdate) {
	if dst == nil {
		return
	}
	dst["access_token"] = update.AccessToken
	if update.RefreshToken != "" {
		dst["refresh_token"] = update.RefreshToken
	}
	if update.SessionToken != "" {
		dst["session_token"] = update.SessionToken
	}
	if update.IDToken != "" {
		dst["id_token"] = update.IDToken
	}
	if update.AccountID != "" {
		dst["chatgpt_account_id"] = update.AccountID
	}
	if update.Email != "" {
		dst["email"] = update.Email
	}
	if update.PlanType != "" {
		dst["plan_type"] = update.PlanType
	}
	if update.SubscriptionActiveUntil != "" {
		dst["subscription_active_until"] = update.SubscriptionActiveUntil
	}
	if update.ChatGPTAccountIsFedrampSet || update.ChatGPTAccountIsFedramp {
		dst["chatgpt_account_is_fedramp"] = strconv.FormatBool(update.ChatGPTAccountIsFedramp)
	}
	if update.ExpiresAt > 0 {
		dst["expires_at"] = strconv.FormatInt(update.ExpiresAt, 10)
		dst["expired"] = time.Unix(update.ExpiresAt, 0).UTC().Format(time.RFC3339)
	}
}

type CodexPluginError struct{ Info protocol.CodexExecutorError }

func (e *CodexPluginError) Error() string { return fmt.Sprintf("codex plugin: %s", e.Info.Message) }

func usageFromEvent(u *protocol.CodexUsage) *dto.Usage {
	if u == nil {
		return nil
	}
	return &dto.Usage{PromptTokens: int(u.InputTokens), CachedTokens: int(u.CachedInputTokens), CompletionTokens: int(u.OutputTokens)}
}

func cloneValues(in map[string][]string) map[string][]string {
	if in == nil {
		return nil
	}
	out := make(map[string][]string, len(in))
	for key, values := range in {
		out[key] = append([]string(nil), values...)
	}
	return out
}

// codexProviderHeadersForEndpoint applies the stricter provider-scope
// projection required by the official turn-costs client. Keep Content-Type as
// wire metadata; the selected account credential is injected separately by the
// executor and is never accepted from this map.
func codexProviderHeadersForEndpoint(endpoint string, in map[string][]string) map[string][]string {
	if CodexOAuthOnlyEndpoint(endpoint) {
		out := cloneValues(in)
		if out == nil {
			out = make(map[string][]string)
		}
		stripCodexRemoteControlWebSocketOptionalHeaders(endpoint, out)
		// The remote plugin service identifies the Codex product via this
		// required header. Never trust a caller-supplied product value.
		out[http.CanonicalHeaderKey("OAI-Product-Sku")] = []string{"codex"}
		return out
	}
	if !strings.EqualFold(strings.TrimSpace(endpoint), protocol.CodexEndpointTurnCosts) {
		out := cloneValues(in)
		stripCodexRemoteControlWebSocketOptionalHeaders(endpoint, out)
		return out
	}
	out := make(map[string][]string)
	for name, values := range in {
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "openai-organization", "openai-project", "content-type":
			if len(values) > 0 {
				out[http.CanonicalHeaderKey(name)] = append([]string(nil), values...)
			}
		}
	}
	return out
}

func stripCodexRemoteControlWebSocketOptionalHeaders(endpoint string, headers map[string][]string) {
	if !strings.EqualFold(strings.TrimSpace(endpoint), protocol.CodexEndpointRemoteControlServerWebSocket) || len(headers) == 0 {
		return
	}
	for name := range headers {
		if strings.EqualFold(strings.TrimSpace(name), "X-Codex-Host-Device-Kind") || strings.EqualFold(strings.TrimSpace(name), "X-Codex-Subscribe-Cursor") {
			delete(headers, name)
		}
	}
}

func headerFromValues(in map[string][]string) http.Header { return http.Header(cloneValues(in)) }

func credentialAccessToken(authKind string, credentials map[string]string) string {
	// API-key leases must never copy a residual OAuth access_token into the
	// native executor.  The official turn-costs contract is API-key-only, and
	// even ordinary API-key requests must authenticate with the configured key
	// rather than an accidentally retained browser token.
	if IsCodexAPIKeyAuthType(authKind) {
		return ""
	}
	if token := strings.TrimSpace(credentials["access_token"]); token != "" {
		return token
	}
	return strings.TrimSpace(credentials["api_key"])
}

func credentialChatGPTAccountID(account Account) string {
	// ChatGPT workspace routing belongs only to a canonical OAuth Codex lease.
	// API-key records can contain residual imported OAuth fields; keep those out
	// of the executor envelope so no plugin version can combine the identities.
	if !IsCodexPlatform(account.Platform) || !IsCodexOAuthAuthType(account.Type) {
		return ""
	}
	return strings.TrimSpace(account.Credentials["chatgpt_account_id"])
}

func credentialHeader(credentials map[string]string) (string, string, bool) {
	name := strings.TrimSpace(credentials["auth_header_name"])
	if name == "" {
		name = "Authorization"
	}
	prefix, prefixSet := credentials["auth_header_prefix"]
	// Authorization is the only header with a universal bearer convention.
	// Custom API-key headers (for example X-API-Key) conventionally carry the
	// raw secret unless the account explicitly supplies auth_header_prefix.
	if !prefixSet && strings.EqualFold(name, "Authorization") {
		prefix = "Bearer "
	}
	return name, prefix, prefixSet
}

func credentialOAuthEndpoint(account Account) (string, string) {
	tokenURL := strings.TrimSpace(account.Credentials["token_url"])
	if tokenURL == "" {
		tokenURL = strings.TrimSpace(account.Credentials["token_endpoint"])
	}
	clientID := strings.TrimSpace(account.Credentials["client_id"])
	isCodexOAuth := IsCodexPlatform(account.Platform) && IsCodexOAuthAuthType(account.Type)
	if isCodexOAuth && strings.TrimSpace(account.Credentials["refresh_token"]) != "" {
		if tokenURL == "" {
			tokenURL = defaultCodexOAuthTokenURL
		}
		if clientID == "" {
			clientID = defaultCodexOAuthClientID
		}
	}
	return tokenURL, clientID
}

func credentialSessionEndpoint(account Account) string {
	if account.Credentials == nil {
		return ""
	}
	if !IsCodexPlatform(account.Platform) || !IsCodexOAuthAuthType(account.Type) {
		return ""
	}
	if strings.TrimSpace(account.Credentials["session_token"]) == "" {
		return ""
	}
	for _, key := range []string{"session_url", "session_endpoint"} {
		if value := strings.TrimSpace(account.Credentials[key]); value != "" {
			return value
		}
	}
	return defaultCodexSessionURL
}

func codexFedrampCredential(credentials map[string]string) bool {
	for _, key := range []string{"chatgpt_account_is_fedramp", "is_fedramp_account", "fedramp"} {
		value := strings.TrimSpace(strings.ToLower(credentials[key]))
		if value == "true" || value == "1" || value == "yes" {
			return true
		}
	}
	return false
}

func codexFedrampCredentialPresent(credentials map[string]string) bool {
	if credentials == nil {
		return false
	}
	for _, key := range []string{"chatgpt_account_is_fedramp", "is_fedramp_account", "fedramp"} {
		if _, exists := credentials[key]; exists {
			return true
		}
	}
	return false
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func nativeEligible(req Request, mode CodexTransportMode) bool {
	// Native execution is reserved for canonical Codex accounts. Synthetic
	// CPA channel identities (for example openai-compatibility) must stay on
	// the translation transport; treating them as native would bypass CPA's
	// protocol conversion and channel-specific request construction.
	if !IsCodexPlatform(req.Account.Platform) {
		return false
	}
	// The account service persists only oauth/api_key, but this transport is
	// also callable directly by tests and third-party integrations.  Reject an
	// empty or unknown credential family here so it cannot fall through
	// credentialAccessToken and accidentally send a residual token through the
	// native executor.
	if !IsCodexNativeAuthType(req.Account.Type) {
		return false
	}
	if !CodexNativeWireContract(req.EntryProtocol, req.Endpoint) {
		return false
	}
	// The official turn-cost reconciliation endpoint is intentionally scoped to
	// API-key accounts. Keep this guard in the transport itself as well as in
	// Core's account picker so direct transport callers cannot route an OAuth
	// lease to the public analytics API.
	if strings.EqualFold(strings.TrimSpace(req.Endpoint), protocol.CodexEndpointTurnCosts) &&
		!IsCodexAPIKeyAuthType(req.Account.Type) {
		return false
	}
	if CodexOAuthOnlyEndpoint(req.Endpoint) &&
		!IsCodexOAuthAuthType(req.Account.Type) {
		return false
	}
	switch NormalizeCodexTransportMode(string(mode)) {
	case CodexModeCPAOnly:
		return false
	case CodexModeCPATranslate:
		// cpa_translate reserves endpoints with a stable CPA contract for
		// translation, but leaves native-only Codex contracts (compact/search)
		// on the official executor.
		return !CodexCPATranslationContract(req.Endpoint)
	default:
		return true
	}
}

// codexRemoteControlPathPresent guards the two Remote Control HTTP operations
// whose URL contains dynamic identifiers. Static Remote Control endpoints and
// every unrelated Codex endpoint are valid without this check. The HTTP
// pipeline validates the identifiers more strictly; this second boundary
// protects direct transport callers and older integrations that construct a
// Request themselves.
func codexRemoteControlPathPresent(endpoint, path string) bool {
	switch strings.ToLower(strings.TrimSpace(endpoint)) {
	case protocol.CodexEndpointRemoteControlClientsList:
		return codexRemoteControlPathShape(path, 5)
	case protocol.CodexEndpointRemoteControlClientRevoke:
		return codexRemoteControlPathShape(path, 6)
	default:
		return true
	}
}

func codexRemoteControlPathShape(path string, wantSegments int) bool {
	path = strings.TrimRight(strings.TrimSpace(path), "/")
	if path == "" || !strings.HasPrefix(path, "/") {
		return false
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != wantSegments || len(parts) < 5 {
		return false
	}
	if parts[0] != "remote" || parts[1] != "control" || parts[2] != "environments" || parts[4] != "clients" {
		return false
	}
	// Dynamic IDs must be one non-empty segment. The entry handler rejects
	// traversal/control characters and encoded separators before this point;
	// reject the obvious malformed forms here too for direct callers.
	for _, value := range parts[3:] {
		if strings.TrimSpace(value) == "" || value == "." || value == ".." || strings.ContainsAny(value, "\\?#") {
			return false
		}
	}
	return true
}

// cloneCodexRefreshCredentials copies only the authentication and identity
// fields that the native executor can refresh. Routing policy is deliberately
// not part of this lease or refresh map.
func cloneCodexRefreshCredentials(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	keys := [...]string{
		"access_token", "refresh_token", "id_token", "session_token", "session_url", "session_endpoint",
		"chatgpt_account_id", "email", "plan_type", "subscription_active_until",
		"chatgpt_account_is_fedramp", "is_fedramp_account", "fedramp", "expires_at", "expired", "expires_in",
		"token_url", "token_endpoint", "client_id", "auth_header_name", "auth_header_prefix", "api_key",
		"base_url", "auth_kind", "credential_origin", "account_id",
	}
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := in[key]; ok {
			out[key] = value
		}
	}
	return out
}

// CodexNativeWireContract reports whether an entry protocol and endpoint pair
// is implemented by the official Codex HTTP data plane. Account modes are not
// considered here: callers use this classification to distinguish native
// execution from requests that inherently require CPA protocol translation.
func CodexNativeWireContract(entryProtocol, endpoint string) bool {
	protocolName := strings.ToLower(strings.TrimSpace(entryProtocol))
	if protocolName != "" && protocolName != "openai" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(endpoint)) {
	case "responses", "images_generations", "images_edits", "compact", "alpha_search", "realtime_calls", "memories_trace_summarize", "realtime_sideband", "guardian", "guardian_classifier",
		"history_list_windows", "history_list_items", "history_read_item", "history_search_contents",
		"notes_list_files_by_prefix", "notes_read_file", "notes_search_contents", "notes_append_to_file", "notes_write_file", "notes_thread_hint",
		"analytics_events", "files_create", "files_finalize",
		"codex_usage", "codex_thread_usage", "codex_rate_limit_reset_credits",
		"codex_rate_limit_reset_credits_consume", "codex_accounts_check",
		"codex_accounts_send_add_credits_nudge_email", "codex_profiles_me",
		"codex_config_bundle", "codex_settings_user", "codex_tasks",
		"codex_tasks_list", "codex_task_details", "codex_task_sibling_turns",
		"codex_environments", "codex_environments_by_repo",
		"codex_workspace_messages", "codex_ps_mcp", protocol.CodexEndpointTurnCosts,
		"codex_plugins_list", "codex_plugins_search", "codex_plugins_suggested",
		"codex_plugins_installed", "codex_plugins_workspace_shared",
		"codex_plugins_workspace_created", "codex_plugin_detail",
		"codex_plugin_skill_detail", "codex_plugin_install",
		"codex_plugin_uninstall", "codex_plugin_shares",
		"codex_connectors_directory_list", "codex_connectors_directory_list_workspace",
		"codex_apps_batch", "codex_plugins_featured",
		"codex_plugin_legacy_enable", "codex_plugin_legacy_uninstall",
		"codex_plugins_workspace_upload_url", "codex_plugins_workspace_create",
		"codex_plugins_workspace_update", "codex_plugins_workspace_detail",
		"codex_plugins_workspace_delete",
		protocol.CodexEndpointRemoteControlEnroll, protocol.CodexEndpointRemoteControlRefresh,
		protocol.CodexEndpointRemoteControlPair, protocol.CodexEndpointRemoteControlPairStatus,
		protocol.CodexEndpointRemoteControlClientsList, protocol.CodexEndpointRemoteControlClientRevoke,
		protocol.CodexEndpointRemoteControl, protocol.CodexEndpointRemoteControlServerWebSocket:
		return true
	default:
		return false
	}
}

func safeResponseHeader(in map[string][]string) http.Header {
	out := headerFromValues(in)
	connectionTokens := make(map[string]struct{})
	for _, value := range out.Values("Connection") {
		for _, token := range strings.Split(value, ",") {
			token = strings.ToLower(strings.TrimSpace(token))
			if token != "" {
				connectionTokens[token] = struct{}{}
			}
		}
	}
	for name := range out {
		if _, blocked := connectionTokens[strings.ToLower(strings.TrimSpace(name))]; blocked {
			out.Del(name)
		}
	}
	for _, name := range []string{"Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Transfer-Encoding", "Upgrade", "TE", "Trailer"} {
		out.Del(name)
	}
	return out
}

func safeAuditHeader(in map[string][]string) http.Header {
	out := headerFromValues(in)
	for name := range out {
		lower := strings.ToLower(name)
		if lower == "host" || lower == "content-length" || isAuditSecretHeader(lower) || isAuditHopByHopHeader(lower) {
			out.Del(name)
		}
	}
	return out
}

func isAuditSecretHeader(lower string) bool {
	for _, marker := range []string{"authorization", "api-key", "apikey", "token", "secret", "credential", "cookie"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func isAuditHopByHopHeader(lower string) bool {
	for _, name := range []string{"connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "transfer-encoding", "upgrade", "te", "trailer"} {
		if lower == name {
			return true
		}
	}
	return false
}

// markCodexFirstContent 按 CPA 同一口径记录内容首字：Responses 跳过 created 等生命周期事件，
// 只在真实增量/终态输出出现时落 FirstTokenMs。原生插件路径此前只写出流、不记首字，
// 用量里会显示为空。
func markCodexFirstContent(result *Result, req Request, data []byte, attemptStarted time.Time) {
	if result == nil || result.FirstTokenMs > 0 || !codexPayloadHasFirstContent(req.Endpoint, data) {
		return
	}
	now := time.Now()
	if !attemptStarted.IsZero() {
		result.FirstTokenMs = now.Sub(attemptStarted).Milliseconds()
	}
	if !req.RequestStartedAt.IsZero() && now.After(req.RequestStartedAt) {
		result.RequestFirstTokenMs = now.Sub(req.RequestStartedAt).Milliseconds()
	}
}

func codexPayloadHasFirstContent(endpoint string, data []byte) bool {
	switch strings.ToLower(strings.TrimSpace(endpoint)) {
	case protocol.CodexEndpointResponses:
		return dto.ResponsesPayloadHasContent(data)
	default:
		return len(bytes.TrimSpace(data)) > 0
	}
}
