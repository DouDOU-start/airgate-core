package pipeline

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
	"github.com/DouDOU-start/airgate-core/internal/relay/cpa"
	providertransport "github.com/DouDOU-start/airgate-core/internal/relay/transport"
	"github.com/DouDOU-start/airgate-core/internal/server/middleware"
)

// executeProvider routes through the new Core-owned transport contract when
// configured, while retaining the legacy CPA interface for test doubles and
// callers that have not migrated yet.
func (p *Pipeline) executeProvider(ctx context.Context, c *gin.Context, req cpa.ForwardRequest) cpa.ForwardResult {
	endpoint := strings.ToLower(strings.TrimSpace(req.Endpoint))
	compact := endpoint == adaptor.EndpointCompact
	mode := p.codexTransportMode()
	// cpa_only is an explicit translation-only policy.  Endpoints without a
	// CPA wire contract must fail closed before either transport is selected;
	// keep compact's specific public error for compatibility.  Do this outside
	// the providerTransport branch as legacy/test pipelines may only configure
	// the CPA bridge.
	// Plugin-level Codex policy applies only to the canonical Codex account
	// platform. Other account platforms keep their existing CPA/channel path.
	codexPlatform := providertransport.IsCodexPlatform(req.Account.Platform)
	if codexPlatform && mode == providertransport.CodexModeCPAOnly && !providerCPAEligibleEndpoint(endpoint) {
		if compact {
			return cpa.ForwardResult{BuildErr: cpa.ErrCompactUnsupported}
		}
		return cpa.ForwardResult{BuildErr: providertransport.ErrCodexCPAUnsupported}
	}
	// Account transport mode only selects between the two implementations of a
	// real Codex wire contract. Requests entering through Anthropic, Gemini, or
	// another CPA-supported non-native endpoint require protocol translation by
	// definition, so they must never be rejected by native_only. Likewise,
	// cpa_translate/cpa_only are dispatched straight to CPA instead of probing
	// the native plugin and treating its unsupported result as a fallback.
	if codexPlatform && codexRequestRequiresCPA(req, mode) {
		return p.executeCPA(ctx, c, req)
	}
	if p.providerTransport != nil {
		providerReq := providerRequestFromCPA(ctx, c, req)
		if eligibility, ok := p.providerTransport.(providertransport.AccountEligibility); ok &&
			!eligibility.SupportsAccount(providerReq.Account) {
			// The production native transport is intentionally Codex-specific.
			// Requests outside its account family (for example XAI video
			// accounts) belong to CPA and must not be converted into a local
			// unsupported-capability error merely because the plugin is installed.
			return p.executeCPA(ctx, c, req)
		}
		// A translation-only provider is the legacy CPA adapter. Compact has an
		// independent unary wire contract, so reject it before CPA sees the
		// payload instead of misclassifying it as a Responses request.
		if capabilities, ok := p.providerTransport.(providertransport.CapabilityProvider); ok &&
			compact && capabilities.Capabilities().Translation {
			return cpa.ForwardResult{BuildErr: cpa.ErrCompactUnsupported}
		}
		if providerAuditSinkFromContext(ctx) != nil {
			if capabilities, ok := p.providerTransport.(providertransport.CapabilityProvider); ok && capabilities.Capabilities().Translation {
				// The CPA adapter is observed by the in-process RoundTripper in
				// context; do not let its own proxy selection bypass that wrapper.
				providerReq.Account.ProxyURL = ""
			}
		}
		result := p.providerTransport.Execute(ctx, providerReq)
		retryablePluginNetErr := false
		if pluginErr, ok := result.NetErr.(*providertransport.CodexPluginError); ok {
			phase := strings.ToLower(strings.TrimSpace(pluginErr.Info.Phase))
			// Switching wire implementations is safe only when the native
			// executor explicitly confirms that no upstream response headers were
			// observed. An after_headers failure may already have consumed a
			// provider request (and can carry response semantics that CPA cannot
			// reproduce), even when no body bytes reached Core yet.
			retryablePluginNetErr = pluginErr.Info.Retryable && phase == "before_headers"
		}
		// Automatic CPA fallback is limited to endpoints with an explicit CPA
		// translation contract. Compact has a distinct unary contract and alpha
		// search has no translation contract. Native-only accounts fail closed on
		// the plugin. Switching is safe only before any provider data was observed
		// or downstream output and never for an actual HTTP 4xx/5xx.
		httpFailure := result.StatusCode >= 400 && result.StatusCode <= 599
		// A native executor's phase label is advisory metadata. Once Core has
		// observed any upstream response status/header, the provider request has
		// crossed the replay-safety boundary even if a buggy/older plugin later
		// reports the error as before_headers. Prefer the concrete response
		// evidence and keep the failure on this attempt instead of replaying it
		// through CPA.
		providerResponseStarted := result.ResponseStarted || result.StatusCode != 0 || len(result.Headers) > 0
		nativeFallbackDisabled := codexPlatform && mode == providertransport.CodexModeNativeOnly
		if providerCPAEligibleEndpoint(endpoint) && !nativeFallbackDisabled &&
			!providerResponseStarted && !result.DataReceived && !result.Written && result.StreamErr == nil && !httpFailure &&
			(errors.Is(result.BuildErr, providertransport.ErrCodexPluginUnavailable) || errors.Is(result.BuildErr, providertransport.ErrCodexPluginUnsupported) || retryablePluginNetErr) &&
			(p.cpa != nil || pipelineHasCPATranslation(p.providerTransport)) {
			fallbackReq := req
			if len(result.RefreshedCredentials) > 0 {
				// A native executor may refresh OAuth before discovering a
				// pre-header transport failure. Feed the refreshed lease into the
				// CPA retry immediately, and retain it for persistence even when the
				// fallback succeeds (or performs its own refresh).
				fallbackReq.Account.Credentials = cloneStringMap(result.RefreshedCredentials)
			}
			fallback := p.executeCPA(ctx, c, fallbackReq)
			fallback.RefreshedCredentials = mergeRefreshedCredentials(result.RefreshedCredentials, fallback.RefreshedCredentials)
			return fallback
		}
		return providerResultToCPA(result)
	}
	if compact {
		return cpa.ForwardResult{BuildErr: cpa.ErrCompactUnsupported}
	}
	return p.executeCPA(ctx, c, req)
}

