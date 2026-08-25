package account

import (
	"context"
	"errors"
	"testing"
)

func TestNormalizeConnectivityTestMode(t *testing.T) {
	tests := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{input: "", want: "normal"},
		{input: "normal", want: "normal"},
		{input: " NORMAL ", want: "normal"},
		{input: "overage", want: "overage"},
		{input: " OVERAGE ", want: "overage"},
		{input: "unknown", wantErr: true},
	}
	for _, tt := range tests {
		got, err := normalizeConnectivityTestMode(tt.input)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Fatalf("normalizeConnectivityTestMode(%q) = %q, %v", tt.input, got, err)
		}
	}
}

func TestPrepareConnectivityTestOverageRequiresOAuthAccount(t *testing.T) {
	service := NewService(stubRepository{
		findByID: func(context.Context, int, LoadOptions) (Account, error) {
			return Account{ID: 7, Platform: "openai", Type: "apikey"}, nil
		},
	}, stubPluginCatalog{}, nil, nil)

	_, err := service.PrepareConnectivityTest(t.Context(), 7, "gpt-5.4", "overage")
	if !errors.Is(err, ErrConnectivityTestModeAccountTypeUnsupported) {
		t.Fatalf("PrepareConnectivityTest() error = %v, want account type error", err)
	}
}
