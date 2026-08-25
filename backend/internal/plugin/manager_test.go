package plugin

import (
	"os/exec"
	"testing"

	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"
)

func TestMatchPluginByPlatformAndPath(t *testing.T) {
	mgr := &Manager{
		instances: map[string]*PluginInstance{
			"openai-plugin":    {Name: "openai-plugin", Platform: "openai"},
			"anthropic-plugin": {Name: "anthropic-plugin", Platform: "anthropic"},
		},
		routeCache: map[string][]sdk.RouteDefinition{
			"openai-plugin": {
				{Method: "POST", Path: "/v1/messages"},
			},
			"anthropic-plugin": {
				{Method: "POST", Path: "/v1/messages"},
			},
		},
	}

	inst := mgr.MatchPluginByPlatformAndPath("anthropic", "/v1/messages")
	if inst == nil {
		t.Fatal("expected plugin instance, got nil")
	} else if inst.Platform != "anthropic" {
		t.Fatalf("expected anthropic plugin, got %q", inst.Platform)
	}
}

func TestMatchPluginByPlatformAndPathRejectsUnsupportedPath(t *testing.T) {
	mgr := &Manager{
		instances: map[string]*PluginInstance{
			"openai-plugin": {Name: "openai-plugin", Platform: "openai"},
		},
		routeCache: map[string][]sdk.RouteDefinition{
			"openai-plugin": {
				{Method: "POST", Path: "/v1/chat/completions"},
			},
		},
	}

	inst := mgr.MatchPluginByPlatformAndPath("openai", "/v1/messages")
	if inst != nil {
		t.Fatalf("expected no plugin match, got %q", inst.Name)
	}
}

func TestPluginSupportsRouteWebSocketMethod(t *testing.T) {
	mgr := &Manager{
		instances: map[string]*PluginInstance{
			"openai-plugin": {Name: "openai-plugin", Platform: "openai"},
		},
		routeCache: map[string][]sdk.RouteDefinition{
			"openai-plugin": {
				{Method: "WS", Path: "/v1/responses"},
				{Method: "POST", Path: "/v1/responses"},
			},
		},
	}
	if !mgr.PluginSupportsRoute("openai-plugin", "ws", "/v1/responses") {
		t.Fatal("expected WS route to be supported")
	}
	if mgr.PluginSupportsRoute("openai-plugin", "GET", "/v1/responses") {
		t.Fatal("GET must not be treated as a declared WS route")
	}
}

func TestParseGithubRepo(t *testing.T) {
	owner, name, err := parseGithubRepo("https://github.com/acme/airgate-plugin.git")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if owner != "acme" || name != "airgate-plugin" {
		t.Fatalf("expected acme/airgate-plugin, got %s/%s", owner, name)
	}
}

func TestGetModelsReturnsClone(t *testing.T) {
	mgr := &Manager{
		modelCache: map[string][]sdk.ModelInfo{
			"openai": {
				{ID: "gpt-4.1", Name: "GPT-4.1"},
			},
		},
	}

	models := mgr.GetModels("openai")
	models[0].Name = "mutated"

	if got := mgr.modelCache["openai"][0].Name; got != "GPT-4.1" {
		t.Fatalf("expected cached model to remain unchanged, got %q", got)
	}
}

func TestNewPluginClientConfigSetsStartTimeout(t *testing.T) {
	mgr := &Manager{}
	cfg := mgr.newPluginClientConfig(exec.Command("sh", "-c", "exit 0"), false, nil)

	if cfg.StartTimeout != pluginStartTimeout {
		t.Fatalf("StartTimeout = %v, want %v", cfg.StartTimeout, pluginStartTimeout)
	}
}
