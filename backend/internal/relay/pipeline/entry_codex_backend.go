package pipeline

import (
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/relay/accountreg"
	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
	"github.com/DouDOU-start/airgate-core/internal/relay/dto"
	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
)

// codexBackendClientRoute is the finite allowlist for the official
// backend-client/app-server management surface. ProviderPath is a canonical
// suffix; provider_transport.go adds the account-specific `/api/codex`,
// `/wham`, or `/ps` prefix after account selection.
type codexBackendClientRoute struct {
	Endpoint string
	Method   string
	Path     string
	// OAuthOnly marks ChatGPT backend contracts that reject API-key auth.
	// Keeping this on the route spec lets the shared handler apply the account
	// family guard without exposing a caller-controlled mode switch.
	OAuthOnly bool
}

var codexBackendClientStaticRoutes = []codexBackendClientRoute{
	{Endpoint: adaptor.EndpointCodexUsage, Method: http.MethodGet, Path: "/usage"},
	{Endpoint: adaptor.EndpointCodexThreadUsage, Method: http.MethodPost, Path: "/usage/thread_usage/query"},
	{Endpoint: adaptor.EndpointCodexRateLimitResetCredits, Method: http.MethodGet, Path: "/rate-limit-reset-credits"},
	{Endpoint: adaptor.EndpointCodexRateLimitResetCreditsConsume, Method: http.MethodPost, Path: "/rate-limit-reset-credits/consume"},
	{Endpoint: adaptor.EndpointCodexAccountsCheck, Method: http.MethodGet, Path: "/accounts/check"},
	{Endpoint: adaptor.EndpointCodexAccountsNudge, Method: http.MethodPost, Path: "/accounts/send_add_credits_nudge_email"},
	{Endpoint: adaptor.EndpointCodexProfilesMe, Method: http.MethodGet, Path: "/profiles/me"},
	{Endpoint: adaptor.EndpointCodexConfigBundle, Method: http.MethodGet, Path: "/config/bundle"},
	{Endpoint: adaptor.EndpointCodexSettingsUser, Method: http.MethodGet, Path: "/settings/user"},
	{Endpoint: adaptor.EndpointCodexTasks, Method: http.MethodPost, Path: "/tasks"},
	{Endpoint: adaptor.EndpointCodexTasksList, Method: http.MethodGet, Path: "/tasks/list"},
	{Endpoint: adaptor.EndpointCodexEnvironments, Method: http.MethodGet, Path: "/environments"},
	{Endpoint: adaptor.EndpointCodexWorkspaceMessages, Method: http.MethodGet, Path: "/workspace-messages"},
	{Endpoint: adaptor.EndpointCodexPSMCP, Method: http.MethodPost, Path: "/ps/mcp"},
	{Endpoint: adaptor.EndpointCodexPSMCP, Method: http.MethodGet, Path: "/ps/mcp"},
	{Endpoint: adaptor.EndpointCodexPSMCP, Method: http.MethodDelete, Path: "/ps/mcp"},
	// The app-server turn-cost worker uses the public OpenAI analytics path
	// directly (`/v1/analytics/codex/turn-costs`), rather than the `/wham` or
	// `/api/codex` backend-client families.
	{Endpoint: adaptor.EndpointCodexTurnCosts, Method: http.MethodPost, Path: "/analytics/codex/turn-costs"},
}

// codexBackendClientRoutePrefixes are sorted longest-first so a versioned or
// backend alias cannot be mistaken for a shorter public prefix.
var codexBackendClientRoutePrefixes = []string{
	"/backend-api/v1/wham",
	"/backend-api/wham/v1",
	"/backend-api/wham",
	"/backend-api/codex/v1",
	"/backend-api/codex",
	"/backend-api/v1",
	"/backend-api",
	"/api/codex/v1",
	"/api/codex",
	"/codex/v1",
	"/codex",
	"/wham/v1",
	"/wham",
	"/v1",
	"",
}

