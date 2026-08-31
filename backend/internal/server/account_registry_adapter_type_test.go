package server

import (
	"testing"

	"github.com/DouDOU-start/airgate-core/ent"
)

func TestAccountRegistryAdapterCanonicalizesKnownAccountTypeAliases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		raw  string
		want string
	}{
		{raw: " OAUTH ", want: "oauth"},
		{raw: "refresh_token", want: "oauth"},
		{raw: "api-key", want: "api_key"},
		{raw: " APIKEY ", want: "api_key"},
		{raw: "service_account", want: "api_key"},
	}

	adapter := &accountRegistryAdapter{}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			snapshot, err := adapter.mapSnapshot(&ent.Account{Platform: "codex", Type: tt.raw})
			if err != nil {
				t.Fatalf("mapSnapshot() error = %v", err)
			}
			if snapshot.Type != tt.want {
				t.Fatalf("snapshot.Type = %q, want %q", snapshot.Type, tt.want)
			}
		})
	}
}

func TestAccountRegistryAdapterKeepsUnknownAccountTypesFailClosed(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{"", "  ", "custom_auth"} {
		t.Run(raw, func(t *testing.T) {
			snapshot, err := (&accountRegistryAdapter{}).mapSnapshot(&ent.Account{Platform: "codex", Type: raw})
			if err != nil {
				t.Fatalf("mapSnapshot() error = %v", err)
			}
			if snapshot.Type != raw {
				t.Fatalf("snapshot.Type = %q, want unrecognized value %q to remain non-canonical", snapshot.Type, raw)
			}
		})
	}
}

func TestAccountRegistryAdapterLeavesNonCodexAccountTypeUntouched(t *testing.T) {
	t.Parallel()

	for _, platform := range []string{"openai", "xai", "claude"} {
		t.Run(platform, func(t *testing.T) {
			raw := "api-key"
			snapshot, err := (&accountRegistryAdapter{}).mapSnapshot(&ent.Account{Platform: platform, Type: raw})
			if err != nil {
				t.Fatalf("mapSnapshot() error = %v", err)
			}
			if snapshot.Type != raw {
				t.Fatalf("snapshot.Type = %q, want non-Codex type %q unchanged", snapshot.Type, raw)
			}
		})
	}
}
