package account

import (
	"context"
	"encoding/base64"
	"net/http"
	"strconv"
	"testing"
	"time"
)

func TestCredentialExpirationSupportsCodexTimestampAndJWTForms(t *testing.T) {
	want := time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)
	seconds := want.Unix()
	tests := []struct {
		name        string
		credentials map[string]string
	}{
		{name: "unix seconds", credentials: map[string]string{"expires_at": strconv.FormatInt(seconds, 10)}},
		{name: "unix milliseconds", credentials: map[string]string{"expires_at": strconv.FormatInt(seconds*1000, 10)}},
		{name: "JWT numeric exp", credentials: map[string]string{"access_token": "header." + base64.RawURLEncoding.EncodeToString([]byte(`{"exp":`+strconv.FormatInt(seconds, 10)+`}`)) + ".signature"}},
		{name: "JWT string exp", credentials: map[string]string{"access_token": "header." + base64.RawURLEncoding.EncodeToString([]byte(`{"exp":"`+strconv.FormatInt(seconds, 10)+`"}`)) + ".signature"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := credentialExpiration(test.credentials)
			if !ok || !got.Equal(want) {
				t.Fatalf("credentialExpiration() = %v, %v; want %v, true", got, ok, want)
			}
		})
	}
}

func TestOAuthCredentialsNeedRefresh覆盖主要授权平台(t *testing.T) {
	now := time.Date(2026, 8, 7, 8, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		platform string
		expiry   time.Duration
		want     bool
	}{
		{name: "xAI五分钟内刷新", platform: "xai", expiry: 4 * time.Minute, want: true},
		{name: "Kimi五分钟外不刷新", platform: "kimi", expiry: 30 * time.Minute, want: false},
		{name: "Claude四小时内刷新", platform: "claude", expiry: 3 * time.Hour, want: true},
		{name: "Codex五天内刷新", platform: "codex", expiry: 4 * 24 * time.Hour, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item := Account{Platform: tt.platform, Type: TypeOAuth, Credentials: map[string]string{
				"access_token":  "旧令牌",
				"refresh_token": "刷新令牌",
				"expired":       now.Add(tt.expiry).Format(time.RFC3339),
			}}
			if got := oauthCredentialsNeedRefresh(item, now); got != tt.want {
				t.Fatalf("oauthCredentialsNeedRefresh() = %v，期望 %v", got, tt.want)
			}
		})
	}
}

func TestRefreshUsage认证失败后刷新并重试(t *testing.T) {
	const secret = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"
	repo := &oauthRefreshRepo{}
	service := NewService(repo, secret)
	credentials := map[string]string{
		"access_token":  "旧令牌",
		"refresh_token": "刷新令牌",
	}
	encrypted, _, err := service.prepareCredentials(credentials)
	if err != nil {
		t.Fatalf("加密测试凭证失败: %v", err)
	}
	repo.item = Account{
		ID:             7,
		Name:           "Grok OAuth",
		Platform:       "xai",
		Type:           TypeOAuth,
		CredentialsEnc: encrypted,
	}
	refresher := &oauthRefresherStub{credentials: map[string]string{
		"access_token":  "新令牌",
		"refresh_token": "新刷新令牌",
		"expired":       time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	}}
	service.SetOAuthCredentialRefresher(refresher)

	fetchCalls := 0
	service.usageFetcher = func(_ context.Context, _ string, _ string, got map[string]string, _ string) (UsageSnapshot, error) {
		fetchCalls++
		if fetchCalls == 1 {
			return UsageSnapshot{}, &accountUpstreamHTTPError{
				status: http.StatusForbidden,
				body:   []byte(`{"code":"unauthenticated:bad-credentials","error":"OAuth2 access token expired"}`),
				label:  "xAI billing",
			}
		}
		if got["access_token"] != "新令牌" {
			t.Fatalf("重试未使用刷新后的令牌：%v", got)
		}
		return UsageSnapshot{
			CapturedAt: time.Now().UTC(),
			Windows:    []UsageWindow{{Key: "weekly", UsedPercent: 20}},
		}, nil
	}

	snapshot, updated, err := service.RefreshUsage(context.Background(), repo.item.ID)
	if err != nil {
		t.Fatalf("RefreshUsage() 错误: %v", err)
	}
	if fetchCalls != 2 || refresher.calls != 1 {
		t.Fatalf("调用次数不符合预期：fetch=%d refresh=%d", fetchCalls, refresher.calls)
	}
	if len(snapshot.Windows) != 1 || updated.Credentials["access_token"] != "新令牌" {
		t.Fatalf("刷新结果不完整：snapshot=%+v account=%+v", snapshot, updated)
	}
}

type oauthRefresherStub struct {
	calls       int
	credentials map[string]string
}

func (s *oauthRefresherStub) RefreshAccountCredentials(
	context.Context,
	int,
	string,
	string,
	string,
	map[string]string,
	string,
) (map[string]string, error) {
	s.calls++
	return cloneStringMap(s.credentials), nil
}

type oauthRefreshRepo struct {
	stubAccountRepo
	item Account
}

func (r *oauthRefreshRepo) FindByID(context.Context, int, LoadOptions) (Account, error) {
	return r.item, nil
}

func (r *oauthRefreshRepo) Update(_ context.Context, _ int, input PersistUpdateInput) (Account, error) {
	if input.CredentialsEnc != nil {
		r.item.CredentialsEnc = *input.CredentialsEnc
	}
	if input.Email != nil {
		r.item.Email = *input.Email
	}
	if input.HasExtra {
		r.item.Extra = input.Extra
	}
	return r.item, nil
}
