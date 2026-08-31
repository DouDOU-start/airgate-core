package account

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestApplyCodexIDTokenClaimsPersistsFedRAMPAndProfileClaims(t *testing.T) {
	payload, err := json.Marshal(map[string]any{
		"https://api.openai.com/profile": map[string]any{"email": "profile@example.com"},
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id":         "acct-123",
			"chatgpt_account_is_fedramp": true,
			"chatgpt_plan_type":          "team",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	token := "e30." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
	creds := map[string]string{}
	applyCodexIDTokenClaims(creds, token)
	if creds["email"] != "profile@example.com" || creds["chatgpt_account_id"] != "acct-123" ||
		creds["plan_type"] != "team" || creds["chatgpt_account_is_fedramp"] != "true" {
		t.Fatalf("claims were not persisted: %#v", creds)
	}
}

func TestApplyCodexIDTokenClaimsAcceptsFalseFedRAMPClaim(t *testing.T) {
	payload, _ := json.Marshal(map[string]any{
		"https://api.openai.com/auth": map[string]any{"chatgpt_account_is_fedramp": false},
	})
	token := "e30." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
	creds := map[string]string{}
	applyCodexIDTokenClaims(creds, token)
	if strings.TrimSpace(creds["chatgpt_account_is_fedramp"]) != "false" {
		t.Fatalf("false claim was not preserved: %#v", creds)
	}
}