// HandleCodexBackendClient forwards the official Codex backend-client and
// app-server management calls without translating or billing them. Requests
// are matched against the finite route table above; task/turn identifiers are
// accepted only as one safe path segment. The original body, method, query,
// response status, headers and bytes stay on the native account path.
func (p *Pipeline) HandleCodexBackendClient(c *gin.Context) {
	setEntryProtocol(c, registry.ProtocolOpenAI)
	route, providerPath, ok := codexBackendClientRouteForRequest(c)
	if !ok {
		writeError(c, http.StatusNotFound, "invalid_request_error", "unsupported_endpoint", "unsupported Codex backend-client endpoint")
		return
	}
	if !requireCodexClientBoundary(c, "backend-client") {
		return
	}
	if route.Method != "" && !strings.EqualFold(c.Request.Method, route.Method) {
		writeError(c, http.StatusMethodNotAllowed, "invalid_request_error", "method_not_allowed", "HTTP method is not supported for this Codex endpoint")
		return
	}
	keyInfo, ok := requireKeyInfo(c)
	if !ok {
		return
	}
	body, ok := readRawBody(c)
	if !ok {
		return
	}
	if body == nil {
		body = []byte{}
	}

	// Management calls do not carry a model-routing contract. Start with an
	// empty request object so native picker can use any schedulable Codex
	// account; retain the exact body in rawBody for the provider.
	req, err := dto.ParseChatRequest([]byte("{}"))
	if err != nil {
		writeError(c, http.StatusInternalServerError, "server_error", "internal_error", "failed to initialize Codex backend request")
		return
	}
	req.Stream = codexBackendClientShouldStream(c, route.Endpoint)
	opts := forwardOptions{
		method:                  c.Request.Method,
		rawBody:                 body,
		rawContentType:          c.GetHeader("Content-Type"),
		providerPath:            providerPath,
		nativeCodexAccountsOnly: true,
		nativeCodexAPIKeyOnly:   route.Endpoint == adaptor.EndpointCodexTurnCosts,
		nativeCodexOAuthOnly:    route.OAuthOnly,
		allowUnpriced:           true,
		zeroBilling:             true,
		passthroughResponse:     true,
	}
	// The provider returns a short-lived, signed blob URL for workspace plugin
	// bundles. Never expose that URL to the CLI: replace it with an AirGate
	// opaque lease and relay the subsequent unauthenticated PUT through the
	// account-bound proxy. The final workspace create/update call is pinned to
	// the same account and consumes the lease only after a successful response.
	switch route.Endpoint {
	case adaptor.EndpointCodexPluginsWorkspaceUploadURL:
		uploadRequest, err := parseCodexPluginUploadURLRequest(body)
		if err != nil {
			writeError(c, http.StatusBadRequest, "invalid_request_error", "invalid_upload_request", err.Error())
			return
		}
		opts.responseTransform = func(account *accountreg.Snapshot, result *attemptResult) error {
			return p.rewriteCodexPluginUploadURLResponse(c, keyInfo, account, uploadRequest, result)
		}
	case adaptor.EndpointCodexPluginsWorkspaceCreate, adaptor.EndpointCodexPluginsWorkspaceUpdate:
		// Give malformed JSON/fields a client error while keeping a missing or
		// mismatched upload lease distinguishable as a conflict.
		if _, _, err := codexPluginUploadFinalizeFields(body); err != nil {
			writeError(c, http.StatusBadRequest, "invalid_request_error", "invalid_upload_finalize", err.Error())
			return
		}
		pluginOpts, err := p.codexPluginFinalizeOptions(c, keyInfo, route, providerPath, body)
		if err != nil {
			writeError(c, http.StatusConflict, "invalid_request_error", "upload_not_ready", err.Error())
			return
		}
		opts.nativeCodexAccountID = pluginOpts.nativeCodexAccountID
		opts.responseTransform = pluginOpts.responseTransform
	}
	p.forwardOpt(c, keyInfo, req, route.Endpoint, opts)
}

func codexBackendClientShouldStream(c *gin.Context, endpoint string) bool {
	if endpoint != adaptor.EndpointCodexPSMCP || c == nil || c.Request == nil {
		return false
	}
	if c.Request.Method == http.MethodGet {
		return true
	}
	accept := strings.ToLower(c.GetHeader("Accept"))
	return strings.Contains(accept, "text/event-stream")
}

