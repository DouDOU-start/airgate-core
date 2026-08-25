package plugin

import (
	"strings"
	"testing"
)

func TestBuildPluginDSNQuotesOptionsSearchPath(t *testing.T) {
	t.Parallel()

	provisioner := &pluginDSNProvisioner{
		adminFields: dsnFields{
			"host":   "localhost",
			"port":   "5432",
			"dbname": "airgate",
		},
	}

	dsn := provisioner.buildPluginDSN("plugin_airgate-playground_role", "secret", "plugin_airgate-playground")

	if !strings.Contains(dsn, `options='-c search_path="plugin_airgate-playground"'`) {
		t.Fatalf("dsn = %q, want quoted options search_path", dsn)
	}
	if !strings.Contains(dsn, "user=plugin_airgate-playground_role") {
		t.Fatalf("dsn = %q, want plugin role", dsn)
	}
	if strings.Contains(dsn, " options=-c ") {
		t.Fatalf("dsn = %q, options value must not be split by spaces", dsn)
	}
}

func TestQuoteConninfoValue(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   string
		want string
	}{
		{in: "plain", want: "plain"},
		{in: "", want: "''"},
		{in: "-c search_path=plugin", want: "'-c search_path=plugin'"},
		{in: "pa'ss\\word", want: `'pa\'ss\\word'`},
	}

	for _, tc := range cases {
		if got := quoteConninfoValue(tc.in); got != tc.want {
			t.Fatalf("quoteConninfoValue(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
