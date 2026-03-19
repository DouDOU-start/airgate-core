package plugin

import (
	"testing"

	sdk "github.com/DouDOU-start/airgate-sdk"
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
	}
	if inst.Platform != "anthropic" {
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
