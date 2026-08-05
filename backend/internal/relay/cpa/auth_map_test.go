package cpa

import "testing"

func TestMapAuthXAIOAuth补齐认证类型(t *testing.T) {
	auth, err := MapAuth(AccountAuthInput{
		AccountID: 1,
		Name:      "xAI 订阅账号",
		Platform:  "xai",
		Type:      "oauth",
		Credentials: map[string]string{
			"access_token":      "test-token",
			"credential_origin": "oauth",
		},
	})
	if err != nil {
		t.Fatalf("MapAuth() 错误: %v", err)
	}
	if got := metadataToString(auth.Metadata["auth_kind"]); got != "oauth" {
		t.Fatalf("auth_kind = %q，期望 oauth", got)
	}
}

func TestMapAuthXAIAPIKey不伪装成OAuth(t *testing.T) {
	auth, err := MapAuth(AccountAuthInput{
		AccountID: 2,
		Name:      "xAI API Key",
		Platform:  "xai",
		Type:      "api_key",
		Credentials: map[string]string{
			"api_key": "xai-test-key",
		},
	})
	if err != nil {
		t.Fatalf("MapAuth() 错误: %v", err)
	}
	if got := metadataToString(auth.Metadata["auth_kind"]); got != "" {
		t.Fatalf("API Key 账号不应补 auth_kind，实际为 %q", got)
	}
}
