package account

import (
	"strings"
	"testing"
)

func TestMergeCredentialsUpdate_PreservesTokens(t *testing.T) {
	existing := map[string]string{
		"access_token":  "at-secret",
		"refresh_token": "rt-secret",
		"email":         "old@example.com",
		"plan_type":     "plus",
	}
	// 编辑表单只回传 email（旧 bug 会整包覆盖）
	patch := map[string]string{
		"email": "new@example.com",
	}
	got := mergeCredentialsUpdate(existing, patch)
	if got["access_token"] != "at-secret" {
		t.Fatalf("access_token wiped: %q", got["access_token"])
	}
	if got["refresh_token"] != "rt-secret" {
		t.Fatalf("refresh_token wiped: %q", got["refresh_token"])
	}
	if got["email"] != "new@example.com" {
		t.Fatalf("email not updated: %q", got["email"])
	}
}

func TestMergeCredentialsUpdate_SkipsRedactedPlaceholder(t *testing.T) {
	existing := map[string]string{
		"access_token": "real-at",
		"api_key":      "real-key",
	}
	patch := map[string]string{
		"access_token": "***",
		"api_key":      "",
		"email":        "a@b.c",
	}
	got := mergeCredentialsUpdate(existing, patch)
	if got["access_token"] != "real-at" {
		t.Fatalf("redacted access_token overwrote: %q", got["access_token"])
	}
	if got["api_key"] != "real-key" {
		t.Fatalf("empty api_key overwrote: %q", got["api_key"])
	}
	if got["email"] != "a@b.c" {
		t.Fatalf("email missing: %q", got["email"])
	}
}

func TestMergeCredentialsUpdate_ReplacesRealSecrets(t *testing.T) {
	existing := map[string]string{"refresh_token": "old-rt"}
	patch := map[string]string{"refresh_token": "new-rt", "access_token": "new-at"}
	got := mergeCredentialsUpdate(existing, patch)
	if got["refresh_token"] != "new-rt" || got["access_token"] != "new-at" {
		t.Fatalf("unexpected merge: %#v", got)
	}
}

func TestCredentialMissingMsg_LocalNoUpstream(t *testing.T) {
	msg := credentialMissingMsg(map[string]string{"email": "x@y.z"})
	if !strings.Contains(msg, "未向上游发请求") {
		t.Fatalf("should clarify no upstream call: %s", msg)
	}
	if !strings.Contains(msg, "email") {
		t.Fatalf("should list keys: %s", msg)
	}
}

func TestFormatUpstreamHTTPError_ShowsJSONBody(t *testing.T) {
	body := []byte(`{"detail":"The 'gpt-5.3-codex-spark' model is not supported when using Codex with a ChatGPT account."}`)
	got := formatUpstreamHTTPError(400, body)
	if !strings.HasPrefix(got, "上游 HTTP 400\n") {
		t.Fatalf("unexpected format:\n%s", got)
	}
	if strings.Count(got, "not supported") != 1 {
		t.Fatalf("message should appear once:\n%s", got)
	}
	if !strings.Contains(got, `"detail"`) {
		t.Fatalf("should show JSON body:\n%s", got)
	}
}

func TestFormatUpstreamHTTPError_PrettyPrintsNested(t *testing.T) {
	body := []byte(`{"error":{"message":"bad model","type":"invalid_request_error","code":"model_not_found"},"request_id":"req_123"}`)
	got := formatUpstreamHTTPError(400, body)
	if !strings.Contains(got, "request_id") || !strings.Contains(got, "bad model") {
		t.Fatalf("missing fields:\n%s", got)
	}
	// pretty-print 会有换行缩进
	if !strings.Contains(got, "\n  ") {
		t.Fatalf("expected indented JSON:\n%s", got)
	}
}
