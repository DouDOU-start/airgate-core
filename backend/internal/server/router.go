package server

import (
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/server/middleware"
	webfs "github.com/DouDOU-start/airgate-core/internal/web"
)

// registerCodexResponsesWebSocketFallbackRoutes explicitly handles the GET
// upgrade probe used by recent Codex CLI versions. The supplied handler is the
// real native WebSocket relay in production; when that capability is absent it
// returns 426, which lets the official client switch to HTTP Responses instead
// of falling through to the SPA NoRoute handler (which would return
// 200/text-html).
func registerCodexResponsesWebSocketFallbackRoutes(
	relayGroup gin.IRouter,
	noPrefixGroup gin.IRouter,
	codexGroup gin.IRouter,
	handler gin.HandlerFunc,
) {
	relayGroup.GET("/responses", handler)
	noPrefixGroup.GET("/responses", handler)
	codexGroup.GET("/v1/responses", handler)
	codexGroup.GET("/responses", handler)
}

// registerCodexGuardianUnsupportedRoutes reserves the internal Guardian
// endpoint names on every Codex-compatible public route shape. The official
// CLI can select these paths for its approval-review and risk-classifier
// agents; until AirGate implements those dedicated contracts, returning an
// authenticated JSON 501 is safer than allowing Gin's SPA NoRoute fallback
// to answer with 200/text-html or accidentally routing the request through
// ordinary Responses/CPA translation.
func registerCodexGuardianUnsupportedRoutes(
	relayGroup gin.IRouter,
	noPrefixGroup gin.IRouter,
	codexGroup gin.IRouter,
	handler gin.HandlerFunc,
) {
	for _, path := range []string{"/guardian", "/guardian-classifier"} {
		relayGroup.POST(path, handler)
		relayGroup.GET(path, handler)
		noPrefixGroup.POST(path, handler)
		noPrefixGroup.GET(path, handler)
		codexGroup.POST("/v1"+path, handler)
		codexGroup.GET("/v1"+path, handler)
		codexGroup.POST(path, handler)
		codexGroup.GET(path, handler)
	}
}

// registerCodexGuardianNativeRoutes exposes the official Codex Guardian
// Responses-compatible endpoints. POST is handled by the native Codex
// executor.  GET is a Responses WebSocket handshake for recent Codex CLI
// builds; callers that do not have a duplex executor can omit the optional
// handlers and retain the authenticated JSON 501 fallback.
func registerCodexGuardianNativeRoutes(
	relayGroup gin.IRouter,
	noPrefixGroup gin.IRouter,
	codexGroup gin.IRouter,
	guardian gin.HandlerFunc,
	classifier gin.HandlerFunc,
	unsupported gin.HandlerFunc,
	websocketHandlers ...gin.HandlerFunc,
) {
	guardianWebSocket := unsupported
	classifierWebSocket := unsupported
	if len(websocketHandlers) > 0 && websocketHandlers[0] != nil {
		guardianWebSocket = websocketHandlers[0]
	}
	if len(websocketHandlers) > 1 && websocketHandlers[1] != nil {
		classifierWebSocket = websocketHandlers[1]
	}
	registerGuardianPair := func(group gin.IRouter, prefix string) {
		group.POST(prefix+"/guardian", guardian)
		group.POST(prefix+"/guardian-classifier", classifier)
		group.GET(prefix+"/guardian", guardianWebSocket)
		group.GET(prefix+"/guardian-classifier", classifierWebSocket)
	}
	registerGuardianPair(relayGroup, "")
	registerGuardianPair(noPrefixGroup, "")
	registerGuardianPair(codexGroup, "/v1")
	registerGuardianPair(codexGroup, "")
}

func registerCodexNativeHTTPRoutes(
	relayGroup gin.IRouter,
	noPrefixGroup gin.IRouter,
	codexGroup gin.IRouter,
	realtimeCalls gin.HandlerFunc,
	realtimeLive gin.HandlerFunc,
	memories gin.HandlerFunc,
) {
	relayGroup.POST("/realtime/calls", realtimeCalls)
	relayGroup.POST("/live", realtimeLive)
	relayGroup.POST("/memories/trace_summarize", memories)
	noPrefixGroup.POST("/realtime/calls", realtimeCalls)
	noPrefixGroup.POST("/live", realtimeLive)
	noPrefixGroup.POST("/memories/trace_summarize", memories)
	codexGroup.POST("/v1/realtime/calls", realtimeCalls)
	codexGroup.POST("/v1/live", realtimeLive)
	codexGroup.POST("/v1/memories/trace_summarize", memories)
	codexGroup.POST("/realtime/calls", realtimeCalls)
	codexGroup.POST("/live", realtimeLive)
	codexGroup.POST("/memories/trace_summarize", memories)
}

func registerCodexRealtimeSidebandRoutes(
	relayGroup gin.IRouter,
	noPrefixGroup gin.IRouter,
	codexGroup gin.IRouter,
	handler gin.HandlerFunc,
) {
	relayGroup.GET("/realtime", handler)
	relayGroup.GET("/live/:call_id", handler)
	noPrefixGroup.GET("/realtime", handler)
	noPrefixGroup.GET("/live/:call_id", handler)
	codexGroup.GET("/v1/realtime", handler)
	codexGroup.GET("/v1/live/:call_id", handler)
	codexGroup.GET("/realtime", handler)
	codexGroup.GET("/live/:call_id", handler)
}

// registerCodexControlPlaneRoutes exposes the finite set of raw JSON POST
// contracts used by the official History/Notes extension and analytics queue.
// There is intentionally no wildcard route: unknown alpha/notes or analytics
// paths must not become an arbitrary account credentialed proxy.
func registerCodexControlPlaneRoutes(
	relayGroup gin.IRouter,
	noPrefixGroup gin.IRouter,
	codexGroup gin.IRouter,
	handler gin.HandlerFunc,
) {
	register := func(group gin.IRouter, prefix string) {
		registerCodexControlPlaneRoutesAt(group, prefix, handler)
	}
	register(relayGroup, "")
	register(noPrefixGroup, "")
	register(codexGroup, "/v1")
	register(codexGroup, "")
}

// registerCodexControlPlaneRoutesAt is the prefix-parametric form used by
// backend aliases that are not represented by the historical relay/codex
// groups (for example /backend-api/v1/wham). Keeping the finite path table in
// one place prevents one alias from silently accepting a different surface.
func registerCodexControlPlaneRoutesAt(group gin.IRouter, prefix string, handler gin.HandlerFunc) {
	if group == nil || handler == nil {
		return
	}
	paths := []string{
		"/alpha/history/v2/list_windows",
		"/alpha/history/v2/list_items",
		"/alpha/history/v2/read_item",
		"/alpha/history/v2/search_contents",
		"/alpha/notes/v2/list_files_by_prefix",
		"/alpha/notes/v2/read_file",
		"/alpha/notes/v2/search_contents",
		"/alpha/notes/v2/append_to_file",
		"/alpha/notes/v2/write_file",
		"/alpha/notes/v2/thread_hint",
		"/analytics-events/events",
		"/analytics/codex/turn-costs",
	}
	for _, path := range paths {
		group.POST(prefix+path, handler)
	}
}

// registerCodexFilesRoutes exposes the finite official Codex file lifecycle
// under every supported public base-url shape. The blob PUT itself is not
// registered here: it uses the unauthenticated opaque-token route below so
// the official client can send the signed-upload headers without copying its
// API key.
func registerCodexFilesRoutes(
	relayGroup gin.IRouter,
	noPrefixGroup gin.IRouter,
	codexGroup gin.IRouter,
	create gin.HandlerFunc,
	finalize gin.HandlerFunc,
) {
	register := func(group gin.IRouter, prefix string) {
		if group == nil {
			return
		}
		if create != nil {
			group.POST(prefix+"/files", create)
		}
		if finalize != nil {
			group.POST(prefix+"/files/:file_id/uploaded", finalize)
		}
	}
	register(relayGroup, "")
	register(noPrefixGroup, "")
	register(codexGroup, "/v1")
	register(codexGroup, "")
}

// registerCodexBackendClientRoutes exposes the finite management surface used
// by the official Codex backend-client. Registering all common verbs lets the
// shared handler return an authenticated JSON 405 for a known path instead of
// falling through to the SPA NoRoute handler; the handler still enforces the
// exact method for each operation.
func registerCodexBackendClientRoutes(group gin.IRouter, prefix string, handler gin.HandlerFunc) {
	registerCodexBackendClientRoutesExcept(group, prefix, handler, nil)
}

// registerCodexCuratedPluginsExportRoute exposes the exact public startup-sync
// URL used by the official Codex CLI. It must remain outside APIKeyAuth: the
// official client deliberately sends only Originator/User-Agent here while it
// bootstraps a local curated-plugin snapshot. The handler itself owns the
// fixed-upstream and response-size/security checks.
func registerCodexCuratedPluginsExportRoute(router gin.IRouter, handlers ...gin.HandlerFunc) {
	if router == nil || len(handlers) == 0 {
		return
	}
	for _, handler := range handlers {
		if handler == nil {
			return
		}
	}
	// Any reserves the known path for every method so malformed POST/PUT/etc.
	// requests receive the handler's structured 405 rather than the SPA
	// fallback. The handler forwards only GET.
	router.Any("/backend-api/plugins/export/curated", handlers...)
}