func codexBackendClientRouteForRequest(c *gin.Context) (codexBackendClientRoute, string, bool) {
	if c == nil || c.Request == nil || c.Request.URL == nil {
		return codexBackendClientRoute{}, "", false
	}
	// Route shape must be evaluated on the escaped path.  Gin is configured
	// with UseRawPath, and official cloud-task repository identifiers are URL
	// path segments that may themselves contain escaped separators (for
	// example an owner/ref containing `%2F`).  Looking only at URL.Path would
	// turn those bytes into extra route segments before the finite allowlist is
	// applied.
	path, ok := codexBackendClientEscapedPath(c.Request.URL)
	if !ok {
		return codexBackendClientRoute{}, "", false
	}
	path = strings.TrimRight(path, "/")
	if path == "" {
		return codexBackendClientRoute{}, "", false
	}
	canonical := ""
	for _, prefix := range codexBackendClientRoutePrefixes {
		if path == prefix {
			canonical = "/"
			break
		}
		if strings.HasPrefix(path, prefix+"/") {
			canonical = strings.TrimPrefix(path, prefix)
			break
		}
	}
	if canonical == "" {
		return codexBackendClientRoute{}, "", false
	}
	var pathRoute *codexBackendClientRoute
	for _, route := range codexBackendClientStaticRoutes {
		if route.Path != canonical {
			continue
		}
		if strings.EqualFold(route.Method, c.Request.Method) {
			return route, canonical, true
		}
		if pathRoute == nil {
			copy := route
			pathRoute = &copy
		}
	}
	if pathRoute != nil {
		// Preserve a known path even when the method is wrong so the handler can
		// return a JSON 405 instead of falling through to the SPA NoRoute path.
		return *pathRoute, canonical, true
	}
	if route, providerPath, ok := codexPluginRouteForCanonicalPath(canonical, c.Request.Method); ok {
		return route, providerPath, true
	}

	parts := strings.Split(strings.Trim(canonical, "/"), "/")
	if len(parts) == 2 && parts[0] == "environments" && parts[1] == "" {
		return codexBackendClientRoute{}, "", false
	}
	if len(parts) == 5 || len(parts) == 6 {
		if parts[0] == "environments" && parts[1] == "by-repo" {
			segments := make([]string, 0, len(parts)-2)
			for _, raw := range parts[2:] {
				decoded, valid := decodeCodexBackendOpaqueSegment(raw)
				if !valid {
					return codexBackendClientRoute{}, "", false
				}
				segments = append(segments, decoded)
			}
			providerPath := "/environments/by-repo"
			for _, segment := range segments {
				providerPath += "/" + url.PathEscape(segment)
			}
			route := codexBackendClientRoute{
				Endpoint: adaptor.EndpointCodexEnvironmentsByRepo,
				Method:   http.MethodGet,
				Path:     providerPath,
			}
			return route, providerPath, true
		}
	}
	if len(parts) == 2 && parts[0] == "tasks" && validCodexBackendPathSegment(parts[1]) {
		return codexBackendClientRoute{Endpoint: adaptor.EndpointCodexTaskDetails, Method: http.MethodGet, Path: canonical}, canonical, true
	}
	if len(parts) == 5 && parts[0] == "tasks" && parts[2] == "turns" && parts[4] == "sibling_turns" &&
		validCodexBackendPathSegment(parts[1]) && validCodexBackendPathSegment(parts[3]) {
		return codexBackendClientRoute{Endpoint: adaptor.EndpointCodexTaskSiblingTurns, Method: http.MethodGet, Path: canonical}, canonical, true
	}
	return codexBackendClientRoute{}, "", false
}

// codexBackendClientEscapedPath returns a request path before URL-escaped
// dynamic segments are decoded.  Reject malformed RawPath values rather than
// letting net/url silently reinterpret them into a different resource.
func codexBackendClientEscapedPath(u *url.URL) (string, bool) {
	if u == nil {
		return "", false
	}
	if u.RawPath != "" {
		decoded, err := url.PathUnescape(u.RawPath)
		if err != nil || decoded != u.Path {
			return "", false
		}
	}
	path := u.EscapedPath()
	if path == "" {
		path = u.Path
	}
	if path == "" || !strings.HasPrefix(path, "/") {
		return "", false
	}
	return path, true
}

// decodeCodexBackendOpaqueSegment performs exactly one path-segment decode.
// Opaque cloud-task values may contain URL punctuation, including an encoded
// slash, but never a backslash, traversal sentinel, control byte, or invalid
// UTF-8. Re-escaping the decoded value when constructing providerPath keeps
// the upstream request's segment boundary intact.
func decodeCodexBackendOpaqueSegment(raw string) (string, bool) {
	if raw == "" {
		return "", false
	}
	decoded, err := url.PathUnescape(raw)
	if err != nil || !validCodexBackendOpaqueSegment(decoded) {
		return "", false
	}
	return decoded, true
}

func validCodexBackendOpaqueSegment(value string) bool {
	if value == "" || strings.TrimSpace(value) == "" || value == "." || value == ".." || len(value) > 512 || !utf8.ValidString(value) {
		return false
	}
	if strings.ContainsRune(value, '\\') {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func validCodexBackendPathSegment(value string) bool {
	// Task/turn identifiers are opaque URL path segments. Decode exactly once
	// for traversal/control validation, but keep the escaped spelling in the
	// provider path so an encoded slash remains inside one upstream segment.
	decoded, err := url.PathUnescape(value)
	if err != nil || decoded == "" || strings.TrimSpace(decoded) == "" || decoded == "." || decoded == ".." || len(decoded) > 512 || !utf8.ValidString(decoded) {
		return false
	}
	if strings.ContainsRune(decoded, '\\') {
		return false
	}
	for _, r := range decoded {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}
