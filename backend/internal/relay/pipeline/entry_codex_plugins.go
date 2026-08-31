package pipeline

import (
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
)

// codexPluginStaticRoutes is the finite remote-plugin surface used by current
// Codex CLI builds. The upstream service is ChatGPT OAuth-only; arbitrary
// /ps/plugins paths are intentionally not proxied.
var codexPluginStaticRoutes = []codexBackendClientRoute{
	{Endpoint: adaptor.EndpointCodexPluginsList, Method: http.MethodGet, Path: "/ps/plugins/list", OAuthOnly: true},
	{Endpoint: adaptor.EndpointCodexPluginsSearch, Method: http.MethodGet, Path: "/ps/plugins/search", OAuthOnly: true},
	{Endpoint: adaptor.EndpointCodexPluginsSuggested, Method: http.MethodGet, Path: "/ps/plugins/suggested/codex", OAuthOnly: true},
	{Endpoint: adaptor.EndpointCodexPluginsInstalled, Method: http.MethodGet, Path: "/ps/plugins/installed", OAuthOnly: true},
	{Endpoint: adaptor.EndpointCodexPluginsWorkspaceShared, Method: http.MethodGet, Path: "/ps/plugins/workspace/shared", OAuthOnly: true},
	{Endpoint: adaptor.EndpointCodexPluginsWorkspaceCreated, Method: http.MethodGet, Path: "/ps/plugins/workspace/created", OAuthOnly: true},
	// Apps/Connectors are fetched by the official ChatGPT client alongside the
	// remote-plugin catalog. They live at the backend root rather than under
	// /wham or /api/codex, and require a ChatGPT OAuth lease when invoked via
	// the native executor.
	{Endpoint: adaptor.EndpointCodexConnectorsDirectoryList, Method: http.MethodGet, Path: "/connectors/directory/list", OAuthOnly: true},
	{Endpoint: adaptor.EndpointCodexConnectorsDirectoryListWorkspace, Method: http.MethodGet, Path: "/connectors/directory/list_workspace", OAuthOnly: true},
	{Endpoint: adaptor.EndpointCodexAppsBatch, Method: http.MethodPost, Path: "/ps/apps/batch", OAuthOnly: true},
	// Legacy featured discovery accepts an optional auth token upstream. Keep
	// this route non-OAuth-only so a public/custom gateway can still serve the
	// response while OAuth accounts receive their normal selected lease.
	{Endpoint: adaptor.EndpointCodexPluginsFeatured, Method: http.MethodGet, Path: "/plugins/featured", OAuthOnly: false},
	{Endpoint: adaptor.EndpointCodexPluginsWorkspaceUploadURL, Method: http.MethodPost, Path: "/public/plugins/workspace/upload-url", OAuthOnly: true},
	{Endpoint: adaptor.EndpointCodexPluginsWorkspaceCreate, Method: http.MethodPost, Path: "/public/plugins/workspace", OAuthOnly: true},
}