// registerCodexBackendClientRoutesExcept is used when a compatibility route
// already owns one exact method/path pair on an otherwise Codex-compatible
// base.  The important case is GET /v1/usage: AirGate has long exposed that
// endpoint as the cc-switch balance probe, so registering the Codex backend
// usage contract on top of it would both change existing behavior and make
// Gin panic while the server is starting.  Other verbs for the same known path
// remain reserved by the backend-client handler and return its structured 405.
func registerCodexBackendClientRoutesExcept(
	group gin.IRouter,
	prefix string,
	handler gin.HandlerFunc,
	skip func(method, path string) bool,
) {
	if group == nil || handler == nil {
		return
	}
	paths := []string{
		"/usage",
		"/usage/thread_usage/query",
		"/rate-limit-reset-credits",
		"/rate-limit-reset-credits/consume",
		"/accounts/check",
		"/accounts/send_add_credits_nudge_email",
		"/profiles/me",
		"/config/bundle",
		"/settings/user",
		"/tasks",
		"/tasks/list",
		"/tasks/:task_id",
		"/tasks/:task_id/turns/:turn_id/sibling_turns",
		// Cloud Tasks environment discovery. The by-repo route deliberately
		// registers both the upstream three-segment form and its optional ref
		// extension; the handler applies the stricter finite shape/encoding
		// validation before forwarding.
		"/environments",
		"/environments/by-repo/:provider/:owner/:repo",
		"/environments/by-repo/:provider/:owner/:repo/:ref",
		"/workspace-messages",
		"/ps/mcp",
		// Remote plugin catalog/mutation endpoints. Keep these in the same
		// finite registration table as the other backend-client contracts so an
		// unknown /ps/plugins path cannot become a credentialed catch-all proxy.
		"/ps/plugins/list",
		"/ps/plugins/search",
		"/ps/plugins/suggested/codex",
		"/ps/plugins/installed",
		"/ps/plugins/workspace/shared",
		"/ps/plugins/workspace/created",
		"/ps/plugins/:plugin_id",
		"/ps/plugins/:plugin_id/skills/:skill_name",
		"/ps/plugins/:plugin_id/install",
		"/ps/plugins/:plugin_id/uninstall",
		"/ps/plugins/:plugin_id/shares",
		// Apps/Connectors directory and metadata endpoints used by the official
		// ChatGPT/Codex client. These are finite paths; arbitrary /connectors or
		// /ps/apps requests must still fall through to the JSON 404 boundary.
		"/connectors/directory/list",
		"/connectors/directory/list_workspace",
		"/ps/apps/batch",
		// Legacy featured-plugin discovery and mutations.
		"/plugins/featured",
		"/plugins/:plugin_id/enable",
		"/plugins/:plugin_id/uninstall",
		// Workspace plugin sharing/public upload lifecycle.
		"/public/plugins/workspace/upload-url",
		"/public/plugins/workspace",
		"/public/plugins/workspace/:remote_plugin_id",
	}
	methods := []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions}
	for _, path := range paths {
		for _, method := range methods {
			if skip != nil && skip(method, prefix+path) {
				continue
			}
			group.Handle(method, prefix+path, handler)
		}
	}
}

// registerCodexRemoteControlRoutes exposes the finite HTTP portion of the
// official app-server Remote Control contract. The websocket server endpoint
// is accepted as an optional handler so a native duplex implementation can be
// installed without changing the route table; until then the shared HTTP
// handler returns a structured method/capability error.
func registerCodexRemoteControlRoutes(group gin.IRouter, prefix string, handler gin.HandlerFunc, websocketHandler ...gin.HandlerFunc) {
	if group == nil || handler == nil {
		return
	}
	registerAllMethods := func(path string) {
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions} {
			group.Handle(method, prefix+path, handler)
		}
	}
	for _, path := range []string{
		"/remote/control/server/enroll",
		"/remote/control/server/refresh",
		"/remote/control/server/pair",
		"/remote/control/server/pair/status",
		"/remote/control/environments/:environment_id/clients",
		"/remote/control/environments/:environment_id/clients/:client_id",
	} {
		registerAllMethods(path)
	}
	serverHandler := handler
	if len(websocketHandler) > 0 && websocketHandler[0] != nil {
		serverHandler = websocketHandler[0]
	}
	// Reserve the websocket path with the supplied duplex handler when one is
	// available. Other verbs still go through the HTTP handler so known-path
	// method errors remain JSON rather than Gin's SPA fallback.
	group.GET(prefix+"/remote/control/server", serverHandler)
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions} {
		group.Handle(method, prefix+"/remote/control/server", handler)
	}
}

// registerCodexAgentIdentityJWKSUnsupportedRoutes reserves the exact JWKS
// paths emitted by the official Codex auth client. Agent Identity is a
// separate Ed25519/AgentAssertion trust flow and is intentionally not treated
// as an ordinary bearer/API-key proxy; the supplied handler returns a
// structured 501 until that flow has a dedicated Core contract. Keeping the
// aliases explicit also prevents a broad /agent-identities wildcard from
// becoming a credentialed or cacheable catch-all endpoint.
var codexAgentIdentityJWKSPaths = [...]string{
	// Public/rooted Codex API bases (for example a deployment configured with
	// `https://gateway.example/v1` or a host-root base). The official helper
	// appends `/agent-identities/jwks` whenever the base does not contain
	// `/backend-api`.
	"/agent-identities/jwks",
	"/v1/agent-identities/jwks",
	"/codex/agent-identities/jwks",
	"/codex/v1/agent-identities/jwks",
	"/wham/agent-identities/jwks",
	"/wham/v1/agent-identities/jwks",
	// Official ChatGPT production/staging base rooted at /backend-api.
	"/backend-api/wham/agent-identities/jwks",
	"/backend-api/v1/wham/agent-identities/jwks",
	// Be liberal about complete /backend-api/codex aliases accepted by
	// current launchers; agent_identity_jwks_url appends /wham when the
	// base contains /backend-api.
	"/backend-api/codex/wham/agent-identities/jwks",
	"/backend-api/codex/v1/wham/agent-identities/jwks",
	// Public/custom Codex base rooted at /api/codex.
	"/api/codex/agent-identities/jwks",
	"/api/codex/v1/agent-identities/jwks",
}

func registerCodexAgentIdentityJWKSUnsupportedRoutes(group gin.IRouter, handler gin.HandlerFunc) {
	if group == nil || handler == nil {
		return
	}
	for _, path := range codexAgentIdentityJWKSPaths {
		// Register the complete standard method set so a known trust-boundary
		// path always reaches the structured method/capability handler. In
		// particular, POST/CONNECT/TRACE must not depend on the SPA NoRoute
		// fallback to synthesize a 405.
		group.Any(path, handler)
	}
}

// registerCodexAgentIdentityJWKSRoutes registers the public Agent Identity
// JWKS aliases with the supplied middleware/handler chain.  The official
// Codex client performs this discovery before it has an AirGate API-key
// context, so callers should pass only the dedicated rate limiter and fixed
// upstream proxy (never APIKeyAuth or account selection middleware).
func registerCodexAgentIdentityJWKSRoutes(group gin.IRouter, handlers ...gin.HandlerFunc) {
	if group == nil || len(handlers) == 0 {
		return
	}
	methodHandler := handlers[len(handlers)-1]
	if methodHandler == nil {
		return
	}
	for _, path := range codexAgentIdentityJWKSPaths {
		// Only valid GET discovery consumes the public endpoint's rate-limit
		// budget. Invalid methods go straight to the terminal handler, which
		// returns the structured 405 without allowing cheap quota exhaustion.
		group.GET(path, handlers...)
		for _, method := range []string{
			http.MethodHead,
			http.MethodPost,
			http.MethodPut,
			http.MethodPatch,
			http.MethodDelete,
			http.MethodOptions,
			http.MethodConnect,
			http.MethodTrace,
		} {
			group.Handle(method, path, methodHandler)
		}
	}
}

func isCodexAgentIdentityJWKSPath(path string) bool {
	path = strings.TrimRight(strings.TrimSpace(path), "/")
	if path == "" {
		return false
	}
	for _, candidate := range codexAgentIdentityJWKSPaths {
		if path == candidate {
			return true
		}
	}
	return false
}

// isCodexFilesPath reports whether a request is aimed at one of the finite
// Codex Files route families. Gin's global NoRoute handler serves the SPA for
// browser deep-links, but doing that for a misspelled Files endpoint would
// turn an API error into a 200/text-html response.
func isCodexFilesPath(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	for _, prefix := range []string{
		"", "/v1", "/codex", "/codex/v1",
		"/backend-api", "/backend-api/v1", "/backend-api/codex", "/backend-api/codex/v1",
		"/api/codex", "/api/codex/v1",
	} {
		base := prefix + "/files"
		if path == base || strings.HasPrefix(path, base+"/") {
			return true
		}
	}
	return false
}

