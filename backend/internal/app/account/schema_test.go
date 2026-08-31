package account

import "testing"

func TestCanonicalizeKnownAccountType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		raw        string
		want       string
		recognized bool
	}{
		{name: "oauth", raw: " OAUTH ", want: TypeOAuth, recognized: true},
		{name: "refresh token", raw: "refresh_token", want: TypeOAuth, recognized: true},
		{name: "setup token", raw: "setup_token", want: TypeOAuth, recognized: true},
		{name: "session", raw: "session", want: TypeOAuth, recognized: true},
		{name: "device", raw: "device", want: TypeOAuth, recognized: true},
		{name: "api key", raw: " API_KEY ", want: TypeAPIKey, recognized: true},
		{name: "api key compact", raw: "apikey", want: TypeAPIKey, recognized: true},
		{name: "api key hyphen", raw: "api-key", want: TypeAPIKey, recognized: true},
		{name: "service account", raw: "service_account", want: TypeAPIKey, recognized: true},
		{name: "service account hyphen", raw: "service-account", want: TypeAPIKey, recognized: true},
		{name: "empty", raw: "  ", recognized: false},
		{name: "unknown", raw: "custom_auth", recognized: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, recognized := CanonicalizeKnownAccountType(tt.raw)
			if got != tt.want || recognized != tt.recognized {
				t.Fatalf("CanonicalizeKnownAccountType(%q) = (%q, %v), want (%q, %v)", tt.raw, got, recognized, tt.want, tt.recognized)
			}
		})
	}
}

func TestNormalizeAccountTypeRetainsWriteDefault(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{"", "unknown"} {
		if got := NormalizeAccountType(raw); got != TypeOAuth {
			t.Fatalf("NormalizeAccountType(%q) = %q, want %q", raw, got, TypeOAuth)
		}
	}
}
