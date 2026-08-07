package cpa

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

const defaultOAuthRefreshLead = 5 * time.Minute

type accountRefreshLock struct {
	mu sync.Mutex
}

type refreshedAuthCache struct {
	auth                 *coreauth.Auth
	previousAccessToken  string
	previousRefreshToken string
}

type requestAuthPreparer interface {
	ShouldPrepareRequestAuth(auth *coreauth.Auth) bool
	PrepareRequestAuth(ctx context.Context, auth *coreauth.Auth) (*coreauth.Auth, error)
}

// ImportOAuthCredentials 在账号落库前使用临时 Auth 完成 RT 换票。
// Antigravity executor 还会通过 PrepareRequestAuth 自动发现并校验 project_id。
func (b *Bridge) ImportOAuthCredentials(
	ctx context.Context,
	platform string,
	accountType string,
	credentials map[string]string,
	proxyURL string,
) (map[string]string, error) {
	auth, err := mapAuthWithID(AccountAuthInput{
		Name:        "OAuth RT 导入",
		Platform:    platform,
		Type:        accountType,
		Credentials: credentials,
		ProxyURL:    proxyURL,
	}, "airgate-import-"+uuid.NewString())
	if err != nil {
		return nil, err
	}
	// 临时 Auth 只用于本次导入，完成后清理并发锁和刷新缓存，避免长期导入累积内存。
	defer b.refreshLocks.Delete(auth.ID)
	defer b.refreshedAuths.Delete(auth.ID)
	if !authHasRefreshCredential(auth) {
		return nil, fmt.Errorf("账号缺少 refresh_token，无法导入 OAuth 凭证")
	}
	executor, err := b.EnsureExecutor(auth.Provider)
	if err != nil {
		return nil, err
	}
	refreshed, err := b.refreshAuth(ctx, executor, auth)
	if err != nil {
		return nil, err
	}
	if preparer, ok := executor.(requestAuthPreparer); ok && preparer.ShouldPrepareRequestAuth(refreshed) {
		prepared, prepareErr := preparer.PrepareRequestAuth(ctx, refreshed.Clone())
		if prepareErr != nil {
			return nil, prepareErr
		}
		if prepared != nil {
			refreshed = prepared
		}
	}
	result := CredentialsFromAuth(refreshed)
	if strings.TrimSpace(result["access_token"]) == "" {
		return nil, fmt.Errorf("OAuth 换票后缺少 access_token")
	}
	return result, nil
}

// RefreshAccountCredentials 强制刷新一个指定账号的 OAuth 凭证。
// 账号管理中的连接测试和用量查询通过该入口复用 CPA 各平台 executor 的刷新实现。
func (b *Bridge) RefreshAccountCredentials(
	ctx context.Context,
	accountID int,
	name string,
	platform string,
	accountType string,
	credentials map[string]string,
	proxyURL string,
) (map[string]string, error) {
	auth, err := MapAuth(AccountAuthInput{
		AccountID:   accountID,
		Name:        name,
		Platform:    platform,
		Type:        accountType,
		Credentials: credentials,
		ProxyURL:    proxyURL,
	})
	if err != nil {
		return nil, err
	}
	if !authHasRefreshCredential(auth) {
		return nil, fmt.Errorf("账号缺少 refresh_token，无法刷新 OAuth 凭证")
	}
	ex, err := b.EnsureExecutor(auth.Provider)
	if err != nil {
		return nil, err
	}
	refreshed, err := b.refreshAuth(ctx, ex, auth)
	if err != nil {
		return nil, err
	}
	return CredentialsFromAuth(refreshed), nil
}

