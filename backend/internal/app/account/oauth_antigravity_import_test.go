package account

import (
	"context"
	"errors"
	"strings"
	"testing"
)

const antigravityImportTestSecret = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

func TestImportAntigravityRefresh创建完整账号(t *testing.T) {
	repo := &antigravityImportRepo{}
	service := NewService(repo, antigravityImportTestSecret)
	importer := &antigravityImporterStub{credentials: map[string]string{
		"access_token":  "新访问令牌",
		"refresh_token": "旋转后的刷新令牌",
		"project_id":    "自动发现的项目",
		"expired":       "2026-08-07T12:00:00Z",
	}}
	service.SetOAuthCredentialRefresher(importer)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 阻止测试访问 Google userinfo；邮箱获取失败不影响导入。
	item, err := service.ImportAntigravityRefresh(ctx, OAuthStartInput{
		Name:     "Antigravity 测试账号",
		ProxyURL: "http://127.0.0.1:7890",
		GroupIDs: []int64{3, 5},
	}, "  测试刷新令牌  ")
	if err != nil {
		t.Fatalf("ImportAntigravityRefresh() 错误: %v", err)
	}
	if importer.calls != 1 || importer.platform != "antigravity" || importer.accountType != TypeOAuth {
		t.Fatalf("导入器调用参数不正确: %+v", importer)
	}
	if importer.refreshToken != "测试刷新令牌" || importer.proxyURL != "http://127.0.0.1:7890" {
		t.Fatalf("RT 或代理未正确传给导入器: %+v", importer)
	}
	if repo.createInput == nil {
		t.Fatal("账号未创建")
	}
	if item.Platform != "antigravity" || item.Type != TypeOAuth || item.Name != "Antigravity 测试账号" {
		t.Fatalf("账号基本信息不正确: %+v", item)
	}
	if item.Credentials["access_token"] != "新访问令牌" ||
		item.Credentials["refresh_token"] != "旋转后的刷新令牌" ||
		item.Credentials["project_id"] != "自动发现的项目" {
		t.Fatalf("自动补全凭证不完整: %#v", item.Credentials)
	}
	if item.Credentials["credential_origin"] != "import_refresh" || item.Credentials["auth_kind"] != TypeOAuth {
		t.Fatalf("导入来源元数据不完整: %#v", item.Credentials)
	}
}

func TestImportAntigravityRefresh拒绝不完整导入(t *testing.T) {
	tests := []struct {
		name       string
		refresh    string
		refresher  OAuthCredentialRefresher
		wantErrSub string
	}{
		{name: "空RT", refresh: " ", wantErrSub: "refresh_token 不能为空"},
		{name: "缺少导入器", refresh: "刷新令牌", refresher: &oauthRefresherStub{}, wantErrSub: "导入器不可用"},
		{
			name:    "换票失败",
			refresh: "刷新令牌",
			refresher: &antigravityImporterStub{
				err: errors.New("上游拒绝刷新令牌"),
			},
			wantErrSub: "换票失败",
		},
		{
			name:    "缺少项目ID",
			refresh: "刷新令牌",
			refresher: &antigravityImporterStub{credentials: map[string]string{
				"access_token": "访问令牌",
			}},
			wantErrSub: "缺少 project_id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &antigravityImportRepo{}
			service := NewService(repo, antigravityImportTestSecret)
			service.SetOAuthCredentialRefresher(tt.refresher)
			_, err := service.ImportAntigravityRefresh(context.Background(), OAuthStartInput{}, tt.refresh)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("错误 = %v，期望包含 %q", err, tt.wantErrSub)
			}
			if repo.createInput != nil || repo.updateInput != nil {
				t.Fatal("导入未完成时不应写入账号")
			}
		})
	}
}