// codexPluginRouteForCanonicalPath recognizes a canonical suffix after one of
// the public Codex/backend aliases has been stripped. It returns a route even
// when the HTTP method is wrong so the caller can emit a JSON 405 instead of
// falling through to Gin's SPA NoRoute handler.
func codexPluginRouteForCanonicalPath(canonical, method string) (codexBackendClientRoute, string, bool) {
	canonical = strings.TrimRight(strings.TrimSpace(canonical), "/")
	if canonical == "" {
		return codexBackendClientRoute{}, "", false
	}
	for _, route := range codexPluginStaticRoutes {
		if route.Path != canonical {
			continue
		}
		if strings.EqualFold(route.Method, method) {
			return route, canonical, true
		}
		return route, canonical, true
	}

	// Workspace plugin sharing uses a public-service path separate from the
	// /ps/plugins catalog. The collection POST creates a share; an opaque
	// plugin id selects update/delete (and, on newer services, detail GET).
	const workspacePrefix = "/public/plugins/workspace"
	if canonical == workspacePrefix {
		route := codexBackendClientRoute{
			Endpoint:  adaptor.EndpointCodexPluginsWorkspaceCreate,
			Method:    http.MethodPost,
			Path:      canonical,
			OAuthOnly: true,
		}
		return route, canonical, true
	}
	if strings.HasPrefix(canonical, workspacePrefix+"/") {
		parts := strings.Split(strings.TrimPrefix(canonical, workspacePrefix+"/"), "/")
		if len(parts) == 1 && validCodexPluginPathSegment(parts[0]) {
			if strings.EqualFold(method, http.MethodDelete) {
				return codexBackendClientRoute{Endpoint: adaptor.EndpointCodexPluginsWorkspaceDelete, Method: http.MethodDelete, Path: canonical, OAuthOnly: true}, canonical, true
			}
			if strings.EqualFold(method, http.MethodGet) {
				return codexBackendClientRoute{Endpoint: adaptor.EndpointCodexPluginsWorkspaceDetail, Method: http.MethodGet, Path: canonical, OAuthOnly: true}, canonical, true
			}
			return codexBackendClientRoute{Endpoint: adaptor.EndpointCodexPluginsWorkspaceUpdate, Method: http.MethodPost, Path: canonical, OAuthOnly: true}, canonical, true
		}
	}

	// Legacy plugin enable/uninstall calls remain in the upstream CLI for
	// installations migrated from /plugins/featured. Validate the id as one
	// opaque segment before reflecting it into the provider URL.
	const legacyPrefix = "/plugins/"
	if strings.HasPrefix(canonical, legacyPrefix) {
		parts := strings.Split(strings.TrimPrefix(canonical, legacyPrefix), "/")
		if len(parts) == 2 && validCodexPluginPathSegment(parts[0]) {
			var endpoint string
			switch parts[1] {
			case "enable":
				endpoint = adaptor.EndpointCodexPluginLegacyEnable
			case "uninstall":
				endpoint = adaptor.EndpointCodexPluginLegacyUninstall
			default:
				return codexBackendClientRoute{}, "", false
			}
			return codexBackendClientRoute{Endpoint: endpoint, Method: http.MethodPost, Path: canonical, OAuthOnly: true}, canonical, true
		}
	}

	const prefix = "/ps/plugins/"
	if !strings.HasPrefix(canonical, prefix) {
		return codexBackendClientRoute{}, "", false
	}
	parts := strings.Split(strings.TrimPrefix(canonical, prefix), "/")
	if len(parts) == 0 || !validCodexPluginPathSegment(parts[0]) {
		return codexBackendClientRoute{}, "", false
	}

	// GET /ps/plugins/{plugin_id}
	if len(parts) == 1 {
		route := codexBackendClientRoute{
			Endpoint:  adaptor.EndpointCodexPluginDetail,
			Method:    http.MethodGet,
			Path:      canonical,
			OAuthOnly: true,
		}
		return route, canonical, true
	}

	if len(parts) == 2 {
		var endpoint string
		var expectedMethod string
		switch parts[1] {
		case "install":
			endpoint, expectedMethod = adaptor.EndpointCodexPluginInstall, http.MethodPost
		case "uninstall":
			endpoint, expectedMethod = adaptor.EndpointCodexPluginUninstall, http.MethodPost
		case "shares":
			endpoint, expectedMethod = adaptor.EndpointCodexPluginShares, http.MethodPut
		default:
			return codexBackendClientRoute{}, "", false
		}
		route := codexBackendClientRoute{Endpoint: endpoint, Method: expectedMethod, Path: canonical, OAuthOnly: true}
		return route, canonical, true
	}

	// GET /ps/plugins/{plugin_id}/skills/{skill_name}
	if len(parts) == 3 && parts[1] == "skills" && validCodexPluginPathSegment(parts[2]) {
		route := codexBackendClientRoute{
			Endpoint:  adaptor.EndpointCodexPluginSkillDetail,
			Method:    http.MethodGet,
			Path:      canonical,
			OAuthOnly: true,
		}
		return route, canonical, true
	}

	return codexBackendClientRoute{}, "", false
}

// validCodexPluginPathSegment is deliberately stricter than a generic URL
// segment check. Plugin and skill identifiers are opaque names, but they may
// not contain separators, traversal components, controls, or an unbounded
// amount of data that could be reflected into an upstream URL.
func validCodexPluginPathSegment(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || value == "." || value == ".." || len(value) > 256 {
		return false
	}
	// Gin may preserve an escaped segment in URL.RawPath. Validate the decoded
	// spelling as well so `%2F`, traversal escapes, controls, and invalid UTF-8
	// cannot become a second upstream path segment after another URL parser.
	decoded, err := url.PathUnescape(value)
	if err != nil || !utf8.ValidString(decoded) {
		return false
	}
	if decoded == "" || decoded == "." || decoded == ".." || len(decoded) > 256 || strings.ContainsAny(decoded, "/\\") {
		return false
	}
	for _, r := range decoded {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}