func (b *Bridge) refreshAuth(
	ctx context.Context,
	ex coreauth.ProviderExecutor,
	auth *coreauth.Auth,
) (*coreauth.Auth, error) {
	if b == nil || ex == nil || auth == nil {
		return nil, fmt.Errorf("OAuth 刷新参数不完整")
	}
	if !authHasRefreshCredential(auth) {
		return nil, fmt.Errorf("账号缺少 refresh_token，无法刷新 OAuth 凭证")
	}

	lockValue, _ := b.refreshLocks.LoadOrStore(auth.ID, &accountRefreshLock{})
	lock, _ := lockValue.(*accountRefreshLock)
	if lock == nil {
		lock = &accountRefreshLock{}
		b.refreshLocks.Store(auth.ID, lock)
	}
	lock.mu.Lock()
	defer lock.mu.Unlock()

	// 并发请求携带同一个旧 access_token 到达时，复用前一个请求刚换出的凭证，
	// 避免旋转型 refresh_token 被重复消费。
	failedAccessToken := authMetadataString(auth, "access_token")
	failedRefreshToken := authMetadataString(auth, "refresh_token")
	if cachedRaw, ok := b.refreshedAuths.Load(auth.ID); ok {
		if cached, okCached := cachedRaw.(refreshedAuthCache); okCached && cached.auth != nil {
			if (failedAccessToken != "" && cached.previousAccessToken == failedAccessToken) ||
				(failedAccessToken == "" && failedRefreshToken != "" && cached.previousRefreshToken == failedRefreshToken) {
				return cached.auth.Clone(), nil
			}
		}
	}

	refreshed, err := ex.Refresh(ctx, auth.Clone())
	if err != nil {
		return nil, err
	}
	if refreshed == nil {
		return nil, fmt.Errorf("OAuth 刷新返回空凭证")
	}
	if authMetadataString(refreshed, "access_token") == "" {
		return nil, fmt.Errorf("OAuth 刷新后缺少 access_token")
	}
	b.refreshedAuths.Store(auth.ID, refreshedAuthCache{
		auth:                 refreshed.Clone(),
		previousAccessToken:  failedAccessToken,
		previousRefreshToken: failedRefreshToken,
	})
	return refreshed, nil
}

func authNeedsProactiveRefresh(auth *coreauth.Auth, now time.Time) bool {
	if auth == nil || !authHasRefreshCredential(auth) {
		return false
	}
	if authMetadataString(auth, "access_token") == "" {
		return true
	}
	expiry, ok := auth.ExpirationTime()
	if !ok || expiry.IsZero() {
		return false
	}
	lead := defaultOAuthRefreshLead
	if providerLead := coreauth.ProviderRefreshLead(auth.Provider, auth.Runtime); providerLead != nil && *providerLead > 0 {
		lead = *providerLead
	}
	return !expiry.After(now.Add(lead))
}

func authHasRefreshCredential(auth *coreauth.Auth) bool {
	return authMetadataString(auth, "refresh_token") != ""
}

func authMetadataString(auth *coreauth.Auth, key string) string {
	if auth == nil || auth.Metadata == nil {
		return ""
	}
	value, _ := auth.Metadata[key].(string)
	return strings.TrimSpace(value)
}

func isRefreshableAuthError(provider string, err error) bool {
	if err == nil {
		return false
	}
	info := ClassifyError(err)
	return isRefreshableAuthFailure(provider, info.StatusCode, info.Message+"\n"+string(info.Body))
}

func isRefreshableAuthResult(provider string, result ForwardResult) bool {
	if result.Written || result.StatusCode == 0 {
		return false
	}
	return isRefreshableAuthFailure(provider, result.StatusCode, string(result.Body))
}

func isRefreshableAuthFailure(provider string, statusCode int, raw string) bool {
	if statusCode == 401 {
		return true
	}
	if statusCode != 403 {
		return false
	}
	lower := strings.ToLower(raw)
	strongSignals := []string{
		"bad-credentials",
		"bad_credentials",
		"invalid_token",
		"token_expired",
		"expired token",
		"invalid or expired token",
		"authentication_error",
	}
	for _, signal := range strongSignals {
		if strings.Contains(lower, signal) {
			return true
		}
	}
	// 部分 Google/xAI 接口用 403 + UNAUTHENTICATED 表达令牌失效；
	// 必须同时出现 token/credential 语义，避免把普通权限不足误判成可刷新。
	if strings.Contains(lower, "unauthenticated") &&
		(strings.Contains(lower, "token") || strings.Contains(lower, "credential")) {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(provider), "xai") &&
		strings.Contains(lower, "oauth2 access token")
}

// IsRefreshableAuthFailure 判断上游失败是否明确表示 OAuth 凭证已失效。
// 供账号管理中的直连测试和用量查询复用相同判定口径。
func IsRefreshableAuthFailure(provider string, statusCode int, raw string) bool {
	return isRefreshableAuthFailure(provider, statusCode, raw)
}