func TestImportAntigravityRefresh重新授权已有账号(t *testing.T) {
	repo := &antigravityImportRepo{}
	service := NewService(repo, antigravityImportTestSecret)
	oldEncrypted, _, err := service.prepareCredentials(map[string]string{
		"access_token":  "旧访问令牌",
		"refresh_token": "旧刷新令牌",
		"project_id":    "旧项目",
	})
	if err != nil {
		t.Fatalf("准备旧凭证失败: %v", err)
	}
	repo.item = Account{
		ID:             9,
		Name:           "保留原账号名称",
		Platform:       "antigravity",
		Type:           TypeOAuth,
		CredentialsEnc: oldEncrypted,
		State:          StateDisabled,
		GroupIDs:       []int64{8},
	}
	service.SetOAuthCredentialRefresher(&antigravityImporterStub{credentials: map[string]string{
		"access_token":  "新访问令牌",
		"refresh_token": "新刷新令牌",
		"project_id":    "新项目",
	}})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	item, err := service.ImportAntigravityRefresh(ctx, OAuthStartInput{AccountID: 9}, "重新授权令牌")
	if err != nil {
		t.Fatalf("重新授权失败: %v", err)
	}
	if repo.createInput != nil || repo.updateInput == nil {
		t.Fatalf("重新授权应更新原账号: create=%v update=%v", repo.createInput, repo.updateInput)
	}
	if item.ID != 9 || item.Name != "保留原账号名称" || item.State != StateActive {
		t.Fatalf("重新授权破坏了账号信息: %+v", item)
	}
	if item.Credentials["access_token"] != "新访问令牌" || item.Credentials["project_id"] != "新项目" {
		t.Fatalf("重新授权凭证未更新: %#v", item.Credentials)
	}
}

func TestAntigravity浏览器授权通过CPA补全项目后落库(t *testing.T) {
	repo := &antigravityImportRepo{}
	service := NewService(repo, antigravityImportTestSecret)
	preparer := &antigravityCredentialPreparerStub{credentials: map[string]string{
		"access_token":      "浏览器访问令牌",
		"refresh_token":     "浏览器刷新令牌",
		"project_id":        "CPA 自动发现的项目",
		"credential_origin": "oauth",
	}}
	service.SetOAuthCredentialRefresher(preparer)
	entry := &oauthSessionEntry{
		public: OAuthSession{Platform: "antigravity", Status: OAuthStatusPending},
		input: OAuthStartInput{
			Name:     "Antigravity 浏览器账号",
			ProxyURL: "http://127.0.0.1:7890",
		},
		done: make(chan struct{}),
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 阻止订阅信息补全访问网络；CPA 准备结果不受影响。
	err := service.finishOAuthWithCredentials(ctx, entry, map[string]string{
		"access_token":  "浏览器访问令牌",
		"refresh_token": "浏览器刷新令牌",
	})
	if err != nil {
		t.Fatalf("finishOAuthWithCredentials() 错误: %v", err)
	}
	if preparer.calls != 1 || preparer.platform != "antigravity" || preparer.accountType != TypeOAuth {
		t.Fatalf("CPA 授权准备参数不正确: %+v", preparer)
	}
	if preparer.accessToken != "浏览器访问令牌" || preparer.proxyURL != "http://127.0.0.1:7890" {
		t.Fatalf("浏览器凭证或代理未传给 CPA: %+v", preparer)
	}
	item, err := service.FindByID(context.Background(), 21, LoadOptions{})
	if err != nil {
		t.Fatalf("读取已创建账号失败: %v", err)
	}
	if item.Credentials["project_id"] != "CPA 自动发现的项目" ||
		item.Credentials["access_token"] != "浏览器访问令牌" {
		t.Fatalf("浏览器授权落库凭证不完整: %#v", item.Credentials)
	}
}

func TestAntigravity浏览器授权拒绝CPA准备失败或项目缺失(t *testing.T) {
	tests := []struct {
		name       string
		refresher  OAuthCredentialRefresher
		wantErrSub string
	}{
		{name: "缺少准备器", refresher: &oauthRefresherStub{}, wantErrSub: "授权准备器不可用"},
		{
			name: "CPA准备失败",
			refresher: &antigravityCredentialPreparerStub{
				err: errors.New("项目发现接口不可用"),
			},
			wantErrSub: "CPA 授权准备失败",
		},
		{
			name: "准备后仍缺项目",
			refresher: &antigravityCredentialPreparerStub{credentials: map[string]string{
				"access_token": "浏览器访问令牌",
			}},
			wantErrSub: "缺少 project_id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &antigravityImportRepo{}
			service := NewService(repo, antigravityImportTestSecret)
			service.SetOAuthCredentialRefresher(tt.refresher)
			entry := &oauthSessionEntry{
				public: OAuthSession{Platform: "antigravity", Status: OAuthStatusPending},
				done:   make(chan struct{}),
			}
			err := service.finishOAuthWithCredentials(context.Background(), entry, map[string]string{
				"access_token": "浏览器访问令牌",
			})
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("错误 = %v，期望包含 %q", err, tt.wantErrSub)
			}
			if repo.createInput != nil || repo.updateInput != nil {
				t.Fatal("CPA 授权准备未完成时不应写入账号")
			}
		})
	}
}