func mergeRefreshedCredentials(base, overlay map[string]string) map[string]string {
	if len(base) == 0 && len(overlay) == 0 {
		return nil
	}
	merged := cloneStringMap(base)
	if merged == nil {
		merged = make(map[string]string, len(overlay))
	}
	for key, value := range overlay {
		merged[key] = value
	}
	return merged
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func (p *Pipeline) executeCPA(ctx context.Context, c *gin.Context, req cpa.ForwardRequest) cpa.ForwardResult {
	if providerAuditSinkFromContext(ctx) != nil {
		// CPA receives the in-process audit RoundTripper through context. Clear
		// the account proxy only on the CPA copy so it cannot bypass that wrapper;
		// native execution retains the selected account proxy.
		req.Account.ProxyURL = ""
	}
	if p.cpa != nil {
		return p.cpa.Forward(ctx, c, req)
	}
	// Pipeline.New normally keeps opts.CPA alongside the native transport. The
	// capability fallback makes manually assembled pipelines robust as well:
	// when only a CPAAdapter was injected, translated requests still use it;
	// a native-only transport is never invoked as a substitute for CPA.
	if p.providerTransport != nil {
		if capabilities, ok := p.providerTransport.(providertransport.CapabilityProvider); ok && capabilities.Capabilities().Translation {
			return providerResultToCPA(p.providerTransport.Execute(ctx, providerRequestFromCPA(ctx, c, req)))
		}
	}
	return cpa.ForwardResult{BuildErr: errCPAUnavailable}
}

func providerRequestFromCPA(ctx context.Context, c *gin.Context, req cpa.ForwardRequest) providertransport.Request {
	method := strings.TrimSpace(req.Method)
	if method == "" {
		method = http.MethodPost
	}
	path := strings.TrimSpace(req.Path)
	if path == "" {
		path = providerPath(req.Endpoint)
	}
	path = codexProviderPathForAccount(req.Account, req.Endpoint, path)
	headers := cloneHTTPHeaders(req.Headers)
	if headers == nil {
		headers = make(http.Header)
	}
	if strings.EqualFold(strings.TrimSpace(req.Endpoint), adaptor.EndpointCodexTurnCosts) {
		headers = codexTurnCostsProviderHeaders(headers)
	}
	if strings.TrimSpace(req.RawContentType) != "" && headers.Get("Content-Type") == "" {
		headers.Set("Content-Type", req.RawContentType)
	}
	account := providertransport.Account{
		ID: req.Account.AccountID, Name: req.Account.Name, Platform: req.Account.Platform,
		Type: req.Account.Type, Credentials: req.Account.Credentials, ProxyURL: req.Account.ProxyURL,
	}
	query := providerQueryWithOverrideForEndpoint(c, req.Endpoint, req.Query)
	if isCodexTurnCostsEndpoint(req.Endpoint) {
		// Official turn-cost requests carry turn_ids in the JSON body and clear
		// the URL query entirely. A custom provider may still encode its own
		// tenant query in BaseURL; caller-supplied query values do not belong on
		// this endpoint.
		query = nil
	}
	payload := req.Payload
	if req.RawBody != nil {
		payload = req.RawBody
	}
	return providertransport.Request{
		Account:                      account,
		RequestID:                    requestIDForProvider(c),
		Client:                       relayClientType(c),
		GroupID:                      groupIDFromContext(c),
		Model:                        req.Model,
		Method:                       method,
		BaseURL:                      codexProviderBaseURLForAccount(req.Account, req.Endpoint),
		Path:                         path,
		Query:                        query,
		Transport:                    providerWireTransport(req.Stream),
		UpstreamModel:                req.UpstreamModel,
		Endpoint:                     req.Endpoint,
		EntryProtocol:                req.EntryProtocol,
		Stream:                       req.Stream,
		Payload:                      append([]byte(nil), payload...),
		RawBody:                      appendNilPreservingBytes(req.RawBody),
		RawContentType:               req.RawContentType,
		Headers:                      headers,
		RequestStartedAt:             req.RequestStartedAt,
		CursorSessionKey:             req.CursorSessionKey,
		RemoteControlToken:           req.RemoteControlToken,
		RemoteControlServerID:        req.RemoteControlServerID,
		RemoteControlName:            req.RemoteControlName,
		RemoteControlProtocolVersion: req.RemoteControlProtocolVersion,
		InstallationID:               req.InstallationID,
		UpstreamAudit:                providerAuditSinkFromContext(ctx),
		LegacyContext:                c,
	}
}

// appendNilPreservingBytes clones a byte slice without collapsing the
// distinction between an absent raw body and an explicitly present empty raw
// body. That presence bit is part of the provider transport contract.
func appendNilPreservingBytes(in []byte) []byte {
	if in == nil {
		return nil
	}
	return append([]byte(nil), in...)
}

// codexProviderPathForAccount resolves the small provider-shape difference
// between the public Frameless /live endpoint and the ChatGPT OAuth backend.
// The official CLI sends /v1/live to the public API, but ChatGPT OAuth keeps
// the same call-create wire under /backend-api/codex/realtime/calls.  The
// endpoint is selected once per account, so a mixed group can safely contain
// both API-key and OAuth accounts without leaking one provider's path shape to
// the other.
func codexProviderPathForAccount(account cpa.AccountAuthInput, endpoint, requestedPath string) string {
	path := strings.TrimSpace(requestedPath)
	// Client-management routes contain caller-supplied environment/client
	// identifiers. There is no safe endpoint-only default for these paths. Keep
	// an omitted path empty so the native transport can fail closed rather than
	// manufacturing a resource URL; the HTTP entry handler always supplies the
	// validated expanded path.
	if path == "" && (isCodexRemoteControlDynamicEndpoint(endpoint) || endpoint == adaptor.EndpointCodexEnvironmentsByRepo || isCodexPluginDynamicEndpoint(endpoint)) {
		return ""
	}
	if path == "" {
		path = providerPath(endpoint)
	}
	endpoint = strings.TrimSpace(endpoint)
	base := strings.TrimRight(strings.TrimSpace(account.Credentials["base_url"]), "/")
	if base == "" && !providertransport.IsCodexAPIKeyAuthType(account.Type) {
		base = "https://chatgpt.com/backend-api/codex"
	}
	// The app-server turn-cost worker is the one native Codex endpoint whose
	// hosted ChatGPT URL is built from an API origin rather than the account's
	// `/backend-api/codex` model base. Normalize every Core route alias to the
	// canonical suffix; codexProviderBaseURLForAccount applies the host/base
	// rewrite for the official ChatGPT origins below.
	if isCodexTurnCostsEndpoint(endpoint) {
		if parsed, err := url.Parse(path); err == nil && parsed.Path != "" {
			if codexTurnCostsPathMatches(parsed.Path) {
				return "/analytics/codex/turn-costs"
			}
		}
		if codexTurnCostsPathMatches(path) {
			return "/analytics/codex/turn-costs"
		}
		// Keep an unknown path intact so the native executor's finite endpoint
		// allowlist can reject it instead of silently converting an arbitrary
		// request into a turn-cost query.
		return path
	}
	if isCodexManagementEndpoint(endpoint) {
		return codexManagementPathForAccount(account, endpoint, base, path)
	}
	// Analytics is the one official Codex endpoint whose client builds its URL
	// from the ChatGPT backend root (`.../backend-api/codex/...`) rather than
	// the model provider's `/backend-api/codex` base. Accommodate both forms so
	// custom accounts and reverse proxies do not receive a duplicated `codex`
	// path segment.
	if strings.EqualFold(endpoint, adaptor.EndpointAnalyticsEvents) {
		isCodexBase, isBackendRoot := codexBackendBasePathKinds(base)
		if isCodexBase {
			return "/analytics-events/events"
		}
		if isBackendRoot {
			return "/codex/analytics-events/events"
		}
		return path
	}
	// Files is rooted at the ChatGPT backend root (`/backend-api/files`), not
	// under `/backend-api/codex/files`. Keep the shortest `/files` path for both
	// the normal `/backend-api/codex` base (which is lowered to `/backend-api`
	// by codexProviderBaseURLForAccount) and callers that already supplied a
	// `/backend-api` base. Other control-plane families retain their `/codex`
	// prefix handling below.
	if isCodexHistoryNotesEndpoint(endpoint) {
		_, isBackendRoot := codexBackendBasePathKinds(base)
		if isBackendRoot {
			return addCodexBackendPrefix(path)
		}
	}
	if !strings.EqualFold(strings.TrimSpace(endpoint), adaptor.EndpointRealtimeCalls) || path != "/live" {
		return path
	}
	// The upstream route shape is determined by the URL path, not the host.
	// Private reverse proxies commonly retain /backend-api/codex while using a
	// different hostname; those still require the backend /realtime/calls path.
	if isCodexBackendBaseURLPath(base) {
		return "/realtime/calls"
	}
	return path
}

func isCodexRemoteControlDynamicEndpoint(endpoint string) bool {
	switch strings.ToLower(strings.TrimSpace(endpoint)) {
	case adaptor.EndpointCodexRemoteControlClientsList, adaptor.EndpointCodexRemoteControlClientRevoke:
		return true
	default:
		return false
	}
}

func isCodexPluginDynamicEndpoint(endpoint string) bool {
	switch strings.ToLower(strings.TrimSpace(endpoint)) {
	case adaptor.EndpointCodexPluginDetail,
		adaptor.EndpointCodexPluginSkillDetail,
		adaptor.EndpointCodexPluginInstall,
		adaptor.EndpointCodexPluginUninstall,
		adaptor.EndpointCodexPluginShares,
		adaptor.EndpointCodexPluginLegacyEnable,
		adaptor.EndpointCodexPluginLegacyUninstall,
		adaptor.EndpointCodexPluginsWorkspaceUpdate,
		adaptor.EndpointCodexPluginsWorkspaceDetail,
		adaptor.EndpointCodexPluginsWorkspaceDelete:
		return true
	default:
		return false
	}
}

// addCodexBackendPrefix is idempotent because account_forward.go normalizes
// the path before handing the request to providerRequestFromCPA, which also
// accepts direct callers and performs the same endpoint normalization.
func addCodexBackendPrefix(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || path == "/codex" || strings.HasPrefix(path, "/codex/") {
		return path
	}
	if strings.HasPrefix(path, "/") {
		return "/codex" + path
	}
	return "/codex/" + path
}

func isCodexHistoryNotesEndpoint(endpoint string) bool {
	switch strings.ToLower(strings.TrimSpace(endpoint)) {
	case adaptor.EndpointHistoryListWindows, adaptor.EndpointHistoryListItems,
		adaptor.EndpointHistoryReadItem, adaptor.EndpointHistorySearchContents,
		adaptor.EndpointNotesListFilesByPrefix, adaptor.EndpointNotesReadFile,
		adaptor.EndpointNotesSearchContents, adaptor.EndpointNotesAppendToFile,
		adaptor.EndpointNotesWriteFile, adaptor.EndpointNotesThreadHint:
		return true
	default:
		return false
	}
}

func isCodexTurnCostsEndpoint(endpoint string) bool {
	return strings.EqualFold(strings.TrimSpace(endpoint), adaptor.EndpointCodexTurnCosts)
}

func codexTurnCostsPathMatches(raw string) bool {
	path := strings.TrimRight(strings.ToLower(strings.TrimSpace(raw)), "/")
	for _, prefix := range []string{
		"", "/v1", "/codex", "/codex/v1", "/api/codex", "/api/codex/v1",
		"/backend-api", "/backend-api/v1", "/backend-api/codex", "/backend-api/codex/v1",
		"/backend-api/v1/wham", "/backend-api/wham", "/backend-api/wham/v1", "/wham", "/wham/v1",
	} {
		if path == prefix+"/analytics/codex/turn-costs" {
			return true
		}
	}
	return false
}

// isCodexManagementEndpoint identifies the official backend-client control
// plane. These calls use API-style `/api/codex/...` paths or ChatGPT's
// `/backend-api/wham/...` paths and must remain raw/native.
func isCodexManagementEndpoint(endpoint string) bool {
	switch strings.ToLower(strings.TrimSpace(endpoint)) {
	case adaptor.EndpointCodexUsage, adaptor.EndpointCodexThreadUsage,
		adaptor.EndpointCodexRateLimitResetCredits,
		adaptor.EndpointCodexRateLimitResetCreditsConsume,
		adaptor.EndpointCodexAccountsCheck, adaptor.EndpointCodexAccountsNudge,
		adaptor.EndpointCodexProfilesMe, adaptor.EndpointCodexConfigBundle,
		adaptor.EndpointCodexSettingsUser, adaptor.EndpointCodexTasks,
		adaptor.EndpointCodexTasksList, adaptor.EndpointCodexTaskDetails,
		adaptor.EndpointCodexTaskSiblingTurns,
		adaptor.EndpointCodexEnvironments, adaptor.EndpointCodexEnvironmentsByRepo,
		adaptor.EndpointCodexWorkspaceMessages, adaptor.EndpointCodexPSMCP,
		adaptor.EndpointCodexPluginsList, adaptor.EndpointCodexPluginsSearch,
		adaptor.EndpointCodexPluginsSuggested, adaptor.EndpointCodexPluginsInstalled,
		adaptor.EndpointCodexPluginsWorkspaceShared,
		adaptor.EndpointCodexPluginsWorkspaceCreated, adaptor.EndpointCodexPluginDetail,
		adaptor.EndpointCodexPluginSkillDetail, adaptor.EndpointCodexPluginInstall,
		adaptor.EndpointCodexPluginUninstall, adaptor.EndpointCodexPluginShares,
		adaptor.EndpointCodexConnectorsDirectoryList,
		adaptor.EndpointCodexConnectorsDirectoryListWorkspace,
		adaptor.EndpointCodexAppsBatch,
		adaptor.EndpointCodexPluginsFeatured,
		adaptor.EndpointCodexPluginLegacyEnable,
		adaptor.EndpointCodexPluginLegacyUninstall,
		adaptor.EndpointCodexPluginsWorkspaceUploadURL,
		adaptor.EndpointCodexPluginsWorkspaceCreate,
		adaptor.EndpointCodexPluginsWorkspaceUpdate,
		adaptor.EndpointCodexPluginsWorkspaceDetail,
		adaptor.EndpointCodexPluginsWorkspaceDelete:
		return true
	case adaptor.EndpointCodexRemoteControlEnroll, adaptor.EndpointCodexRemoteControlRefresh,
		adaptor.EndpointCodexRemoteControlPair, adaptor.EndpointCodexRemoteControlPairStatus,
		adaptor.EndpointCodexRemoteControlClientsList, adaptor.EndpointCodexRemoteControlClientRevoke,
		adaptor.EndpointCodexRemoteControlServerWebSocket:
		return true
	default:
		return false
	}
}

// codexManagementPathForAccount maps the canonical management suffix (for
// example `/usage` or `/tasks/abc`) to the path style expected by the
// selected account. OAuth/ChatGPT accounts use `/wham`; API-style accounts
// use `/api/codex`. The account base URL is separately rooted at the host or
// `/backend-api` so the result cannot duplicate a terminal `/codex` segment.
func codexManagementPathForAccount(account cpa.AccountAuthInput, endpoint, base, path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	// A caller may pass an already-prefixed alias. Normalize it back to the
	// canonical suffix before applying the selected account style.
	for _, prefix := range []string{
		// Reduce the longest aliases first. Otherwise
		// `/backend-api/v1/wham/...` would match `/backend-api/v1` and
		// leave `/wham/...` to be prefixed a second time below.
		"/backend-api/v1/wham", "/backend-api/wham/v1",
		"/backend-api/wham", "/backend-api/codex/v1",
		"/backend-api/codex", "/backend-api/v1", "/backend-api",
		"/api/codex/v1", "/api/codex", "/wham/v1", "/wham",
		"/codex/v1", "/codex", "/v1",
	} {
		if path == prefix {
			path = "/"
			break
		}
		if strings.HasPrefix(path, prefix+"/") {
			path = strings.TrimPrefix(path, prefix)
			break
		}
	}
	// Remote plugin APIs live directly below the ChatGPT backend root
	// (`/backend-api/ps/plugins`), not below `/wham` or `/api/codex`. Keep the
	// validated dynamic suffix intact while the endpoint-specific base rewrite
	// removes a terminal `/codex` segment when needed.
	if isCodexBackendRootEndpoint(endpoint) {
		return path
	}
	if strings.EqualFold(strings.TrimSpace(path), "/ps/mcp") {
		// Hosted Apps MCP is rooted directly under `/backend-api`, whereas a
		// public API-style gateway may expose the same contract under `/api/codex`.
		isCodexBase, backendRoot := codexBackendBasePathKinds(base)
		if backendRoot || isCodexBase {
			return "/ps/mcp"
		}
		return "/api/codex/ps/mcp"
	}
	isCodexBase, backendRoot := codexBackendBasePathKinds(base)
	if backendRoot || isCodexBase {
		return "/wham" + path
	}
	if isCodexAPIBasePath(base) || providertransport.IsCodexAPIKeyAuthType(account.Type) {
		return "/api/codex" + path
	}
	// An ordinary custom provider base (for example
	// `https://gateway.example/openai/v1`) owns its path namespace. Preserve
	// the canonical suffix instead of guessing that it is a ChatGPT WHAM
	// gateway; callers that need `/wham` can express that explicitly with a
	// `/backend-api` base.
	return path
}

func isCodexPluginEndpoint(endpoint string) bool {
	switch strings.ToLower(strings.TrimSpace(endpoint)) {
	case adaptor.EndpointCodexPluginsList, adaptor.EndpointCodexPluginsSearch,
		adaptor.EndpointCodexPluginsSuggested, adaptor.EndpointCodexPluginsInstalled,
		adaptor.EndpointCodexPluginsWorkspaceShared,
		adaptor.EndpointCodexPluginsWorkspaceCreated, adaptor.EndpointCodexPluginDetail,
		adaptor.EndpointCodexPluginSkillDetail, adaptor.EndpointCodexPluginInstall,
		adaptor.EndpointCodexPluginUninstall, adaptor.EndpointCodexPluginShares:
		return true
	default:
		return false
	}
}

// isCodexBackendRootEndpoint identifies ChatGPT Apps/Connectors and public
// workspace-plugin service calls. Unlike the ordinary `/wham` management
// family, these paths are rooted directly below the ChatGPT backend root
// (`/backend-api`). Keeping the classification separate prevents a custom
// reverse proxy from receiving an accidental `/wham` prefix.
func isCodexBackendRootEndpoint(endpoint string) bool {
	switch strings.ToLower(strings.TrimSpace(endpoint)) {
	case adaptor.EndpointCodexConnectorsDirectoryList,
		adaptor.EndpointCodexConnectorsDirectoryListWorkspace,
		adaptor.EndpointCodexAppsBatch,
		adaptor.EndpointCodexPluginsFeatured,
		adaptor.EndpointCodexPluginLegacyEnable,
		adaptor.EndpointCodexPluginLegacyUninstall,
		adaptor.EndpointCodexPluginsWorkspaceUploadURL,
		adaptor.EndpointCodexPluginsWorkspaceCreate,
		adaptor.EndpointCodexPluginsWorkspaceUpdate,
		adaptor.EndpointCodexPluginsWorkspaceDetail,
		adaptor.EndpointCodexPluginsWorkspaceDelete:
		return true
	default:
		return false
	}
}

func cloneHTTPHeaders(in http.Header) http.Header {
	if len(in) == 0 {
		return nil
	}
	out := make(http.Header, len(in))
	for key, values := range in {
		out[http.CanonicalHeaderKey(key)] = append([]string(nil), values...)
	}
	return out
}

func cloneForwardQueryValues(in map[string][]string) map[string][]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string][]string, len(in))
	for key, values := range in {
		if providerQueryCredentialKey(key) || len(values) == 0 {
			continue
		}
		out[key] = append([]string(nil), values...)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// providerQueryWithOverride merges safe inbound query parameters with an
// endpoint-specific override. Explicit values are appended so repeated query
// keys (including Codex feature flags) retain their wire semantics.
func providerQueryWithOverride(c *gin.Context, override map[string][]string) map[string][]string {
	return providerQueryWithOverrideForEndpoint(c, "", override)
}

// providerQueryWithOverrideForEndpoint is the endpoint-aware query projector
// used by native Codex control-plane requests. Most credential-shaped query
// names are removed, but the Connectors directory API intentionally uses
// `token` as a pagination cursor. That value is data, not authentication, and
// must survive pagination while the generic projector continues to strip
// token values from unrelated endpoints.
func providerQueryWithOverrideForEndpoint(c *gin.Context, endpoint string, override map[string][]string) map[string][]string {
	base := providerQueryForEndpoint(c, endpoint)
	if len(override) == 0 {
		return base
	}
	if base == nil {
		base = make(map[string][]string, len(override))
	}
	for key, values := range override {
		if providerQueryCredentialKeyForEndpoint(endpoint, key) {
			continue
		}
		base[key] = append(base[key], values...)
	}
	return base
}

func providerQueryForEndpoint(c *gin.Context, endpoint string) map[string][]string {
	if c == nil || c.Request == nil || c.Request.URL == nil {
		return nil
	}
	query := c.Request.URL.Query()
	if len(query) == 0 {
		return nil
	}
	out := make(map[string][]string, len(query))
	for key, values := range query {
		if providerQueryCredentialKeyForEndpoint(endpoint, key) {
			continue
		}
		out[key] = append([]string(nil), values...)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func providerQueryCredentialKeyForEndpoint(endpoint, key string) bool {
	lowerEndpoint := strings.ToLower(strings.TrimSpace(endpoint))
	lowerKey := strings.ToLower(strings.TrimSpace(key))
	if lowerKey == "token" && (lowerEndpoint == adaptor.EndpointCodexConnectorsDirectoryList ||
		lowerEndpoint == adaptor.EndpointCodexConnectorsDirectoryListWorkspace) {
		return false
	}
	return providerQueryCredentialKey(key)
}

// codexRequestRequiresCPA reports whether a Codex account request is
// necessarily handled by the translation plane. Callers must first establish
// that the selected account uses the canonical Codex platform.
func (p *Pipeline) codexTransportMode() providertransport.CodexTransportMode {
	if p == nil {
		return providertransport.CodexModeAuto
	}
	policy := p.codexTransportPolicy
	if policy == nil && p.providerTransport != nil {
		policy, _ = p.providerTransport.(providertransport.CodexTransportPolicy)
	}
	if policy == nil {
		return providertransport.CodexModeAuto
	}
	return providertransport.NormalizeCodexTransportMode(policy.CodexTransportMode())
}

// codexResponsesForwardOptions is intentionally empty for the shared
// Responses entry point. A Codex client may be scheduled to an XAI/OpenAI
// account, where it must retain the historical CPA behavior and still receive
// the client-scoped instruction hook. Applying Codex mode before account
// selection would reorder or filter the mixed account pool (and native_only
// would incorrectly reject an otherwise valid XAI/OpenAI account). The mode is
// evaluated after a concrete account is selected by executeProvider.
func (p *Pipeline) codexResponsesForwardOptions(c *gin.Context) forwardOptions {
	return forwardOptions{}
}

// codexImagesForwardOptions follows the same post-selection rule as Responses.
// Images requests from a Codex client can legitimately be handled by an
// XAI/OpenAI CPA account, so no Codex-only account filtering is performed at
// the shared entry point. A selected canonical Codex account still observes
// the plugin-wide mode in executeProvider.
func (p *Pipeline) codexImagesForwardOptions(c *gin.Context) forwardOptions {
	return forwardOptions{}
}

func codexRequestRequiresCPA(req cpa.ForwardRequest, mode providertransport.CodexTransportMode) bool {
	endpoint := strings.ToLower(strings.TrimSpace(req.Endpoint))
	if !providerCPAEligibleEndpoint(endpoint) {
		return false
	}
	switch providertransport.NormalizeCodexTransportMode(string(mode)) {
	case providertransport.CodexModeCPAOnly, providertransport.CodexModeCPATranslate:
		return true
	default:
		return !providertransport.CodexNativeWireContract(req.EntryProtocol, endpoint)
	}
}

func providerCPAEligibleEndpoint(endpoint string) bool {
	return providertransport.CodexCPATranslationContract(endpoint)
}

func requestIDForProvider(c *gin.Context) string {
	if c == nil {
		return ""
	}
	return requestIDOf(c)
}

func groupIDFromContext(c *gin.Context) int {
	if c == nil {
		return 0
	}
	value, ok := c.Get(middleware.CtxKeyKeyInfo)
	if !ok {
		return 0
	}
	info, _ := value.(*auth.APIKeyInfo)
	if info == nil {
		return 0
	}
	return info.GroupID
}

func providerPath(endpoint string) string {
	switch strings.ToLower(strings.TrimSpace(endpoint)) {
	case adaptor.EndpointImagesGenerations:
		return "/images/generations"
	case adaptor.EndpointImagesEdits:
		return "/images/edits"
	case "alpha_search":
		return "/alpha/search"
	case "compact":
		return "/responses/compact"
	case "realtime_calls":
		return "/realtime/calls"
	case "realtime_sideband":
		return "/realtime"
	case "memories_trace_summarize":
		return "/memories/trace_summarize"
	case "guardian":
		return "/guardian"
	case "guardian_classifier":
		return "/guardian-classifier"
	case "history_list_windows":
		return "/alpha/history/v2/list_windows"
	case "history_list_items":
		return "/alpha/history/v2/list_items"
	case "history_read_item":
		return "/alpha/history/v2/read_item"
	case "history_search_contents":
		return "/alpha/history/v2/search_contents"
	case "notes_list_files_by_prefix":
		return "/alpha/notes/v2/list_files_by_prefix"
	case "notes_read_file":
		return "/alpha/notes/v2/read_file"
	case "notes_search_contents":
		return "/alpha/notes/v2/search_contents"
	case "notes_append_to_file":
		return "/alpha/notes/v2/append_to_file"
	case "notes_write_file":
		return "/alpha/notes/v2/write_file"
	case "notes_thread_hint":
		return "/alpha/notes/v2/thread_hint"
	case "analytics_events":
		return "/analytics-events/events"
	case "files_create":
		return "/files"
	case "files_finalize":
		return "/files/uploaded"
	case adaptor.EndpointCodexUsage:
		return "/usage"
	case adaptor.EndpointCodexThreadUsage:
		return "/usage/thread_usage/query"
	case adaptor.EndpointCodexRateLimitResetCredits:
		return "/rate-limit-reset-credits"
	case adaptor.EndpointCodexRateLimitResetCreditsConsume:
		return "/rate-limit-reset-credits/consume"
	case adaptor.EndpointCodexAccountsCheck:
		return "/accounts/check"
	case adaptor.EndpointCodexAccountsNudge:
		return "/accounts/send_add_credits_nudge_email"
	case adaptor.EndpointCodexProfilesMe:
		return "/profiles/me"
	case adaptor.EndpointCodexConfigBundle:
		return "/config/bundle"
	case adaptor.EndpointCodexSettingsUser:
		return "/settings/user"
	case adaptor.EndpointCodexTasks:
		return "/tasks"
	case adaptor.EndpointCodexTasksList:
		return "/tasks/list"
	case adaptor.EndpointCodexEnvironments:
		return "/environments"
	case adaptor.EndpointCodexEnvironmentsByRepo:
		// Provider/owner/repo (and the optional ref) are caller-supplied
		// opaque segments. The HTTP entry point must provide the fully expanded
		// path; never manufacture a collection URL for a by-repo lookup.
		return ""
	case adaptor.EndpointCodexTaskDetails:
		return "/tasks"
	case adaptor.EndpointCodexTaskSiblingTurns:
		return "/tasks"
	case adaptor.EndpointCodexWorkspaceMessages:
		return "/workspace-messages"
	case adaptor.EndpointCodexPSMCP:
		return "/ps/mcp"
	case adaptor.EndpointCodexPluginsList:
		return "/ps/plugins/list"
	case adaptor.EndpointCodexPluginsSearch:
		return "/ps/plugins/search"
	case adaptor.EndpointCodexPluginsSuggested:
		return "/ps/plugins/suggested/codex"
	case adaptor.EndpointCodexPluginsInstalled:
		return "/ps/plugins/installed"
	case adaptor.EndpointCodexPluginsWorkspaceShared:
		return "/ps/plugins/workspace/shared"
	case adaptor.EndpointCodexPluginsWorkspaceCreated:
		return "/ps/plugins/workspace/created"
	case adaptor.EndpointCodexConnectorsDirectoryList:
		return "/connectors/directory/list"
	case adaptor.EndpointCodexConnectorsDirectoryListWorkspace:
		return "/connectors/directory/list_workspace"
	case adaptor.EndpointCodexAppsBatch:
		return "/ps/apps/batch"
	case adaptor.EndpointCodexPluginsFeatured:
		return "/plugins/featured"
	case adaptor.EndpointCodexPluginLegacyEnable,
		adaptor.EndpointCodexPluginLegacyUninstall,
		adaptor.EndpointCodexPluginsWorkspaceUpdate,
		adaptor.EndpointCodexPluginsWorkspaceDetail,
		adaptor.EndpointCodexPluginsWorkspaceDelete:
		// These routes contain an opaque plugin id. The HTTP entry point must
		// provide the validated expanded path; never guess a collection URL.
		return ""
	case adaptor.EndpointCodexPluginsWorkspaceUploadURL:
		return "/public/plugins/workspace/upload-url"
	case adaptor.EndpointCodexPluginsWorkspaceCreate:
		return "/public/plugins/workspace"
	case adaptor.EndpointCodexRemoteControlEnroll:
		return "/remote/control/server/enroll"
	case adaptor.EndpointCodexRemoteControlRefresh:
		return "/remote/control/server/refresh"
	case adaptor.EndpointCodexRemoteControlPair:
		return "/remote/control/server/pair"
	case adaptor.EndpointCodexRemoteControlPairStatus:
		return "/remote/control/server/pair/status"
	case adaptor.EndpointCodexRemoteControlServerWebSocket:
		return "/wham/remote/control/server"
	case adaptor.EndpointCodexRemoteControlClientsList:
		// The environment identifier is part of this route and cannot be
		// reconstructed from an endpoint name alone. Callers must provide the
		// fully expanded path (the HTTP entry point does so after validating the
		// identifier); returning a made-up path here could send a request to the
		// wrong upstream resource when a new caller forgets to set Path.
		return ""
	case adaptor.EndpointCodexRemoteControlClientRevoke:
		// See the list endpoint above: revoke also requires both environment and
		// client identifiers, so there is no safe endpoint-only fallback.
		return ""
	case adaptor.EndpointCodexTurnCosts:
		return "/analytics/codex/turn-costs"
	default:
		return "/responses"
	}
}

func isCodexFilesEndpoint(endpoint string) bool {
	switch strings.ToLower(strings.TrimSpace(endpoint)) {
	case adaptor.EndpointFilesCreate, adaptor.EndpointFilesFinalize:
		return true
	default:
		return false
	}
}

// isCodexBackendControlEndpoint reports the Codex control-plane endpoints
// whose URL is built from the ChatGPT backend root rather than the inference
// model base.  Keeping this predicate separate from isCodexManagementEndpoint
// is important for the History/Notes and analytics endpoints: those routes
// are raw/native contracts, but they still need the same base-alias rewrite
// when a caller configures (for example) /backend-api/codex/v1.
func isCodexBackendControlEndpoint(endpoint string) bool {
	if isCodexFilesEndpoint(endpoint) || isCodexManagementEndpoint(endpoint) || isCodexPluginEndpoint(endpoint) {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(endpoint)) {
	case adaptor.EndpointAnalyticsEvents,
		adaptor.EndpointHistoryListWindows, adaptor.EndpointHistoryListItems,
		adaptor.EndpointHistoryReadItem, adaptor.EndpointHistorySearchContents,
		adaptor.EndpointNotesListFilesByPrefix, adaptor.EndpointNotesReadFile,
		adaptor.EndpointNotesSearchContents, adaptor.EndpointNotesAppendToFile,
		adaptor.EndpointNotesWriteFile, adaptor.EndpointNotesThreadHint:
		return true
	default:
		return false
	}
}

// codexControlBaseAliasNeedsRewrite keeps the historical URL shape for the
// canonical, unversioned aliases while normalizing newer/versioned aliases.
// For example, analytics on `/backend-api/codex` intentionally remains under
// that configured base, whereas `/backend-api/codex/v1` must lose the extra
// `/v1` before the canonical analytics suffix is appended. Files, plugins, and
// backend-client management always use the endpoint-specific root rewrite.
func codexControlBaseAliasNeedsRewrite(account cpa.AccountAuthInput, endpoint, base string) bool {
	if isCodexFilesEndpoint(endpoint) || isCodexManagementEndpoint(endpoint) || isCodexPluginEndpoint(endpoint) {
		return true
	}
	alias, _, ok := codexBasePathAliasForURL(base)
	if !ok {
		return false
	}
	// Public API-key `/v1` analytics/history/notes are ordinary OpenAI-style
	// paths and retain their configured model base for compatibility.
	if providertransport.IsCodexAPIKeyAuthType(account.Type) && alias.kind == codexBasePathVersioned {
		return false
	}
	switch alias.kind {
	case codexBasePathBackendCodex, codexBasePathBackendRoot, codexBasePathAPICodex:
		// Preserve the canonical unversioned spelling; only the newer `/v1`
		// aliases need a base-path reduction.
		return strings.HasSuffix(strings.ToLower(alias.suffix), "/v1")
	case codexBasePathBackendWham, codexBasePathWham, codexBasePathCodex:
		// These are family aliases rather than the historical inference base.
		return true
	case codexBasePathVersioned:
		return true
	default:
		return false
	}
}

func providerBaseURL(req cpa.ForwardRequest) string {
	return providerBaseURLForAccount(req.Account)
}

func providerBaseURLForAccount(account cpa.AccountAuthInput) string {
	if base := strings.TrimRight(strings.TrimSpace(account.Credentials["base_url"]), "/"); base != "" {
		return base
	}
	if providertransport.IsCodexAPIKeyAuthType(account.Type) {
		return "https://api.openai.com/v1"
	}
	return "https://chatgpt.com/backend-api/codex"
}

// codexBasePathKind identifies the terminal alias in a configured Codex base
// URL.  Official CLI revisions have used both versioned and unversioned
// spellings, and private reverse proxies commonly prepend a tenant path.  We
// classify only the terminal path suffix, preserving that prefix verbatim.
type codexBasePathKind uint8

const (
	codexBasePathUnknown codexBasePathKind = iota
	codexBasePathBackendCodex
	codexBasePathBackendWham
	codexBasePathBackendRoot
	codexBasePathAPICodex
	codexBasePathWham
	codexBasePathCodex
	codexBasePathVersioned
)

type codexBasePathAlias struct {
	kind   codexBasePathKind
	suffix string
}

// Longest aliases must be checked first.  Otherwise /backend-api/codex/v1
// would be classified as /backend-api/codex and leave a duplicate /v1 in the
// normalized path.
var codexBasePathAliases = []codexBasePathAlias{
	{kind: codexBasePathBackendCodex, suffix: "/backend-api/codex/v1"},
	{kind: codexBasePathBackendCodex, suffix: "/backend-api/codex"},
	{kind: codexBasePathBackendWham, suffix: "/backend-api/wham/v1"},
	{kind: codexBasePathBackendWham, suffix: "/backend-api/wham"},
	{kind: codexBasePathBackendRoot, suffix: "/backend-api/v1"},
	{kind: codexBasePathBackendRoot, suffix: "/backend-api"},
	{kind: codexBasePathAPICodex, suffix: "/api/codex/v1"},
	{kind: codexBasePathAPICodex, suffix: "/api/codex"},
	{kind: codexBasePathWham, suffix: "/wham/v1"},
	{kind: codexBasePathWham, suffix: "/wham"},
	{kind: codexBasePathCodex, suffix: "/codex/v1"},
	{kind: codexBasePathCodex, suffix: "/codex"},
	{kind: codexBasePathVersioned, suffix: "/v1"},
}

// codexBasePathAliasForURL returns the terminal alias and escaped URL path.
// Query/fragment data is deliberately ignored for classification, while the
// URL itself is required to have an authority so malformed values remain on
// the executor's normal validation path.
func codexBasePathAliasForURL(raw string) (codexBasePathAlias, string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u == nil || u.Host == "" || u.User != nil {
		return codexBasePathAlias{}, "", false
	}
	path := strings.TrimRight(u.EscapedPath(), "/")
	if path == "" {
		return codexBasePathAlias{}, "", false
	}
	lowerPath := strings.ToLower(path)
	for _, alias := range codexBasePathAliases {
		lowerSuffix := strings.ToLower(alias.suffix)
		if codexPathHasAliasSuffix(lowerPath, lowerSuffix) {
			return alias, path, true
		}
	}
	return codexBasePathAlias{}, path, false
}

// codexPathHasAliasSuffix compares complete path segments rather than relying
// solely on a byte suffix.  This keeps a look-alike such as
// `/not-backend-api/codex` out while still accepting a legitimate private
// reverse-proxy prefix such as `/tenant/backend-api/codex`.
func codexPathHasAliasSuffix(path, suffix string) bool {
	path = strings.TrimRight(path, "/")
	suffix = strings.TrimRight(suffix, "/")
	if path == "" || suffix == "" {
		return false
	}
	pathParts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	suffixParts := strings.Split(strings.TrimPrefix(suffix, "/"), "/")
	if len(pathParts) < len(suffixParts) {
		return false
	}
	start := len(pathParts) - len(suffixParts)
	for i, expected := range suffixParts {
		if !strings.EqualFold(pathParts[start+i], expected) {
			return false
		}
	}
	return true
}

// codexURLWithEscapedPath replaces only the path portion of raw, retaining its
// scheme, host, query, and fragment.  RawPath is kept when escaping differs
// so reverse-proxy prefixes such as %2F remain byte-for-byte stable.
func codexURLWithEscapedPath(raw, escapedPath string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u == nil || u.Host == "" {
		return raw
	}
	escapedPath = strings.TrimRight(escapedPath, "/")
	if escapedPath == "" {
		escapedPath = "/"
	}
	decodedPath, err := url.PathUnescape(escapedPath)
	if err != nil {
		return raw
	}
	u.Path = decodedPath
	if decodedPath == escapedPath {
		u.RawPath = ""
	} else {
		u.RawPath = escapedPath
	}
	return u.String()
}

// codexBackendRootPath turns any /backend-api[/codex|/wham][/v1] alias into
// the common backend root while preserving a private proxy's leading path.
func codexBackendRootPath(escapedPath string) (string, bool) {
	lowerPath := strings.ToLower(strings.TrimRight(escapedPath, "/"))
	marker := "/backend-api"
	idx := strings.LastIndex(lowerPath, marker)
	if idx < 0 {
		return "", false
	}
	// Preserve the configured casing/escaping of the marker itself.  The
	// marker is ASCII and therefore has the same byte length in both strings.
	return strings.TrimRight(escapedPath[:idx+len(marker)], "/"), true
}

// codexBackendBasePathKinds classifies only the URL path portion of a base
// URL. Query parameters are transport metadata and must not change whether a
// reverse-proxy base is rooted at `/backend-api` or `/backend-api/codex`.
// Versioned /wham and /codex aliases are accepted because newer official CLI
// builds may pass those complete bases to Core.
func codexBackendBasePathKinds(raw string) (isCodexBase, isBackendRoot bool) {
	alias, _, ok := codexBasePathAliasForURL(raw)
	if !ok {
		return false, false
	}
	switch alias.kind {
	case codexBasePathBackendCodex:
		return true, false
	case codexBasePathBackendRoot, codexBasePathBackendWham:
		return false, true
	default:
		return false, false
	}
}

func isCodexAPIBasePath(raw string) bool {
	alias, _, ok := codexBasePathAliasForURL(raw)
	return ok && alias.kind == codexBasePathAPICodex
}

// codexProviderBaseURLForAccount resolves the endpoint-specific split in the
// official ChatGPT backend. Most Codex routes are rooted at
// /backend-api/codex, while Files is rooted one level higher at
// /backend-api/files (and /backend-api/files/{id}/uploaded). Keep the
// account's configured host, scheme, query, and any private reverse-proxy
// prefix intact; only remove the terminal /codex segment for a
// /backend-api/codex-shaped base. Public API-key and ordinary custom gateway
// bases retain their existing semantics.
func codexProviderBaseURLForAccount(account cpa.AccountAuthInput, endpoint string) string {
	base := providerBaseURLForAccount(account)
	// Preserve every fragment-bearing configuration verbatim so the executor's
	// strict URL validation can reject it. Rewriting with net/url would otherwise
	// drop a trailing bare `#` because it is represented as an empty Fragment.
	if strings.Contains(base, "#") {
		return base
	}
	if isCodexTurnCostsEndpoint(endpoint) {
		return codexTurnCostsBaseURLForAccount(base)
	}
	if !isCodexBackendControlEndpoint(endpoint) {
		return base
	}
	if !codexControlBaseAliasNeedsRewrite(account, endpoint, base) {
		return base
	}
	// Public API-key Files follow the ordinary OpenAI Files contract under the
	// account's configured `/v1` base (`https://api.openai.com/v1/files`).  The
	// `/v1` root is stripped only for the separate backend-client management
	// family, whose API-style URLs are rooted at `/api/codex` on the host.  Do
	// not apply that management rewrite to Files or a valid API-key base would
	// become `https://api.openai.com/files`.
	if isCodexFilesEndpoint(endpoint) && providertransport.IsCodexAPIKeyAuthType(account.Type) {
		return base
	}
	u, err := url.Parse(base)
	if err != nil || u == nil || u.Host == "" {
		// The executor will report malformed URLs at the normal validation
		// boundary. Do not rewrite an unparseable custom value here.
		return base
	}
	escapedPath := strings.TrimRight(u.EscapedPath(), "/")
	isCodexBase, isBackendRoot := codexBackendBasePathKinds(base)
	isAPIBase := isCodexAPIBasePath(base)
	if !isCodexBase && !isBackendRoot && !isAPIBase && !providertransport.IsCodexAPIKeyAuthType(account.Type) {
		return base
	}
	if isBackendRoot || isCodexBase {
		// `/backend-api` is already the root expected by `/wham` and `/ps`.
		// Versioned and family-specific aliases (for example
		// `/backend-api/codex/v1` or `/backend-api/wham`) are reduced to that
		// root while retaining any private reverse-proxy prefix and query.
		if _, escaped, ok := codexBasePathAliasForURL(base); ok {
			if rootPath, ok := codexBackendRootPath(escaped); ok {
				return codexURLWithEscapedPath(base, rootPath)
			}
		}
		return base
	}
	if isAPIBase {
		// API-style management routes are rooted at the host and carry their
		// own `/api/codex` prefix.  `/api/codex/v1` is an official alias in
		// recent CLI builds; strip the complete terminal alias, not just the
		// `/api/codex` portion.
		path := strings.TrimRight(u.EscapedPath(), "/")
		if alias, escaped, ok := codexBasePathAliasForURL(base); ok && alias.kind == codexBasePathAPICodex {
			path = strings.TrimRight(escaped[:len(escaped)-len(alias.suffix)], "/")
		} else if len(path) >= len("/api/codex") {
			path = strings.TrimRight(path[:len(path)-len("/api/codex")], "/")
		}
		if path == "" {
			path = "/"
		}
		return codexURLWithEscapedPath(base, path)
	}
	if !isCodexBase && providertransport.IsCodexAPIKeyAuthType(account.Type) {
		// Public API-key Files use the ordinary OpenAI `/v1/files` contract.
		// Unlike ChatGPT backend management routes, Files must retain the
		// configured `/v1` prefix; stripping it would incorrectly target the
		// host-root `/files` endpoint. Keep custom API-key prefixes untouched as
		// well, and only apply the host-root rewrite to the management family.
		if isCodexFilesEndpoint(endpoint) {
			return base
		}
		// Public API-key accounts commonly use `/v1` for model endpoints. The
		// backend-client management family is rooted at the host instead.
		path := strings.TrimRight(u.EscapedPath(), "/")
		if strings.HasSuffix(strings.ToLower(path), "/v1") {
			path = strings.TrimRight(path[:len(path)-len("/v1")], "/")
			if path == "" {
				path = "/"
			}
			decoded, err := url.PathUnescape(path)
			if err == nil {
				u.Path = decoded
				u.RawPath = path
				return u.String()
			}
		}
		return base
	}
	// Preserve the configured casing/escaping of the reverse-proxy prefix. URL
	// paths are technically case-sensitive, so rewriting an unusual but valid
	// `/BACKEND-API/CODEX` configuration to lowercase would be surprising.
	filesPath := strings.TrimRight(escapedPath[:len(escapedPath)-len("/codex")], "/")
	if filesPath == "" {
		return base
	}
	decodedPath, err := url.PathUnescape(filesPath)
	if err != nil {
		return base
	}
	u.Path = decodedPath
	u.RawPath = filesPath
	return u.String()
}

// codexTurnCostsBaseURLForAccount mirrors backend-client's
// Client::query_api_key_turn_costs URL construction. Hosted ChatGPT bases
// are moved to the analytics API origin and rooted at `/v1`; the public
// OpenAI API origin is normalized to the same `/v1` root. A custom provider
// is deliberately left untouched so reverse-proxy prefixes and provider
// routing query parameters retain their configured semantics.
func codexTurnCostsBaseURLForAccount(base string) string {
	base = strings.TrimSpace(base)
	// A fragment is never part of an HTTP request target.  Do not normalize it
	// away here: returning the original value lets the native executor's URL
	// boundary reject the configuration instead of silently changing a signed
	// or otherwise credential-bearing URL into a different endpoint.
	if strings.Contains(base, "#") {
		return base
	}
	u, err := url.Parse(base)
	if err != nil || u == nil || u.Host == "" || u.User != nil {
		// Let the normal native executor validation report malformed values; do
		// not manufacture a different URL while selecting the endpoint.
		return base
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	var analyticsHost string
	switch host {
	case "chatgpt.com", "chat.openai.com":
		analyticsHost = "api.chatgpt.com"
	case "chatgpt-staging.com":
		analyticsHost = "api.chatgpt-staging.com"
	case "api.openai.com":
		analyticsHost = "api.openai.com"
	default:
		// Custom providers use Provider::url_for_path, i.e. append the
		// canonical analytics suffix to the configured base URL.
		return base
	}

	// url.URL.Host includes a possible non-default port. Rust's Url::set_host
	// changes only the hostname, so preserve that port for local fixtures and
	// enterprise gateways that expose an official hostname on a custom port.
	if port := u.Port(); port != "" {
		u.Host = analyticsHost + ":" + port
	} else {
		u.Host = analyticsHost
	}
	u.Path = "/v1"
	u.RawPath = ""
	// The official turn-cost client sets the absolute analytics path and
	// explicitly clears query/fragment. Keeping these fields on a rewritten
	// base could leak stale tenant/query values or make the plugin reject the
	// URL before the request is sent.
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	return u.String()
}

func providerQuery(c *gin.Context) map[string][]string {
	if c == nil || c.Request == nil || c.Request.URL == nil {
		return nil
	}
	query := c.Request.URL.Query()
	if len(query) == 0 {
		return nil
	}
	out := make(map[string][]string, len(query))
	for key, values := range query {
		if providerQueryCredentialKey(key) {
			continue
		}
		out[key] = append([]string(nil), values...)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// providerQueryCredentialKey prevents inbound API-key/session material from
// being copied to a selected account's upstream URL.  Codex requires ordinary
// query parameters such as client_version; only authentication-shaped names
// are removed.
func providerQueryCredentialKey(key string) bool {
	lower := strings.ToLower(strings.TrimSpace(key))
	if lower == "" {
		return false
	}
	switch lower {
	case "key", "api_key", "apikey", "access_token", "refresh_token", "id_token", "session_token",
		"token", "authorization", "proxy_authorization", "password", "secret", "credential":
		return true
	}
	for _, marker := range []string{"api-key", "access-token", "refresh-token", "session-token", "api_key", "access_token", "refresh_token", "session_token", "secret", "credential", "password"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func providerWireTransport(stream bool) string {
	if stream {
		return "sse"
	}
	return "http"
}

func providerResultToCPA(in providertransport.Result) cpa.ForwardResult {
	buildErr := in.BuildErr
	netErr := in.NetErr
	if errors.Is(buildErr, providertransport.ErrCodexPluginUnavailable) ||
		errors.Is(buildErr, providertransport.ErrCodexPluginUnsupported) {
		// Plugin availability/capability is deployment state, not a malformed
		// client request. Preserve it as a retryable transport failure so the
		// account scheduler can try another eligible target and render its
		// existing 5xx exhausted-pool response instead of the BuildErr 400 path.
		if netErr == nil {
			netErr = buildErr
		}
		buildErr = nil
	}
	return cpa.ForwardResult{
		StatusCode: in.StatusCode, Headers: in.Headers, ResponseStarted: in.ResponseStarted, Body: in.Body, ContentType: in.ContentType,
		Usage: in.Usage, FirstTokenMs: in.FirstTokenMs, RequestFirstTokenMs: in.RequestFirstTokenMs,
		ExecutorBootstrapMs: in.ExecutorBootstrapMs, Written: in.Written, DataReceived: in.DataReceived, StreamErr: in.StreamErr,
		Done: in.Done, NetErr: netErr, BuildErr: buildErr, RefreshedCredentials: in.RefreshedCredentials,
	}
}
