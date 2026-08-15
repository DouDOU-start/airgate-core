package account

import (
	"context"
	"strings"
	"testing"
)

func TestCredentialsFromCodexAccessToken规范化凭证(t *testing.T) {
	creds, err := CredentialsFromCodexAccessToken("  at-test-token  ")
	if err != nil {
		t.Fatalf("转换 Access Token 失败: %v", err)
	}
	if creds["access_token"] != "at-test-token" {
		t.Fatalf("access_token = %q，期望去除首尾空白", creds["access_token"])
	}
	if creds["token_type"] != "Bearer" || creds["credential_origin"] != "import_access_token" {
		t.Fatalf("AT 凭证元数据不完整: %#v", creds)
	}
	if creds["refresh_token"] != "" || creds["session_token"] != "" {
		t.Fatalf("AT 导入不应伪造刷新凭证: %#v", creds)
	}
}

func TestCredentialsFromCodexAccessToken拒绝空值(t *testing.T) {
	_, err := CredentialsFromCodexAccessToken(" \n\t ")
	if err == nil || !strings.Contains(err.Error(), "access_token 不能为空") {
		t.Fatalf("错误 = %v，期望拒绝空 access_token", err)
	}
}

func TestImportCodexAccessToken创建OAuth账号(t *testing.T) {
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
		t.Fatalf("AT 导入失败: %v", err)
	}
	if item.Platform != "codex" || item.Type != TypeOAuth || item.Name != "Codex AT" {
		t.Fatalf("账号基本信息不正确: %+v", item)
	}
	if item.Credentials["access_token"] != "at-test-token" || item.Credentials["credential_origin"] != "import_access_token" {
		t.Fatalf("账号凭证不正确: %#v", item.Credentials)
	}
}
