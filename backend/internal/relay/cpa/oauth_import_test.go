package cpa

import (
	"context"
	"net/http"
	"strings"
	"testing"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
)

func TestImportOAuthCredentials刷新并补全项目ID(t *testing.T) {
	manager := coreauth.NewManager(&memoryAuthStore{}, nil, nil)
	executor := &oauthImportExecutorStub{}
	manager.RegisterExecutor(executor)
	bridge := &Bridge{manager: manager}

	credentials, err := bridge.ImportOAuthCredentials(
		context.Background(),
		"antigravity",
		"oauth",
		map[string]string{"refresh_token": "测试刷新令牌"},
		"http://127.0.0.1:7890",
	)
	if err != nil {
		t.Fatalf("ImportOAuthCredentials() 错误: %v", err)
	}
	if executor.refreshCalls != 1 || executor.prepareCalls != 1 {
		t.Fatalf("刷新或项目补全调用次数不正确: refresh=%d prepare=%d", executor.refreshCalls, executor.prepareCalls)
	}
	if !strings.HasPrefix(executor.authID, "airgate-import-") {
		t.Fatalf("未使用临时 Auth ID: %q", executor.authID)
	}
	if executor.refreshToken != "测试刷新令牌" || executor.proxyURL != "http://127.0.0.1:7890" {
		t.Fatalf("临时 Auth 信息不完整: %+v", executor)
	}
	if credentials["access_token"] != "自动换取的访问令牌" || credentials["project_id"] != "自动发现的项目" {
		t.Fatalf("导入结果不完整: %#v", credentials)
	}
}

func TestPrepareOAuthCredentials复用访问令牌补全项目ID(t *testing.T) {
	manager := coreauth.NewManager(&memoryAuthStore{}, nil, nil)
	executor := &oauthImportExecutorStub{}
	manager.RegisterExecutor(executor)
	bridge := &Bridge{manager: manager}

	credentials, err := bridge.PrepareOAuthCredentials(
		context.Background(),
		"antigravity",
		"oauth",
		map[string]string{
			"access_token":  "刚换取的访问令牌",
			"refresh_token": "刚换取的刷新令牌",
			"expired":       "2099-01-01T00:00:00Z",
		},
		"http://127.0.0.1:7890",
	)
	if err != nil {
		t.Fatalf("PrepareOAuthCredentials() 错误: %v", err)
	}
	if executor.refreshCalls != 0 || executor.prepareCalls != 1 {
		t.Fatalf("授权准备调用次数不正确: refresh=%d prepare=%d", executor.refreshCalls, executor.prepareCalls)
	}
	if credentials["access_token"] != "刚换取的访问令牌" ||
		credentials["refresh_token"] != "刚换取的刷新令牌" ||
		credentials["project_id"] != "自动发现的项目" {
		t.Fatalf("授权准备结果不完整: %#v", credentials)
	}
}

func TestEnsureExecutor不回退到其他平台(t *testing.T) {
	manager := coreauth.NewManager(&memoryAuthStore{}, nil, nil)
	manager.RegisterExecutor(&oauthImportExecutorStub{provider: "gemini"})
	bridge := &Bridge{manager: manager}

	if _, err := bridge.EnsureExecutor("antigravity"); err == nil {
		t.Fatal("缺少 antigravity executor 时不应回退到 gemini")
	}
}

type oauthImportExecutorStub struct {
	provider     string
	refreshCalls int
	prepareCalls int
	authID       string
	refreshToken string
	proxyURL     string
}

func (e *oauthImportExecutorStub) Identifier() string {
	if e.provider != "" {
		return e.provider
	}
	return "antigravity"
}

func (e *oauthImportExecutorStub) Execute(
	context.Context,
	*coreauth.Auth,
	coreexecutor.Request,
	coreexecutor.Options,
) (coreexecutor.Response, error) {
	return coreexecutor.Response{}, nil
}

func (e *oauthImportExecutorStub) ExecuteStream(
	context.Context,
	*coreauth.Auth,
	coreexecutor.Request,
	coreexecutor.Options,
) (*coreexecutor.StreamResult, error) {
	return nil, nil
}

func (e *oauthImportExecutorStub) Refresh(_ context.Context, auth *coreauth.Auth) (*coreauth.Auth, error) {
	e.refreshCalls++
	e.authID = auth.ID
	e.refreshToken = authMetadataString(auth, "refresh_token")
	e.proxyURL = auth.ProxyURL
	updated := auth.Clone()
	updated.Metadata["access_token"] = "自动换取的访问令牌"
	return updated, nil
}

func (e *oauthImportExecutorStub) CountTokens(
	context.Context,
	*coreauth.Auth,
	coreexecutor.Request,
	coreexecutor.Options,
) (coreexecutor.Response, error) {
	return coreexecutor.Response{}, nil
}

func (e *oauthImportExecutorStub) HttpRequest(
	context.Context,
	*coreauth.Auth,
	*http.Request,
) (*http.Response, error) {
	return nil, nil
}

func (e *oauthImportExecutorStub) ShouldPrepareRequestAuth(auth *coreauth.Auth) bool {
	return authMetadataString(auth, "project_id") == ""
}

func (e *oauthImportExecutorStub) PrepareRequestAuth(_ context.Context, auth *coreauth.Auth) (*coreauth.Auth, error) {
	e.prepareCalls++
	updated := auth.Clone()
	updated.Metadata["project_id"] = "自动发现的项目"
	return updated, nil
}