func isCodexBackendClientPath(path string) bool {
	path = strings.TrimRight(strings.TrimSpace(path), "/")
	if path == "" {
		return false
	}
	for _, prefix := range []string{
		"", "/v1", "/codex", "/codex/v1",
		"/backend-api/v1/wham", "/backend-api/wham/v1",
		"/backend-api/wham", "/backend-api/codex/v1", "/backend-api/codex",
		"/backend-api/v1", "/backend-api", "/api/codex/v1", "/api/codex",
		"/codex/v1", "/codex", "/wham/v1", "/wham",
	} {
		if path == prefix || !strings.HasPrefix(path, prefix+"/") {
			continue
		}
		rest := strings.TrimPrefix(path, prefix+"/")
		for _, root := range []string{
			"usage", "rate-limit-reset-credits", "accounts", "profiles", "config",
			"settings", "tasks", "environments", "workspace-messages", "ps",
			"connectors", "plugins", "public",
		} {
			if rest == root || strings.HasPrefix(rest, root+"/") {
				return true
			}
		}
	}
	return strings.HasPrefix(path, "/backend-api/ps/") || path == "/backend-api/ps"
}

func isCodexRemoteControlPath(path string) bool {
	path = strings.TrimRight(strings.TrimSpace(path), "/")
	if path == "" {
		return false
	}
	for _, prefix := range []string{
		"", "/v1", "/codex", "/codex/v1", "/api/codex", "/api/codex/v1",
		"/backend-api", "/backend-api/v1", "/backend-api/v1/wham", "/backend-api/codex", "/backend-api/codex/v1",
		"/backend-api/wham", "/backend-api/wham/v1", "/wham", "/wham/v1",
	} {
		base := prefix + "/remote/control"
		if path == base || strings.HasPrefix(path, base+"/") {
			return true
		}
	}
	return false
}

// codexBackendAliasHandlers contains the handlers shared by the ChatGPT
// backend-compatible /backend-api/codex route family. The official Codex CLI
// uses this prefix for OAuth accounts, while API-key callers commonly use
// /v1, /codex, or an unprefixed public route. Keep the aliases pointed at the
// exact same handlers so authentication, scheduling, translation, and native
// framing semantics cannot drift between URL shapes.
type codexBackendAliasHandlers struct {
	responses                   gin.HandlerFunc
	responsesWebSocket          gin.HandlerFunc
	compact                     gin.HandlerFunc
	alphaSearch                 gin.HandlerFunc
	imagesGenerations           gin.HandlerFunc
	imagesEdits                 gin.HandlerFunc
	models                      gin.HandlerFunc
	realtimeCalls               gin.HandlerFunc
	realtimeLive                gin.HandlerFunc
	memories                    gin.HandlerFunc
	realtimeSidebandWebSocket   gin.HandlerFunc
	guardian                    gin.HandlerFunc
	guardianClassifier          gin.HandlerFunc
	guardianWebSocket           gin.HandlerFunc
	guardianClassifierWebSocket gin.HandlerFunc
	controlPlane                gin.HandlerFunc
	filesCreate                 gin.HandlerFunc
	filesFinalize               gin.HandlerFunc
}

// registerCodexBackendAliasRoutes exposes both /backend-api/codex/<endpoint>
// and /backend-api/codex/v1/<endpoint>. It intentionally registers the full
// native Codex surface (including WebSocket GET upgrades), not only the
// Responses POST route, because recent official clients use the backend path
// for Realtime and Guardian control-plane traffic as well.
func registerCodexBackendAliasRoutes(group gin.IRouter, handlers codexBackendAliasHandlers) {
	if group == nil {
		return
	}
	for _, prefix := range []string{"", "/v1"} {
		if handlers.responses != nil {
			group.POST(prefix+"/responses", handlers.responses)
		}
		if handlers.responsesWebSocket != nil {
			group.GET(prefix+"/responses", handlers.responsesWebSocket)
		}
		if handlers.compact != nil {
			group.POST(prefix+"/responses/compact", handlers.compact)
		}
		if handlers.alphaSearch != nil {
			group.POST(prefix+"/alpha/search", handlers.alphaSearch)
		}
		if handlers.imagesGenerations != nil {
			group.POST(prefix+"/images/generations", handlers.imagesGenerations)
		}
		if handlers.imagesEdits != nil {
			group.POST(prefix+"/images/edits", handlers.imagesEdits)
		}
		if handlers.models != nil {
			group.GET(prefix+"/models", handlers.models)
		}
		if handlers.realtimeCalls != nil {
			group.POST(prefix+"/realtime/calls", handlers.realtimeCalls)
		}
		if handlers.realtimeLive != nil {
			group.POST(prefix+"/live", handlers.realtimeLive)
		}
		if handlers.memories != nil {
			group.POST(prefix+"/memories/trace_summarize", handlers.memories)
		}
		if handlers.realtimeSidebandWebSocket != nil {
			group.GET(prefix+"/realtime", handlers.realtimeSidebandWebSocket)
			group.GET(prefix+"/live/:call_id", handlers.realtimeSidebandWebSocket)
		}
		if handlers.guardian != nil {
			group.POST(prefix+"/guardian", handlers.guardian)
		}
		if handlers.guardianClassifier != nil {
			group.POST(prefix+"/guardian-classifier", handlers.guardianClassifier)
		}
		if handlers.guardianWebSocket != nil {
			group.GET(prefix+"/guardian", handlers.guardianWebSocket)
		}
		if handlers.guardianClassifierWebSocket != nil {
			group.GET(prefix+"/guardian-classifier", handlers.guardianClassifierWebSocket)
		}
		if handlers.controlPlane != nil {
			for _, path := range []string{
				"/alpha/history/v2/list_windows",
				"/alpha/history/v2/list_items",
				"/alpha/history/v2/read_item",
				"/alpha/history/v2/search_contents",
				"/alpha/notes/v2/list_files_by_prefix",
				"/alpha/notes/v2/read_file",
				"/alpha/notes/v2/search_contents",
				"/alpha/notes/v2/append_to_file",
				"/alpha/notes/v2/write_file",
				"/alpha/notes/v2/thread_hint",
				"/analytics-events/events",
				"/analytics/codex/turn-costs",
			} {
				group.POST(prefix+path, handlers.controlPlane)
			}
		}
		if handlers.filesCreate != nil {
			group.POST(prefix+"/files", handlers.filesCreate)
		}
		if handlers.filesFinalize != nil {
			group.POST(prefix+"/files/:file_id/uploaded", handlers.filesFinalize)
		}
	}
}

