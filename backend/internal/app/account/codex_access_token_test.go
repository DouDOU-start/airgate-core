package account

import (
	"context"
	"strings"
	"testing"
)

func TestCredentialsFromCodexAccessTokenNormalizesCredentials(t *testing.T) {
	creds, err := CredentialsFromCodexAccessToken("  at-test-token  ")
	if err != nil {
		t.Fatalf("convert access token failed: %v", err)
	}
	if creds["access_token"] != "at-test-token" {
		t.Fatalf("access_token = %q, want trimmed value", creds["access_token"])
	}
	if creds["token_type"] != "Bearer" || creds["credential_origin"] != "import_access_token" {
		t.Fatalf("incomplete access-token metadata: %#v", creds)
	}
	if creds["refresh_token"] != "" || creds["session_token"] != "" {
		t.Fatalf("access-token import must not fabricate refresh credentials: %#v", creds)
	}
}

func TestCredentialsFromCodexAccessTokenRejectsEmptyValue(t *testing.T) {
	_, err := CredentialsFromCodexAccessToken(" \n\t ")
	if err == nil || !strings.Contains(err.Error(), "access_token") {
		t.Fatalf("error = %v, want empty access_token rejection", err)
	}
}

func TestImportCodexAccessTokenCreatesOAuthAccount(t *testing.T) {
	repo := &importCaptureRepo{}
	service := NewService(repo, "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	service.usageFetcher = func(context.Context, string, string, map[string]string, string) (UsageSnapshot, error) {
		return UsageSnapshot{}, nil
	}

	item, err := service.ImportCodexAccessToken(context.Background(), OAuthStartInput{
		Name:           "Codex AT",
		Priority:       3,
		MaxConcurrency: 20,
	}, " at-test-token ")
	if err != nil {
		t.Fatalf("access-token import failed: %v", err)
	}
	if item.Platform != "codex" || item.Type != TypeOAuth || item.Name != "Codex AT" {
		t.Fatalf("account metadata is incorrect: %+v", item)
	}
	if item.Credentials["access_token"] != "at-test-token" || item.Credentials["credential_origin"] != "import_access_token" {
		t.Fatalf("account credentials are incorrect: %#v", item.Credentials)
	}
}
