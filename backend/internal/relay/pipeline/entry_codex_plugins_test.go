package pipeline

import (
	"net/http"
	"testing"

	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
)

func TestCodexPluginRouteForCanonicalPath(t *testing.T) {
	tests := []struct {
		path     string
		method   string
		endpoint string
		ok       bool
		wantPath string
	}{
		{"/ps/plugins/list", http.MethodGet, adaptor.EndpointCodexPluginsList, true, "/ps/plugins/list"},
		{"/ps/plugins/search", http.MethodPost, adaptor.EndpointCodexPluginsSearch, true, "/ps/plugins/search"}, // method is checked by handler
		{"/ps/plugins/plugin-a", http.MethodGet, adaptor.EndpointCodexPluginDetail, true, "/ps/plugins/plugin-a"},
		{"/ps/plugins/plugin-a/skills/my-skill", http.MethodGet, adaptor.EndpointCodexPluginSkillDetail, true, "/ps/plugins/plugin-a/skills/my-skill"},
		{"/ps/plugins/plugin-a/install", http.MethodPost, adaptor.EndpointCodexPluginInstall, true, "/ps/plugins/plugin-a/install"},
		{"/ps/plugins/plugin-a/uninstall", http.MethodPost, adaptor.EndpointCodexPluginUninstall, true, "/ps/plugins/plugin-a/uninstall"},
		{"/ps/plugins/plugin-a/shares", http.MethodPut, adaptor.EndpointCodexPluginShares, true, "/ps/plugins/plugin-a/shares"},
		{"/ps/plugins/plugin-a/../install", http.MethodPost, "", false, ""},
		{"/ps/plugins/plugin-a/skills/", http.MethodGet, "", false, ""},
		{"/ps/plugins/", http.MethodGet, "", false, ""},
	}
	for _, tt := range tests {
		route, path, ok := codexPluginRouteForCanonicalPath(tt.path, tt.method)
		if ok != tt.ok {
			t.Fatalf("path %q ok=%v, want %v", tt.path, ok, tt.ok)
		}
		if !tt.ok {
			continue
		}
		if route.Endpoint != tt.endpoint || path != tt.wantPath || !route.OAuthOnly {
			t.Fatalf("path %q route=(%+v,%q), want endpoint %q path %q OAuthOnly", tt.path, route, path, tt.endpoint, tt.wantPath)
		}
	}
}

func TestValidCodexPluginPathSegment(t *testing.T) {
	for _, value := range []string{"plugin-a", "skill.v1", "中文技能"} {
		if !validCodexPluginPathSegment(value) {
			t.Errorf("valid segment %q rejected", value)
		}
	}
	for _, value := range []string{"", ".", "..", "a/b", `a\b`, "a\x00b", "a%2Fb", "%2e%2e", "%ZZ"} {
		if validCodexPluginPathSegment(value) {
			t.Errorf("invalid segment %q accepted", value)
		}
	}
}

func TestCodexBackendAppsAndWorkspacePluginRoutes(t *testing.T) {
	tests := []struct {
		path     string
		method   string
		endpoint string
		wantPath string
		wantAuth bool
	}{
		{"/connectors/directory/list", http.MethodGet, adaptor.EndpointCodexConnectorsDirectoryList, "/connectors/directory/list", true},
		{"/connectors/directory/list_workspace", http.MethodGet, adaptor.EndpointCodexConnectorsDirectoryListWorkspace, "/connectors/directory/list_workspace", true},
		{"/ps/apps/batch", http.MethodPost, adaptor.EndpointCodexAppsBatch, "/ps/apps/batch", true},
		{"/plugins/featured", http.MethodGet, adaptor.EndpointCodexPluginsFeatured, "/plugins/featured", false},
		{"/plugins/foo/enable", http.MethodPost, adaptor.EndpointCodexPluginLegacyEnable, "/plugins/foo/enable", true},
		{"/plugins/foo/uninstall", http.MethodPost, adaptor.EndpointCodexPluginLegacyUninstall, "/plugins/foo/uninstall", true},
		{"/public/plugins/workspace/upload-url", http.MethodPost, adaptor.EndpointCodexPluginsWorkspaceUploadURL, "/public/plugins/workspace/upload-url", true},
		{"/public/plugins/workspace", http.MethodPost, adaptor.EndpointCodexPluginsWorkspaceCreate, "/public/plugins/workspace", true},
		{"/public/plugins/workspace/foo", http.MethodGet, adaptor.EndpointCodexPluginsWorkspaceDetail, "/public/plugins/workspace/foo", true},
		{"/public/plugins/workspace/foo", http.MethodPost, adaptor.EndpointCodexPluginsWorkspaceUpdate, "/public/plugins/workspace/foo", true},
		{"/public/plugins/workspace/foo", http.MethodDelete, adaptor.EndpointCodexPluginsWorkspaceDelete, "/public/plugins/workspace/foo", true},
	}
	for _, tt := range tests {
		route, path, ok := codexPluginRouteForCanonicalPath(tt.path, tt.method)
		if !ok || route.Endpoint != tt.endpoint || path != tt.wantPath || route.OAuthOnly != tt.wantAuth {
			t.Errorf("route(%s %s)=(%+v,%q,%v), want endpoint=%s path=%s oauth=%v", tt.method, tt.path, route, path, ok, tt.endpoint, tt.wantPath, tt.wantAuth)
		}
	}
	for _, path := range []string{
		"/public/plugins/workspace/foo%2Fbar",
		"/public/plugins/workspace/%2e%2e",
		"/plugins/foo%2Fbar/enable",
		"/plugins/foo/enable/extra",
	} {
		if _, _, ok := codexPluginRouteForCanonicalPath(path, http.MethodPost); ok {
			t.Errorf("unsafe/unknown workspace route %q unexpectedly accepted", path)
		}
	}
}
