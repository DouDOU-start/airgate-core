package plugin

import "testing"

func TestOfficialPluginsIncludeCorePlugins(t *testing.T) {
	want := map[string]string{
		"gateway-openai":     "DouDOU-start/airgate-openai",
		"gateway-claude":     "DouDOU-start/airgate-claude",
		"gateway-kiro":       "DouDOU-start/airgate-kiro",
		"airgate-playground": "DouDOU-start/airgate-playground",
		"airgate-studio":     "DouDOU-start/airgate-studio",
		"airgate-health":     "DouDOU-start/airgate-health",
		"payment-epay":       "DouDOU-start/airgate-epay",
	}

	got := make(map[string]MarketplacePlugin, len(officialPlugins))
	for _, p := range officialPlugins {
		got[p.Name] = p
	}

	for name, repo := range want {
		p, ok := got[name]
		if !ok {
			t.Fatalf("officialPlugins missing %q", name)
		}
		if p.GithubRepo != repo {
			t.Fatalf("officialPlugins[%q].GithubRepo = %q, want %q", name, p.GithubRepo, repo)
		}
	}
}