// registerRoutes wires the public API and compatibility route families.
func (s *Server) registerRoutes() {
	r := s.engine
	handlers := s.handlers

	// 全局中间件：CORS → Recovery → RequestLogger → 业务
	r.Use(middleware.CORS(middleware.CORSConfig{
		// 默认不设置 AllowOrigins，仅同源可访问。
		// 如需跨域请配置具体来源，例如：AllowOrigins: []string{"https://example.com"}
	}))
	r.Use(middleware.Recovery())
	r.Use(middleware.RequestLogger())

	// 健康检查（无需认证，供 docker / k8s healthcheck 使用）
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	// Official Codex startup sync fetches curated-plugin export metadata without
	// an AirGate/API key. Keep this exact endpoint outside every APIKeyAuth
	// group, and protect the fixed public fetch with a modest per-IP limiter.
	if s.curatedPluginsProxy == nil {
		s.curatedPluginsProxy = newCodexCuratedPluginsExportProxy()
	}
	curatedPluginsRL := middleware.NewIPRateLimit(60)
	s.curatedPluginsRateLimiter = curatedPluginsRL.Limiter
	registerCodexCuratedPluginsExportRoute(r, curatedPluginsRL.Handler, s.curatedPluginsProxy.Handle)

	// API v1 路由组
	v1 := r.Group("/api/v1")

	// 插件内部账号接口只接受 Core 同机插件进程访问。认证使用 Core 启动时生成、
	// 仅驻留内存的随机令牌，不复用管理员 API Key。
	pluginAccountGroup := v1.Group("/internal/plugins")
	pluginAccountGroup.Use(middleware.PluginTokenAuth(s.pluginAccessToken))
	{
		pluginAccountGroup.GET("/accounts", handlers.Account.ListAccounts)
		pluginAccountGroup.POST("/accounts", handlers.Account.CreateAccount)
		pluginAccountGroup.PUT("/accounts/:id", handlers.Account.UpdateAccount)
	}

	// === 公共路由（无需认证） ===
	v1.GET("/settings/public", handlers.Settings.GetPublicSettings)

	// 模型广场（未登录可见的模型价格 + 倍率区间）：IP 限流防刷。
	modelMarketRL := middleware.NewIPRateLimit(60)
	s.modelMarketRateLimiter = modelMarketRL.Limiter
	v1.GET("/model-market", modelMarketRL.Handler, handlers.ModelPrice.PublicListModelMarket)

	// === 认证路由（无需 JWT） ===
	//
	// 基于客户端 IP 的速率限制（10 req/min），防止暴力破解和验证码滥用。
	// 替代原来依赖 CtxKeyUserID 的用户级限流（登录前无 user_id，实际空转已移除）。
	authGroup := v1.Group("/auth")
	ipRL := middleware.NewIPRateLimit(10)
	s.ipRateLimiter = ipRL.Limiter
	authGroup.Use(ipRL.Handler)
	{
		authGroup.POST("/login", handlers.Auth.Login)
		authGroup.POST("/login-apikey", handlers.Auth.LoginByAPIKey)
		authGroup.POST("/register", handlers.Auth.Register)
		authGroup.POST("/send-verify-code", handlers.Auth.SendVerifyCode)
		authGroup.POST("/verify-code", handlers.Auth.VerifyCode)
	}

	// Token 刷新（独立于 JWT 中间件，允许过期 token 刷新）
	v1.POST("/auth/refresh", handlers.Auth.RefreshToken)

	// === 用户路由（需要 JWT 认证） ===
	userGroup := v1.Group("")
	userGroup.Use(middleware.JWTAuth(s.jwtMgr))
	{
		accountGroup := userGroup.Group("")
		accountGroup.Use(middleware.RequireRoles("admin", "user"))

		// 用户资料
		userGroup.GET("/users/me", handlers.User.GetMe)
		accountGroup.PUT("/users/me", handlers.User.UpdateProfile)
		accountGroup.POST("/users/me/password", handlers.User.ChangePassword)
		accountGroup.PUT("/users/me/balance-alert", handlers.User.UpdateBalanceAlert)
		accountGroup.GET("/users/me/balance-history", handlers.User.GetMyBalanceHistory)

		// API Key 管理
		accountGroup.GET("/api-keys", handlers.APIKey.ListKeys)
		accountGroup.POST("/api-keys", handlers.APIKey.CreateKey)
		accountGroup.PUT("/api-keys/:id", handlers.APIKey.UpdateKey)
		accountGroup.DELETE("/api-keys/:id", handlers.APIKey.DeleteKey)
		accountGroup.GET("/api-keys/:id/reveal", handlers.APIKey.RevealKey)

		// 分组
		accountGroup.GET("/groups", handlers.Group.ListAvailableGroups)

		// 渠道状态（仅当前用户可访问且 status_visible=true 的分组，脱敏只读）
		accountGroup.GET("/channel-status/overview", handlers.Healthmon.UserOverview)
		accountGroup.GET("/channel-status/groups", handlers.Healthmon.UserGroups)

		// 公告（用户端：查看 + 标已读）
		accountGroup.GET("/announcements", handlers.Announcement.ListMyAnnouncements)
		accountGroup.POST("/announcements/read-all", handlers.Announcement.MarkAllAnnouncementsRead)
		accountGroup.POST("/announcements/:id/read", handlers.Announcement.MarkAnnouncementRead)

		// 使用记录
		userGroup.GET("/usage", handlers.Usage.UserUsage)
		userGroup.GET("/usage/stats", handlers.Usage.UserUsageStats)
		userGroup.GET("/usage/trend", handlers.Usage.UserUsageTrend)
		// 用户视角失败请求（脱敏：不含渠道/重试链/IP/UA）
		userGroup.GET("/usage/upstream-logs", handlers.UpstreamLog.UserList)

		// 在线充值（仅真实用户会话；API Key 登录的 end customer 不可充值）
		accountGroup.GET("/payment/methods", handlers.Payment.ListMethods)
		accountGroup.POST("/payment/orders", handlers.Payment.CreateOrder)
		accountGroup.GET("/payment/orders", handlers.Payment.ListUserOrders)
		accountGroup.GET("/payment/orders/:out_trade_no", handlers.Payment.GetUserOrder)

		// 兑换码充值（同充值：仅真实用户会话）
		accountGroup.POST("/redeem", handlers.Redemption.Redeem)

		// 邀请返利（用户端）
		accountGroup.GET("/invite/me", handlers.Invite.GetMe)
		accountGroup.GET("/invite/invitees", handlers.Invite.ListMyInvitees)
		accountGroup.GET("/invite/logs", handlers.Invite.ListMyRebateLogs)
		accountGroup.POST("/invite/transfer", handlers.Invite.Transfer)

		// OAuth 应用授权（仅真实用户会话；SPA 授权页转发）+ 应用导航入口
		accountGroup.GET("/oauth/authorize-info", handlers.OAuth.GetAuthorizeInfo)
		accountGroup.POST("/oauth/authorize", handlers.OAuth.Authorize)
		accountGroup.GET("/apps", handlers.OAuth.ListApps)
	}

	// === 管理员路由（需要管理员 JWT + AdminOnly，支持 admin- 管理员 API Key） ===
	adminGroup := v1.Group("/admin")
	adminGroup.Use(middleware.JWTAuth(s.jwtMgr, s.db), middleware.AdminOnly())
	{
		// 用户管理
		adminGroup.GET("/users", handlers.User.ListUsers)
		adminGroup.POST("/users", handlers.User.CreateUser)
		adminGroup.PUT("/users/:id", handlers.User.UpdateUser)
		adminGroup.DELETE("/users/:id", handlers.User.DeleteUser)
		adminGroup.PATCH("/users/:id/toggle", handlers.User.ToggleUserStatus)
		adminGroup.POST("/users/:id/balance", handlers.User.AdjustBalance)
		adminGroup.GET("/users/:id/balance-history", handlers.User.GetUserBalanceHistory)
		adminGroup.GET("/users/:id/api-keys", handlers.User.AdminListUserKeys)

		// 分组管理
		adminGroup.GET("/groups", handlers.Group.ListGroups)
		adminGroup.POST("/groups", handlers.Group.CreateGroup)
		adminGroup.GET("/groups/:id", handlers.Group.GetGroup)
		adminGroup.PUT("/groups/:id", handlers.Group.UpdateGroup)
		adminGroup.DELETE("/groups/:id", handlers.Group.DeleteGroup)

		// 公告管理
		adminGroup.GET("/announcements", handlers.Announcement.ListAnnouncements)
		adminGroup.POST("/announcements", handlers.Announcement.CreateAnnouncement)
		adminGroup.GET("/announcements/:id", handlers.Announcement.GetAnnouncement)
		adminGroup.PUT("/announcements/:id", handlers.Announcement.UpdateAnnouncement)
		adminGroup.DELETE("/announcements/:id", handlers.Announcement.DeleteAnnouncement)

		// 用户等级管理（等级×分组倍率，批量分层定价）
		adminGroup.GET("/tiers", handlers.Tier.ListTiers)
		adminGroup.POST("/tiers", handlers.Tier.CreateTier)
		adminGroup.GET("/tiers/:id", handlers.Tier.GetTier)
		adminGroup.PUT("/tiers/:id", handlers.Tier.UpdateTier)
		adminGroup.DELETE("/tiers/:id", handlers.Tier.DeleteTier)

		// 分组专属倍率管理（reverse 视角：某个分组下哪些用户有专属倍率）
		adminGroup.GET("/groups/:id/rate-overrides", handlers.User.ListGroupRateOverrides)
		adminGroup.PUT("/groups/:id/rate-overrides/:userId", handlers.User.SetGroupRateOverride)
		adminGroup.DELETE("/groups/:id/rate-overrides/:userId", handlers.User.DeleteGroupRateOverride)

		// 专属分组用户管理：某个专属分组下开放了哪些用户访问
		adminGroup.GET("/groups/:id/allowed-users", handlers.Group.ListAllowedUsers)
		adminGroup.POST("/groups/:id/allowed-users/:userId", handlers.Group.GrantAllowedUser)
		adminGroup.DELETE("/groups/:id/allowed-users/:userId", handlers.Group.RevokeAllowedUser)

		// 分组渠道 key 绑定管理：绑定/解绑；查询用现成的 GET /channels/keys?group_id= 即可
		adminGroup.POST("/groups/:id/channel-keys/:keyId", handlers.Group.BindChannelKey)
		adminGroup.DELETE("/groups/:id/channel-keys/:keyId", handlers.Group.UnbindChannelKey)

		// API 密钥管理（管理员）
		adminGroup.GET("/api-keys", handlers.APIKey.AdminListKeys)
		adminGroup.PUT("/api-keys/:id", handlers.APIKey.AdminUpdateKey)

		// 渠道管理
		adminGroup.GET("/channels", handlers.Channel.ListChannels)
		// 密钥视图：跨渠道平铺分页（静态段，先于 /channels/:id 匹配）
		adminGroup.GET("/channels/keys", handlers.Channel.ListChannelKeys)
		// 渠道导出 / 导入（静态段，先于 /channels/:id 匹配）
		adminGroup.GET("/channels/export", handlers.Channel.ExportChannels)
		adminGroup.POST("/channels/import", handlers.Channel.ImportChannels)
		adminGroup.POST("/channels", handlers.Channel.CreateChannel)
		adminGroup.PUT("/channels/:id", handlers.Channel.UpdateChannel)
		adminGroup.DELETE("/channels/:id", handlers.Channel.DeleteChannel)
		// 渠道下新增一把 key
		adminGroup.POST("/channels/:id/keys", handlers.Channel.AddChannelKey)
		// 密钥端点级操作（:id 为 channel_key_id）：更新 / 删除 / 测试 / 拉模型 / 刷余额
		adminGroup.PUT("/channels/keys/:id", handlers.Channel.UpdateChannelKey)
		adminGroup.DELETE("/channels/keys/:id", handlers.Channel.DeleteChannelKey)
		adminGroup.POST("/channels/keys/:id/test", handlers.Channel.TestChannel)
		adminGroup.POST("/channels/keys/:id/fetch-models", handlers.Channel.FetchChannelModels)
		adminGroup.POST("/channels/keys/:id/balance", handlers.Channel.RefreshChannelBalance)
		adminGroup.POST("/channels/keys/:id/upstream-rate", handlers.Channel.RefreshUpstreamRate)
		// 预览拉取：key 未保存时按表单连接参数试拉模型（静态段，先于 :id 匹配）
		adminGroup.POST("/channels/fetch-models", handlers.Channel.FetchChannelModelsPreview)
		adminGroup.POST("/channels/bulk-update", handlers.Channel.BulkUpdateChannels)
		// 渠道近 N 分钟失败计数（errlog Redis 分钟桶，渠道页监控列）
		adminGroup.GET("/channels/failure-stats", handlers.UpstreamLog.ChannelFailureStats)

		// 模型价格
		adminGroup.GET("/model-prices", handlers.ModelPrice.ListModelPrices)
		adminGroup.GET("/model-prices/sync-candidates", handlers.ModelPrice.ListModelPriceSyncCandidates)
		adminGroup.POST("/model-prices/sync", handlers.ModelPrice.SyncModelPrices)
		adminGroup.POST("/model-prices/sync-selected", handlers.ModelPrice.SyncSelectedModelPrices)
		adminGroup.POST("/model-prices/bulk-update", handlers.ModelPrice.BulkUpdateModelPrices)
		adminGroup.POST("/model-prices", handlers.ModelPrice.CreateModelPrice)
		adminGroup.PUT("/model-prices/:id", handlers.ModelPrice.UpdateModelPrice)
		adminGroup.DELETE("/model-prices/:id", handlers.ModelPrice.DeleteModelPrice)

		// 模型标签（家族归类，归属模型管理）
		adminGroup.GET("/model-tags", handlers.ModelPrice.ListModelTags)
		adminGroup.POST("/model-tags", handlers.ModelPrice.CreateModelTag)
		adminGroup.PUT("/model-tags/:id", handlers.ModelPrice.UpdateModelTag)
		adminGroup.DELETE("/model-tags/:id", handlers.ModelPrice.DeleteModelTag)

		// 使用记录（管理员）
		adminGroup.GET("/usage", handlers.Usage.AdminUsage)
		adminGroup.GET("/usage/stats", handlers.Usage.AdminUsageStats)
		adminGroup.GET("/usage/trend", handlers.Usage.AdminUsageTrend)

		// 上游请求日志（失败留痕/渠道测试/拉模型，仅管理员）
		adminGroup.GET("/upstream-logs", handlers.UpstreamLog.AdminList)
		// 完整请求审计（解密后的 Header/Body 仅管理员可见）
		adminGroup.GET("/request-audits", handlers.RequestAudit.List)
		adminGroup.GET("/request-audits/stats", handlers.RequestAudit.Stats)
		adminGroup.DELETE("/request-audits", handlers.RequestAudit.Clear)
		adminGroup.GET("/request-audits/:id", handlers.RequestAudit.Get)

		// 健康监测（真实流量统计，只读观测面）
		adminGroup.GET("/health-monitor/overview", handlers.Healthmon.Overview)
		adminGroup.GET("/health-monitor/entities", handlers.Healthmon.Entities)

		// 风控中心（内容审核）：配置/状态/探活/日志/解封/命中哈希管理
		adminGroup.GET("/risk-control/config", handlers.RiskControl.GetConfig)
		adminGroup.PUT("/risk-control/config", handlers.RiskControl.UpdateConfig)
		adminGroup.GET("/risk-control/status", handlers.RiskControl.GetStatus)
		adminGroup.POST("/risk-control/api-keys/test", handlers.RiskControl.TestAPIKeys)
		adminGroup.GET("/risk-control/logs", handlers.RiskControl.ListLogs)
		adminGroup.DELETE("/risk-control/logs", handlers.RiskControl.ClearLogs)
		adminGroup.POST("/risk-control/users/:id/unban", handlers.RiskControl.UnbanUser)
		adminGroup.DELETE("/risk-control/hashes", handlers.RiskControl.DeleteHash)
		adminGroup.DELETE("/risk-control/hashes/all", handlers.RiskControl.ClearHashes)

		// 代理管理
		adminGroup.GET("/proxies", handlers.Proxy.ListProxies)
		adminGroup.POST("/proxies", handlers.Proxy.CreateProxy)
		adminGroup.PUT("/proxies/:id", handlers.Proxy.UpdateProxy)
		adminGroup.DELETE("/proxies/:id", handlers.Proxy.DeleteProxy)
		adminGroup.POST("/proxies/:id/test", handlers.Proxy.TestProxy)

		// 上游账号管理（静态段先于 :id）
		adminGroup.GET("/accounts", handlers.Account.ListAccounts)
		adminGroup.GET("/accounts/platforms", handlers.Account.ListAccountPlatforms)
		adminGroup.GET("/accounts/export", handlers.Account.ExportAccounts)
		adminGroup.POST("/accounts/import", handlers.Account.ImportAccounts)
		adminGroup.POST("/accounts/bulk-update", handlers.Account.BulkUpdateAccounts)
		adminGroup.POST("/accounts/bulk-delete", handlers.Account.BulkDeleteAccounts)
		adminGroup.GET("/accounts/credentials-schema/:platform", handlers.Account.GetCredentialsSchema)
		adminGroup.GET("/accounts/oauth/:platform/hints", handlers.Account.GetOAuthHints)
		adminGroup.POST("/accounts/oauth/:platform/start", handlers.Account.StartOAuth)
		adminGroup.GET("/accounts/oauth/sessions/:sessionId", handlers.Account.GetOAuthSession)
		adminGroup.POST("/accounts/oauth/sessions/:sessionId/complete", handlers.Account.CompleteOAuth)
		// OAuth 专用导入（浏览器授权走 oauth/:platform/start）
		adminGroup.POST("/accounts/oauth/codex/import-refresh", handlers.Account.ImportCodexRefresh)
		adminGroup.POST("/accounts/oauth/codex/import-access-token", handlers.Account.ImportCodexAccessToken)
		adminGroup.POST("/accounts/oauth/codex/import-session", handlers.Account.ImportCodexSession)
		adminGroup.POST("/accounts/oauth/antigravity/import-refresh", handlers.Account.ImportAntigravityRefresh)
		adminGroup.POST("/accounts", handlers.Account.CreateAccount)
		adminGroup.PUT("/accounts/:id", handlers.Account.UpdateAccount)
		adminGroup.DELETE("/accounts/:id", handlers.Account.DeleteAccount)
		adminGroup.PATCH("/accounts/:id/toggle", handlers.Account.ToggleScheduling)
		adminGroup.POST("/accounts/:id/usage/refresh", handlers.Account.RefreshAccountUsage)
		adminGroup.POST("/accounts/:id/usage/reset", handlers.Account.ConsumeAccountUsageReset)
		adminGroup.GET("/accounts/:id/stats", handlers.Account.GetAccountUsageStats)
		adminGroup.GET("/accounts/:id/models", handlers.Account.ListAccountTestModels)
		adminGroup.POST("/accounts/:id/test", handlers.Account.TestAccountConnection)

		// 备忘录
		adminGroup.GET("/bookmarks", handlers.Bookmark.ListBookmarks)
		adminGroup.POST("/bookmarks", handlers.Bookmark.CreateBookmark)
		adminGroup.PUT("/bookmarks/:id", handlers.Bookmark.UpdateBookmark)
		adminGroup.DELETE("/bookmarks/:id", handlers.Bookmark.DeleteBookmark)

		// 插件管理（文件系统安装 + 多实例独立进程运行，不接入业务数据库）
		adminGroup.GET("/plugins", s.pluginHandler.ListPlugins)
		adminGroup.POST("/plugins/upload", s.pluginHandler.UploadPlugin)
		adminGroup.POST("/plugins/install-url", s.pluginHandler.InstallPluginFromURL)
		adminGroup.POST("/plugins/:id/upload", s.pluginHandler.UpdatePlugin)
		adminGroup.GET("/plugins/:id/config", s.pluginHandler.GetPluginConfig)
		adminGroup.PUT("/plugins/:id/config", s.pluginHandler.UpdatePluginConfig)
		adminGroup.PATCH("/plugins/:id/enabled", s.pluginHandler.SetPluginEnabled)
		adminGroup.POST("/plugins/:id/reload", s.pluginHandler.ReloadPlugin)
		adminGroup.POST("/plugins/:id/actions/*action", s.pluginHandler.InvokePluginAction)
		adminGroup.DELETE("/plugins/:id", s.pluginHandler.UninstallPlugin)

		// 系统设置
		adminGroup.GET("/settings", handlers.Settings.GetSettings)
		adminGroup.PUT("/settings", handlers.Settings.UpdateSettings)
		adminGroup.POST("/settings/test-smtp", handlers.Settings.TestSMTP)
		adminGroup.POST("/settings/test-bark", handlers.Settings.TestBark)
		adminGroup.POST("/settings/upload", handlers.Settings.UploadFile)

		// 管理员 API Key
		adminGroup.GET("/settings/admin-api-key", handlers.Settings.GetAdminAPIKey)
		adminGroup.POST("/settings/admin-api-key", handlers.Settings.GenerateAdminAPIKey)
		adminGroup.DELETE("/settings/admin-api-key", handlers.Settings.DeleteAdminAPIKey)

		// OAuth 应用接入管理
		adminGroup.GET("/oauth-clients", handlers.OAuth.ListOAuthClients)
		adminGroup.POST("/oauth-clients", handlers.OAuth.CreateOAuthClient)
		adminGroup.PUT("/oauth-clients/:id", handlers.OAuth.UpdateOAuthClient)
		adminGroup.DELETE("/oauth-clients/:id", handlers.OAuth.DeleteOAuthClient)
		adminGroup.POST("/oauth-clients/:id/reset-secret", handlers.OAuth.ResetOAuthClientSecret)

		// 仪表盘（管理员）
		adminGroup.GET("/dashboard/stats", handlers.Dashboard.Stats)
		adminGroup.GET("/dashboard/trend", handlers.Dashboard.Trend)

		// core 版本信息（仅管理员可见，避免对外暴露版本指纹）
		adminGroup.GET("/version", handlers.Version.GetVersion)

		// 支付管理：订单总览 + 服务商实例配置（保存即热加载）
		adminGroup.GET("/payment/orders", handlers.Payment.AdminListOrders)
		adminGroup.GET("/payment/providers", handlers.Payment.AdminListProviders)
		adminGroup.POST("/payment/providers", handlers.Payment.AdminUpsertProvider)
		adminGroup.DELETE("/payment/providers/:id", handlers.Payment.AdminDeleteProvider)

		// 兑换码管理
		adminGroup.GET("/redemption-codes", handlers.Redemption.AdminListCodes)
		adminGroup.GET("/redemption-codes/stats", handlers.Redemption.AdminStats)
		adminGroup.POST("/redemption-codes", handlers.Redemption.AdminGenerateCodes)
		adminGroup.PATCH("/redemption-codes/:id/status", handlers.Redemption.AdminUpdateStatus)
		adminGroup.DELETE("/redemption-codes/:id", handlers.Redemption.AdminDeleteCode)

		// 邀请返利管理：专属比例覆盖 + 全量邀请关系/流水（总开关+全局比例走通用 /admin/settings?group=invite）
		adminGroup.GET("/invite/overrides", handlers.Invite.AdminListOverrides)
		adminGroup.PATCH("/invite/overrides/:userID", handlers.Invite.AdminSetOverride)
		adminGroup.GET("/invite/invitees", handlers.Invite.AdminListInvitees)
		adminGroup.GET("/invite/logs", handlers.Invite.AdminListRebateLogs)
	}

	// 加载嵌入的前端 SPA：所有静态资源通过 //go:embed 打进二进制
	distFS, err := webfs.FS()
	if err != nil {
		slog.Error("加载嵌入前端失败", "error", err)
		os.Exit(1)
	}
	indexHTML, _ := webfs.IndexHTML()
	ogIndex := newIndexHTMLRenderer(indexHTML, handlers.SettingsService)
	assetsFS, err := fs.Sub(distFS, "assets")
	if err != nil {
		slog.Error("嵌入前端缺少 assets 子目录", "error", err)
		os.Exit(1)
	}

	// === 对外网关路由（sk- API Key 鉴权，纯透传：入站端点按协议分树） ===
	// 显式静态注册（先于 NoRoute），错误体按入口协议出原生形态（errfmt 按 EntryProtocol 分发）。
	relayGroup := r.Group("/v1", middleware.APIKeyAuth(s.db))
	{
		// OpenAI 协议（openai_compatible 渠道）
		relayGroup.POST("/chat/completions", s.relay.HandleChatCompletions)
		relayGroup.POST("/responses", s.relay.HandleResponses)
		relayGroup.POST("/responses/compact", s.relay.HandleCompact)
		// codex CLI 内置联网搜索（openai 协议，POST 非流式，按次计费）
		relayGroup.POST("/alpha/search", s.relay.HandleAlphaSearch)
		relayGroup.POST("/images/generations", s.relay.HandleImagesGenerations)
		relayGroup.POST("/images/edits", s.relay.HandleImagesEdits)
		relayGroup.GET("/models", s.relay.HandleModels)
		// AirGate 计费信息端点：下游网关探测上游倍率用（级联场景）
		relayGroup.GET("/airgate/billing", s.handleBilling)
		// Anthropic 协议（anthropic 渠道）
		relayGroup.POST("/messages", s.relay.HandleMessages)
		relayGroup.POST("/messages/count_tokens", s.relay.HandleMessagesCountTokens)
		// OpenAI 视频任务（openai_video 渠道，Sora 形态；异步任务子系统 relay/task）
		relayGroup.POST("/videos", s.taskFlow.HandleVideoSubmit)
		relayGroup.POST("/videos/generations", s.taskFlow.HandleXAIVideoSubmit)
		relayGroup.GET("/videos/:task_id", s.taskFlow.HandleVideoGet)
		relayGroup.GET("/videos/:task_id/content", s.taskFlow.HandleVideoContent)
	}
	// 无 /v1 前缀别名：部分客户端（如 Codex）base_url 不带版本号，直接拼接
	// /chat/completions 等路径。与上面同协议、同 handler，纯路径别名，不新增业务逻辑。
	noPrefixGroup := r.Group("", middleware.APIKeyAuth(s.db))
	{
		noPrefixGroup.POST("/chat/completions", s.relay.HandleChatCompletions)
		noPrefixGroup.POST("/responses", s.relay.HandleResponses)
		noPrefixGroup.POST("/responses/compact", s.relay.HandleCompact)
		noPrefixGroup.POST("/alpha/search", s.relay.HandleAlphaSearch)
		noPrefixGroup.POST("/images/generations", s.relay.HandleImagesGenerations)
		noPrefixGroup.POST("/images/edits", s.relay.HandleImagesEdits)
		noPrefixGroup.GET("/models", s.relay.HandleModels)
		noPrefixGroup.POST("/videos", s.taskFlow.HandleVideoSubmit)
		noPrefixGroup.POST("/videos/generations", s.taskFlow.HandleXAIVideoSubmit)
		noPrefixGroup.GET("/videos/:task_id", s.taskFlow.HandleVideoGet)
		noPrefixGroup.GET("/videos/:task_id/content", s.taskFlow.HandleVideoContent)
	}
	// Bare `/v1` and host-root clients may use the backend-client management
	// surface directly; keep these aliases on the authenticated relay group.
	// GET /v1/usage is the pre-existing cc-switch balance probe registered on
	// the root router below.  Keep that exact compatibility contract and only
	// reserve the remaining backend-client paths on the /v1 group; attempting
	// to register a second GET /v1/usage makes Gin panic during startup.
	registerCodexBackendClientRoutesExcept(relayGroup, "", s.relay.HandleCodexBackendClient,
		func(method, path string) bool {
			return method == http.MethodGet && path == "/usage"
		})
	registerCodexBackendClientRoutes(noPrefixGroup, "", s.relay.HandleCodexBackendClient)
	// Codex CLI 专用模型目录：保持官方 {models:[ModelInfo...]} 契约，
	// 与兼容 OpenAI 客户端使用的 /v1/models 分离，避免响应形态互相影响。
	codexGroup := r.Group("/codex", middleware.APIKeyAuth(s.db))
	{
		codexGroup.POST("/v1/responses", s.relay.HandleResponses)
		codexGroup.POST("/v1/responses/compact", s.relay.HandleCompact)
		codexGroup.POST("/v1/alpha/search", s.relay.HandleAlphaSearch)
		codexGroup.GET("/v1/models", s.relay.HandleCodexModels)
		codexGroup.POST("/responses", s.relay.HandleResponses)
		codexGroup.POST("/responses/compact", s.relay.HandleCompact)
		codexGroup.POST("/alpha/search", s.relay.HandleAlphaSearch)
		codexGroup.GET("/models", s.relay.HandleCodexModels)
		registerCodexBackendClientRoutes(codexGroup, "/v1", s.relay.HandleCodexBackendClient)
		registerCodexBackendClientRoutes(codexGroup, "", s.relay.HandleCodexBackendClient)
	}
	// Suno 音乐任务（suno 渠道，Suno-API 社区协议；错误体 {"code":"fail",...}）
	// Official ChatGPT OAuth builds use /backend-api/codex as their base URL.
	// Register the same native and CPA-capable handlers under that prefix so a
	// caller can select either the historical /v1 shape or the backend's
	// unversioned shape without falling through to the SPA.
	backendCodexGroup := r.Group("/backend-api/codex", middleware.APIKeyAuth(s.db))
	registerCodexBackendAliasRoutes(backendCodexGroup, codexBackendAliasHandlers{
		responses:                   s.relay.HandleResponses,
		responsesWebSocket:          s.relay.HandleResponsesWebSocket,
		compact:                     s.relay.HandleCompact,
		alphaSearch:                 s.relay.HandleAlphaSearch,
		imagesGenerations:           s.relay.HandleImagesGenerations,
		imagesEdits:                 s.relay.HandleImagesEdits,
		models:                      s.relay.HandleCodexModels,
		realtimeCalls:               s.relay.HandleRealtimeCalls,
		realtimeLive:                s.relay.HandleRealtimeLive,
		memories:                    s.relay.HandleMemoriesTraceSummarize,
		realtimeSidebandWebSocket:   s.relay.HandleRealtimeSidebandWebSocket,
		guardian:                    s.relay.HandleCodexGuardian,
		guardianClassifier:          s.relay.HandleCodexGuardianClassifier,
		guardianWebSocket:           s.relay.HandleCodexGuardianWebSocket,
		guardianClassifierWebSocket: s.relay.HandleCodexGuardianClassifierWebSocket,
		controlPlane:                s.relay.HandleCodexControlPlane,
		filesCreate:                 s.relay.HandleCodexFilesCreate,
		filesFinalize:               s.relay.HandleCodexFilesFinalize,
	})
	registerCodexBackendClientRoutes(backendCodexGroup, "", s.relay.HandleCodexBackendClient)
	registerCodexBackendClientRoutes(backendCodexGroup, "/v1", s.relay.HandleCodexBackendClient)
	// The public Codex API provider uses `/api/codex` as its base URL. Keep the
	// same finite native surface as the ChatGPT backend alias so a custom
	// provider can switch between API-key and OAuth accounts without changing
	// the client-side endpoint paths. This is a route alias only; authentication,
	// account selection, native/CPA policy, and endpoint allowlists remain in the
	// shared handlers below.
	apiCodexGroup := r.Group("/api/codex", middleware.APIKeyAuth(s.db))
	registerCodexBackendAliasRoutes(apiCodexGroup, codexBackendAliasHandlers{
		responses:                   s.relay.HandleResponses,
		responsesWebSocket:          s.relay.HandleResponsesWebSocket,
		compact:                     s.relay.HandleCompact,
		alphaSearch:                 s.relay.HandleAlphaSearch,
		imagesGenerations:           s.relay.HandleImagesGenerations,
		imagesEdits:                 s.relay.HandleImagesEdits,
		models:                      s.relay.HandleCodexModels,
		realtimeCalls:               s.relay.HandleRealtimeCalls,
		realtimeLive:                s.relay.HandleRealtimeLive,
		memories:                    s.relay.HandleMemoriesTraceSummarize,
		realtimeSidebandWebSocket:   s.relay.HandleRealtimeSidebandWebSocket,
		guardian:                    s.relay.HandleCodexGuardian,
		guardianClassifier:          s.relay.HandleCodexGuardianClassifier,
		guardianWebSocket:           s.relay.HandleCodexGuardianWebSocket,
		guardianClassifierWebSocket: s.relay.HandleCodexGuardianClassifierWebSocket,
		controlPlane:                s.relay.HandleCodexControlPlane,
		filesCreate:                 s.relay.HandleCodexFilesCreate,
		filesFinalize:               s.relay.HandleCodexFilesFinalize,
	})
	registerCodexBackendClientRoutes(apiCodexGroup, "", s.relay.HandleCodexBackendClient)
	registerCodexBackendClientRoutes(apiCodexGroup, "/v1", s.relay.HandleCodexBackendClient)
	// ChatGPT OAuth backend-client calls use /backend-api/wham/... while the
	// Responses/files surface remains under /backend-api/codex or /backend-api.
	backendWhamGroup := r.Group("/backend-api/wham", middleware.APIKeyAuth(s.db))
	registerCodexBackendClientRoutes(backendWhamGroup, "", s.relay.HandleCodexBackendClient)
	registerCodexBackendClientRoutes(backendWhamGroup, "/v1", s.relay.HandleCodexBackendClient)
	registerCodexControlPlaneRoutesAt(backendWhamGroup, "", s.relay.HandleCodexControlPlane)
	registerCodexControlPlaneRoutesAt(backendWhamGroup, "/v1", s.relay.HandleCodexControlPlane)
	// A few official builds receive a base URL already rooted at `/wham` (or
	// append `/v1` themselves). Keep this sibling alias on the normal API-key
	// boundary; account selection still supplies the upstream OAuth/API lease.
	whamGroup := r.Group("/wham", middleware.APIKeyAuth(s.db))
	registerCodexBackendClientRoutes(whamGroup, "", s.relay.HandleCodexBackendClient)
	registerCodexBackendClientRoutes(whamGroup, "/v1", s.relay.HandleCodexBackendClient)
	registerCodexControlPlaneRoutesAt(whamGroup, "", s.relay.HandleCodexControlPlane)
	registerCodexControlPlaneRoutesAt(whamGroup, "/v1", s.relay.HandleCodexControlPlane)
	// Some official ChatGPT clients keep `/backend-api` as the base URL and
	// append `/files` directly. Register that root alias (and its versioned
	// spelling) with the same authenticated handlers.
	backendAPIGroup := r.Group("/backend-api", middleware.APIKeyAuth(s.db))
	registerCodexFilesRoutes(backendAPIGroup, nil, nil,
		s.relay.HandleCodexFilesCreate, s.relay.HandleCodexFilesFinalize)
	registerCodexControlPlaneRoutesAt(backendAPIGroup, "", s.relay.HandleCodexControlPlane)
	backendAPIV1Group := r.Group("/backend-api/v1", middleware.APIKeyAuth(s.db))
	registerCodexFilesRoutes(backendAPIV1Group, nil, nil,
		s.relay.HandleCodexFilesCreate, s.relay.HandleCodexFilesFinalize)
	registerCodexControlPlaneRoutesAt(backendAPIV1Group, "", s.relay.HandleCodexControlPlane)
	registerCodexControlPlaneRoutesAt(backendAPIV1Group, "/wham", s.relay.HandleCodexControlPlane)
	registerCodexBackendClientRoutes(backendAPIV1Group, "", s.relay.HandleCodexBackendClient)
	registerCodexBackendClientRoutes(backendAPIV1Group, "/wham", s.relay.HandleCodexBackendClient)
	// Hosted Apps MCP is rooted directly at /backend-api/ps/mcp (not /wham).
	registerCodexBackendClientRoutes(backendAPIGroup, "", s.relay.HandleCodexBackendClient)
	// The root alias above intentionally exposes the full finite table for
	// compatibility; only /ps/mcp can match this base in the handler's path
	// classifier, while other paths receive JSON 404.
	// Remote Control has its own authentication boundary.  In particular, the
	// official client puts the enrolled bearer in Authorization, which must not
	// be consumed by the ordinary APIKeyAuth middleware used by the surrounding
	// Codex aliases.  Register the same finite route set on sibling groups that
	// carry CodexRemoteControlAuth instead of stacking both auth middlewares.
	remoteControlAuth := middleware.CodexRemoteControlAuth(s.db, s.remoteControlTokens)
	registerRemoteControlAliases := func(base string, prefixes ...string) {
		group := r.Group(base, remoteControlAuth)
		for _, prefix := range prefixes {
			registerCodexRemoteControlRoutes(group, prefix, s.relay.HandleCodexRemoteControl, s.relay.HandleCodexRemoteControlWebSocket)
		}
	}
	registerRemoteControlAliases("/codex", "", "/v1")
	registerRemoteControlAliases("/backend-api/codex", "", "/v1")
	registerRemoteControlAliases("/api/codex", "", "/v1")
	registerRemoteControlAliases("/backend-api/wham", "", "/v1")
	registerRemoteControlAliases("/backend-api/v1/wham", "")
	// A custom Codex API base may already be rooted at `/wham` (or may append
	// `/v1` after that root).  The official backend client derives these paths
	// from a non-`/backend-api` base in the same way as its `/api/codex` style.
	// Keep the aliases on the dedicated Remote Control auth boundary so an
	// upstream server bearer is never consumed by the ordinary API-key group.
	registerRemoteControlAliases("/wham", "", "/v1")
	// The official CLI derives the Remote Control URLs from the configured
	// backend base.  A deployment may therefore expose the same contract from
	// a bare `/backend-api` or `/v1` base (without the `/codex`/`/wham`
	// suffix).  Keep these aliases on the dedicated Remote Control auth
	// middleware; registering them on the ordinary API-key groups would consume
	// the OAuth bearer as if it were an AirGate key.
	registerRemoteControlAliases("/backend-api", "", "/v1")
	registerRemoteControlAliases("/v1", "")
	// Agent Identity JWKS discovery is a public, unauthenticated GET made before
	// the CLI has an AirGate API-key/account context. Keep it on a fixed-host
	// proxy with its own IP limiter; it must never enter account selection or
	// become a caller-controlled upstream URL.
	if s.agentIdentityJWKSProxy == nil {
		s.agentIdentityJWKSProxy = newCodexAgentIdentityJWKSProxy()
	}
	agentIdentityJWKSRateLimit := middleware.NewIPRateLimit(60)
	s.agentIdentityJWKSRateLimiter = agentIdentityJWKSRateLimit.Limiter
	registerCodexAgentIdentityJWKSRoutes(r, agentIdentityJWKSRateLimit.Handler, s.agentIdentityJWKSProxy.Handle)
	registerCodexResponsesWebSocketFallbackRoutes(
		relayGroup,
		noPrefixGroup,
		codexGroup,
		s.relay.HandleResponsesWebSocket,
	)
	registerCodexNativeHTTPRoutes(
		relayGroup,
		noPrefixGroup,
		codexGroup,
		s.relay.HandleRealtimeCalls,
		s.relay.HandleRealtimeLive,
		s.relay.HandleMemoriesTraceSummarize,
	)
	registerCodexRealtimeSidebandRoutes(
		relayGroup,
		noPrefixGroup,
		codexGroup,
		s.relay.HandleRealtimeSidebandWebSocket,
	)
	registerCodexGuardianNativeRoutes(
		relayGroup,
		noPrefixGroup,
		codexGroup,
		s.relay.HandleCodexGuardian,
		s.relay.HandleCodexGuardianClassifier,
		s.relay.HandleCodexGuardianUnsupported,
		s.relay.HandleCodexGuardianWebSocket,
		s.relay.HandleCodexGuardianClassifierWebSocket,
	)
	registerCodexControlPlaneRoutes(
		relayGroup,
		noPrefixGroup,
		codexGroup,
		s.relay.HandleCodexControlPlane,
	)
	registerCodexFilesRoutes(
		relayGroup,
		noPrefixGroup,
		codexGroup,
		s.relay.HandleCodexFilesCreate,
		s.relay.HandleCodexFilesFinalize,
	)
	// The upload URL returned by Files create is a random bearer token. It must
	// remain outside APIKeyAuth because the official client intentionally sends
	// no AirGate key on the signed blob PUT.
	r.PUT("/_airgate/codex/files/upload/:token", s.relay.HandleCodexFileUpload)
	// Workspace plugin bundles use a separate opaque lease namespace. Keep the
	// PUT unauthenticated for the same reason as Files, while the lease itself
	// is bound to the API key/group/account that issued upload-url.
	r.PUT("/_airgate/codex/plugins/upload/:token", s.relay.HandleCodexPluginUpload)
	sunoGroup := r.Group("/suno", middleware.APIKeyAuth(s.db))
	{
		sunoGroup.POST("/submit/:action", s.taskFlow.HandleSunoSubmit)
		sunoGroup.POST("/fetch", s.taskFlow.HandleSunoFetch)
		sunoGroup.GET("/fetch/:task_id", s.taskFlow.HandleSunoFetchByID)
	}
	// Gemini 协议（gemini 渠道）：路径形如 /v1beta/models/{model}:generateContent，
	// ':' 在 gin 路由里只有段首才是参数语法、段中不是分隔符——用单参数段承载
	// "model:action"，handler 内自行解析（见 pipeline.HandleGenerateContent，
	// 动词含 generateContent / streamGenerateContent / predict / countTokens）。
	geminiGroup := r.Group("/v1beta", middleware.APIKeyAuth(s.db))
	{
		geminiGroup.POST("/models/:modelAction", s.relay.HandleGenerateContent)
		// Gemini 原生形态模型列表（{"models":[{"name":"models/<id>",...}]}）。
		geminiGroup.GET("/models", s.relay.HandleGeminiModels)
	}

	// === cc-switch 通用模板兼容端点（使用 sk-xxx API Key 自鉴权） ===
	// AirGate 安装脚本和 cc-switch 通用脚本可使用 /v1/usage 做 Key 校验和
	// 余额查询。该路径由 Core 直接处理，返回真实可用余额。
	// 实现见 cc_compat.go。端点自带轻量鉴权且逐次查库，挂 IP 限流防刷。
	// 限额取 300/min：NAT / 未透传 XFF 的反代场景下大量用户共享同一出口 IP，
	// 60/min 会被整体误伤为 429。
	ccUsageRL := middleware.NewIPRateLimit(300)
	s.ccUsageRateLimiter = ccUsageRL.Limiter
	r.GET("/v1/usage", ccUsageRL.Handler, s.handleCCCompatUserBalance)

	// === 支付平台异步回调（公开路由，验签在 provider 实现内完成） ===
	// 易支付系走 form/GET，微信 V3 / easypay 走 JSON body + header 签名。
	r.POST("/api/v1/payment/notify/:provider_id", handlers.Payment.HandleCallback)
	r.GET("/api/v1/payment/notify/:provider_id", handlers.Payment.HandleCallback)

	// OAuth 协议端点（公开，供外部应用后端调用，RFC 6749 原始响应形态）。
	// GET /oauth/authorize 是 SPA 路由（授权页），经 NoRoute 落到前端。
	// token 端点带 IP 限流，防 client_secret 爆破。
	oauthRL := middleware.NewIPRateLimit(60)
	s.oauthRateLimiter = oauthRL.Limiter
	r.POST("/oauth/token", oauthRL.Handler, handlers.OAuth.Token)
	r.GET("/oauth/userinfo", handlers.OAuth.UserInfo)
	r.POST("/oauth/provision-key", handlers.OAuth.ProvisionKey)
	r.GET("/oauth/wallet", handlers.OAuth.Wallet)
	r.GET("/oauth/wallet/history", handlers.OAuth.WalletHistory)
	r.POST("/oauth/wallet/debits", handlers.OAuth.DebitWallet)
	r.POST("/oauth/wallet/refunds", handlers.OAuth.RefundWallet)
	r.GET("/oauth/payment/methods", handlers.OAuth.PaymentMethods)
	r.POST("/oauth/payment/orders", handlers.OAuth.CreatePaymentOrder)
	r.GET("/oauth/payment/orders", handlers.OAuth.ListPaymentOrders)
	r.GET("/oauth/payment/orders/:out_trade_no", handlers.OAuth.GetPaymentOrder)

	// 上传文件静态服务（这部分仍然在磁盘上，因为是用户上传的运行时数据）
	//
	// ⚠️ 安全说明：此路径公开可访问，无需认证。上传的文件（如站点 logo）可能
	// 被嵌入外部链接中分享，因此保持公开。文件名使用 UUID 生成，不可枚举。
	// 如未来需要访问控制，应替换为带鉴权的路由组。
	// X-Content-Type-Options: nosniff——上传目录是用户可控内容，禁止浏览器
	// MIME 嗅探把文件当脚本/HTML 执行（配合上传侧的扩展名白名单纵深防御）。
	uploadsGroup := r.Group("/uploads", func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Next()
	})
	uploadsGroup.Static("", "data/uploads")

	// 静态文件服务（前端 SPA）
	r.StaticFS("/assets", http.FS(assetsFS))
	// og:image 等分享卡片用的根目录静态文件不在 assets/ 下，未显式注册会落进
	// NoRoute 兜底、被当成 index.html（text/html）吐回去——图片抓取方（微信等）
	// 拿到的不是真图，卡片配图会失效。这里显式暴露一个 embed.FS 的根文件。
	r.StaticFileFS("/og-cover.png", "og-cover.png", http.FS(distFS))

	// NoRoute: 未匹配路径回退前端 index.html。
	// P1 起对外网关路由（/v1/chat/completions 等）走显式注册，不再经 NoRoute 分发。
	r.NoRoute(func(c *gin.Context) {
		if c != nil && c.Request != nil && c.Request.URL != nil && isCodexAgentIdentityJWKSPath(c.Request.URL.Path) {
			s.relay.HandleCodexAgentIdentityJWKSUnsupported(c)
			return
		}
		if c != nil && c.Request != nil && c.Request.URL != nil && isCodexFilesPath(c.Request.URL.Path) {
			c.JSON(http.StatusNotFound, gin.H{
				"error": gin.H{
					"type":    "invalid_request_error",
					"code":    "not_found",
					"message": "Codex Files endpoint not found",
				},
			})
			return
		}
		if c != nil && c.Request != nil && c.Request.URL != nil && isCodexRemoteControlPath(c.Request.URL.Path) {
			// Keep unknown Remote Control paths in the API error domain.  The
			// global NoRoute handler normally serves index.html for browser
			// deep-links, but a misspelled native endpoint must never look like a
			// successful HTML response to the official Codex client.
			c.JSON(http.StatusNotFound, gin.H{
				"error": gin.H{
					"type":    "invalid_request_error",
					"code":    "unsupported_endpoint",
					"message": "Codex remote-control endpoint not found",
				},
			})
			return
		}
		if c != nil && c.Request != nil && c.Request.URL != nil && isCodexBackendClientPath(c.Request.URL.Path) {
			c.JSON(http.StatusNotFound, gin.H{
				"error": gin.H{
					"type":    "invalid_request_error",
					"code":    "unsupported_endpoint",
					"message": "Codex backend-client endpoint not found",
				},
			})
			return
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", ogIndex.Bytes(c.Request.Context()))
	})
}