type antigravityImporterStub struct {
	calls        int
	platform     string
	accountType  string
	refreshToken string
	proxyURL     string
	credentials  map[string]string
	err          error
}

type antigravityCredentialPreparerStub struct {
	calls       int
	platform    string
	accountType string
	accessToken string
	proxyURL    string
	credentials map[string]string
	err         error
}

func (s *antigravityCredentialPreparerStub) RefreshAccountCredentials(
	context.Context,
	int,
	string,
	string,
	string,
	map[string]string,
	string,
) (map[string]string, error) {
	return nil, errors.New("本测试不调用账号刷新")
}

func (s *antigravityCredentialPreparerStub) PrepareOAuthCredentials(
	_ context.Context,
	platform string,
	accountType string,
	credentials map[string]string,
	proxyURL string,
) (map[string]string, error) {
	s.calls++
	s.platform = platform
	s.accountType = accountType
	s.accessToken = credentials["access_token"]
	s.proxyURL = proxyURL
	return cloneStringMap(s.credentials), s.err
}

func (s *antigravityImporterStub) RefreshAccountCredentials(
	context.Context,
	int,
	string,
	string,
	string,
	map[string]string,
	string,
) (map[string]string, error) {
	return nil, errors.New("本测试不调用账号刷新")
}

func (s *antigravityImporterStub) ImportOAuthCredentials(
	_ context.Context,
	platform string,
	accountType string,
	credentials map[string]string,
	proxyURL string,
) (map[string]string, error) {
	s.calls++
	s.platform = platform
	s.accountType = accountType
	s.refreshToken = credentials["refresh_token"]
	s.proxyURL = proxyURL
	return cloneStringMap(s.credentials), s.err
}

type antigravityImportRepo struct {
	stubAccountRepo
	item        Account
	createInput *PersistCreateInput
	updateInput *PersistUpdateInput
}

func (r *antigravityImportRepo) FindByID(_ context.Context, id int, _ LoadOptions) (Account, error) {
	if r.item.ID != id {
		return Account{}, ErrAccountNotFound
	}
	return r.item, nil
}

func (r *antigravityImportRepo) Create(_ context.Context, input PersistCreateInput) (Account, error) {
	r.createInput = &input
	r.item = Account{
		ID:             21,
		Name:           input.Name,
		Platform:       input.Platform,
		Type:           input.Type,
		CredentialsEnc: input.CredentialsEnc,
		Email:          input.Email,
		State:          StateActive,
		Priority:       input.Priority,
		Weight:         input.Weight,
		MaxConcurrency: input.MaxConcurrency,
		RateMultiplier: input.RateMultiplier,
		GroupIDs:       append([]int64(nil), input.GroupIDs...),
	}
	return r.item, nil
}

func (r *antigravityImportRepo) Update(_ context.Context, id int, input PersistUpdateInput) (Account, error) {
	if r.item.ID != id {
		return Account{}, ErrAccountNotFound
	}
	r.updateInput = &input
	if input.Type != nil {
		r.item.Type = *input.Type
	}
	if input.CredentialsEnc != nil {
		r.item.CredentialsEnc = *input.CredentialsEnc
	}
	if input.Email != nil {
		r.item.Email = *input.Email
	}
	if input.State != nil {
		r.item.State = *input.State
	}
	return r.item, nil
}
