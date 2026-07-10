package handler

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	appapikey "github.com/DouDOU-start/airgate-core/internal/app/apikey"
	appoauth "github.com/DouDOU-start/airgate-core/internal/app/oauth"
	"github.com/DouDOU-start/airgate-core/internal/infra/store"
)

// oauth_handler_routes_test.go：OAuth 协议端点的 HTTP 集成测试。
// 覆盖 gin handler（RFC 6749 原始形态、错误映射、no-store 头）+ 真实 Redis grant store
// （miniredis：授权码 GETDEL 一次性、令牌 TTL 到期失效），补齐 service 用例层单测之上的集成缺口。

// ==================== 测试替身（handler 层专用） ====================

type stubOAuthRepo struct {
	clients map[string]appoauth.Client
}

func (s *stubOAuthRepo) List(context.Context) ([]appoauth.Client, error) { return nil, nil }
func (s *stubOAuthRepo) FindByClientID(_ context.Context, clientID string) (appoauth.Client, error) {
	if c, ok := s.clients[clientID]; ok {
		return c, nil
	}
	return appoauth.Client{}, appoauth.ErrClientNotFound
}
func (s *stubOAuthRepo) Create(context.Context, string, string, string, appoauth.ClientMutation) (appoauth.Client, error) {
	return appoauth.Client{}, appoauth.ErrClientNotFound
}
func (s *stubOAuthRepo) Update(context.Context, int, appoauth.ClientMutation) (appoauth.Client, error) {
	return appoauth.Client{}, appoauth.ErrClientNotFound
}
func (s *stubOAuthRepo) UpdateSecret(context.Context, int, string, string) (appoauth.Client, error) {
	return appoauth.Client{}, appoauth.ErrClientNotFound
}
func (s *stubOAuthRepo) Delete(context.Context, int) error                  { return nil }
func (s *stubOAuthRepo) ListNav(context.Context) ([]appoauth.Client, error) { return nil, nil }

type stubOAuthUsers struct{}

func (stubOAuthUsers) BasicInfo(_ context.Context, id int) (appoauth.UserInfo, error) {
	return appoauth.UserInfo{ID: id, Email: "u@example.com", Username: "u", Role: "user", Status: "active"}, nil
}

type stubOAuthGroups struct{}

func (stubOAuthGroups) AvailableForUser(context.Context, int) ([]appoauth.GroupInfo, error) {
	return []appoauth.GroupInfo{
		{ID: 1, Name: "default", RateMultiplier: 1},
		{ID: 2, Name: "vip", RateMultiplier: 2, Note: "官转"},
	}, nil
}

// stubOAuthProvisioner 模拟 apikey.ProvisionForClient 的 get-or-create 幂等语义。
type stubOAuthProvisioner struct {
	keys map[string]string // "userID/clientID" → 明文 key
	err  error             // 非 nil 时直接返回该错误
}

func (s *stubOAuthProvisioner) ProvisionForClient(_ context.Context, userID int, clientID, _ string, groupID int) (string, string, int, bool, error) {
	if s.err != nil {
		return "", "", 0, false, s.err
	}
	if groupID == 0 {
		groupID = 1 // 模拟默认分组解析
	}
	if s.keys == nil {
		s.keys = map[string]string{}
	}
	id := strings.Join([]string{hex.EncodeToString([]byte{byte(userID)}), clientID, strconv.Itoa(groupID)}, "/")
	if key, ok := s.keys[id]; ok {
		return key, "hint", groupID, false, nil
	}
	key := fmt.Sprintf("sk-test-%s-g%d", clientID, groupID)
	s.keys[id] = key
	return key, "hint", groupID, true, nil
}

// ==================== 装配 ====================

type oauthTestEnv struct {
	router *gin.Engine
	mr     *miniredis.Miniredis
}

func newOAuthTestEnv(t *testing.T, prov appoauth.KeyProvisioner) *oauthTestEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	secretSum := sha256.Sum256([]byte("secret"))
	repo := &stubOAuthRepo{clients: map[string]appoauth.Client{
		"ac_test": {
			ClientID:     "ac_test",
			SecretHash:   hex.EncodeToString(secretSum[:]),
			Name:         "对话",
			RedirectURIs: []string{"https://chat.example.com/callback"},
			Enabled:      true,
		},
	}}
	svc := appoauth.NewService(repo, store.NewOAuthGrantStore(rdb), stubOAuthUsers{}, stubOAuthGroups{}, prov)
	h := NewOAuthHandler(svc)

	router := gin.New()
	// 模拟 JWT 中间件注入的登录态（用户 1）。
	authorized := router.Group("/api/v1", func(c *gin.Context) { c.Set("user_id", 1) })
	authorized.POST("/oauth/authorize", h.Authorize)
	router.POST("/oauth/token", h.Token)
	router.GET("/oauth/userinfo", h.UserInfo)
	router.POST("/oauth/provision-key", h.ProvisionKey)
	return &oauthTestEnv{router: router, mr: mr}
}

func s256Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// authorizeViaHTTP 走 POST /api/v1/oauth/authorize 签发授权码。
func (e *oauthTestEnv) authorizeViaHTTP(t *testing.T, verifier string) string {
	t.Helper()
	body := `{"client_id":"ac_test","redirect_uri":"https://chat.example.com/callback",` +
		`"state":"st","code_challenge":"` + s256Challenge(verifier) + `","code_challenge_method":"S256"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/oauth/authorize", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("authorize status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Code int `json:"code"`
		Data struct {
			Code  string `json:"code"`
			State string `json:"state"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("解析 authorize 响应失败: %v", err)
	}
	if envelope.Data.Code == "" || envelope.Data.State != "st" {
		t.Fatalf("authorize 响应异常: %s", rec.Body.String())
	}
	return envelope.Data.Code
}

// exchangeToken 走 POST /oauth/token（form 表单）。
func (e *oauthTestEnv) exchangeToken(t *testing.T, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

func tokenForm(code, verifier string) url.Values {
	return url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {"https://chat.example.com/callback"},
		"client_id":     {"ac_test"},
		"client_secret": {"secret"},
		"code_verifier": {verifier},
	}
}

func (e *oauthTestEnv) bearerRequest(method, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

// oauthErrCode 解析 RFC 6749 错误体的 error 码。
func oauthErrCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析错误体失败: %v; body = %s", err, rec.Body.String())
	}
	return body.Error
}

// ==================== 用例 ====================

// TestOAuthTokenFlowHTTP 授权码 → /oauth/token → /oauth/userinfo 全链路（真 Redis 存取）。
func TestOAuthTokenFlowHTTP(t *testing.T) {
	env := newOAuthTestEnv(t, &stubOAuthProvisioner{})
	code := env.authorizeViaHTTP(t, "verifier-value")

	rec := env.exchangeToken(t, tokenForm(code, "verifier-value"))
	if rec.Code != http.StatusOK {
		t.Fatalf("token status = %d, body = %s", rec.Code, rec.Body.String())
	}
	// RFC 6749 §5.1：令牌响应禁止缓存。
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	var token struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &token); err != nil {
		t.Fatalf("解析 token 响应失败: %v", err)
	}
	if !strings.HasPrefix(token.AccessToken, "oat_") || token.TokenType != "Bearer" || token.ExpiresIn <= 0 {
		t.Fatalf("token 响应异常: %+v", token)
	}

	// 令牌可解析出用户身份。
	infoRec := env.bearerRequest(http.MethodGet, "/oauth/userinfo", token.AccessToken)
	if infoRec.Code != http.StatusOK {
		t.Fatalf("userinfo status = %d, body = %s", infoRec.Code, infoRec.Body.String())
	}
	var info struct {
		Sub    string `json:"sub"`
		Email  string `json:"email"`
		Groups []struct {
			ID             int     `json:"id"`
			Name           string  `json:"name"`
			RateMultiplier float64 `json:"rate_multiplier"`
		} `json:"groups"`
	}
	if err := json.Unmarshal(infoRec.Body.Bytes(), &info); err != nil {
		t.Fatalf("解析 userinfo 失败: %v", err)
	}
	if info.Sub != "1" || info.Email != "u@example.com" {
		t.Fatalf("userinfo = %+v, want sub=1", info)
	}
	// userinfo 附带用户可用分组（应用据此做按组领 key / 分组货架）。
	if len(info.Groups) != 2 || info.Groups[0].Name != "default" || info.Groups[1].RateMultiplier != 2 {
		t.Fatalf("userinfo groups = %+v, want [default, vip]", info.Groups)
	}
}

// TestOAuthTokenCodeSingleUseHTTP 授权码经 Redis GETDEL 一次性：重放兑换返回 invalid_grant。
func TestOAuthTokenCodeSingleUseHTTP(t *testing.T) {
	env := newOAuthTestEnv(t, &stubOAuthProvisioner{})
	code := env.authorizeViaHTTP(t, "verifier-value")

	if rec := env.exchangeToken(t, tokenForm(code, "verifier-value")); rec.Code != http.StatusOK {
		t.Fatalf("首次兑换 status = %d, body = %s", rec.Code, rec.Body.String())
	}
	rec := env.exchangeToken(t, tokenForm(code, "verifier-value"))
	if rec.Code != http.StatusBadRequest || oauthErrCode(t, rec) != "invalid_grant" {
		t.Fatalf("重放兑换 = (%d, %s), want (400, invalid_grant)", rec.Code, rec.Body.String())
	}
}

// TestOAuthTokenRejectionsHTTP 协议端点错误映射：PKCE 不匹配 / secret 错误 / grant_type 不支持。
func TestOAuthTokenRejectionsHTTP(t *testing.T) {
	cases := []struct {
		name     string
		mutate   func(url.Values)
		wantHTTP int
		wantCode string
	}{
		{"PKCE verifier 不匹配", func(f url.Values) { f.Set("code_verifier", "wrong") }, 400, "invalid_grant"},
		{"secret 错误", func(f url.Values) { f.Set("client_secret", "wrong") }, 401, "invalid_client"},
		{"grant_type 不支持", func(f url.Values) { f.Set("grant_type", "client_credentials") }, 400, "unsupported_grant_type"},
		{"redirect_uri 不一致", func(f url.Values) { f.Set("redirect_uri", "https://evil.example.com/cb") }, 400, "invalid_grant"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newOAuthTestEnv(t, &stubOAuthProvisioner{})
			form := tokenForm(env.authorizeViaHTTP(t, "verifier-value"), "verifier-value")
			tc.mutate(form)
			rec := env.exchangeToken(t, form)
			if rec.Code != tc.wantHTTP || oauthErrCode(t, rec) != tc.wantCode {
				t.Fatalf("= (%d, %s), want (%d, %s)", rec.Code, rec.Body.String(), tc.wantHTTP, tc.wantCode)
			}
		})
	}
}

// TestOAuthTokenExpiryHTTP 令牌 TTL 写入 Redis 生效：快进过期后 userinfo 返回 invalid_token。
func TestOAuthTokenExpiryHTTP(t *testing.T) {
	env := newOAuthTestEnv(t, &stubOAuthProvisioner{})
	code := env.authorizeViaHTTP(t, "verifier-value")
	rec := env.exchangeToken(t, tokenForm(code, "verifier-value"))
	var token struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &token); err != nil || token.AccessToken == "" {
		t.Fatalf("解析 token 失败: %v; body = %s", err, rec.Body.String())
	}

	env.mr.FastForward(time.Duration(token.ExpiresIn+1) * time.Second)

	infoRec := env.bearerRequest(http.MethodGet, "/oauth/userinfo", token.AccessToken)
	if infoRec.Code != http.StatusUnauthorized || oauthErrCode(t, infoRec) != "invalid_token" {
		t.Fatalf("过期令牌 = (%d, %s), want (401, invalid_token)", infoRec.Code, infoRec.Body.String())
	}
}

// TestOAuthProvisionKeyHTTP provision-key 幂等 + no-store 头 + 未带令牌 401。
func TestOAuthProvisionKeyHTTP(t *testing.T) {
	env := newOAuthTestEnv(t, &stubOAuthProvisioner{})
	code := env.authorizeViaHTTP(t, "verifier-value")
	rec := env.exchangeToken(t, tokenForm(code, "verifier-value"))
	var token struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &token); err != nil || token.AccessToken == "" {
		t.Fatalf("解析 token 失败: %v", err)
	}

	type provisionResp struct {
		APIKey  string `json:"api_key"`
		GroupID int    `json:"group_id"`
		Created bool   `json:"created"`
	}
	provision := func(body string) (provisionResp, *httptest.ResponseRecorder) {
		var reader *strings.Reader
		if body == "" {
			reader = strings.NewReader("")
		} else {
			reader = strings.NewReader(body)
		}
		req := httptest.NewRequest(http.MethodPost, "/oauth/provision-key", reader)
		req.Header.Set("Authorization", "Bearer "+token.AccessToken)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		r := httptest.NewRecorder()
		env.router.ServeHTTP(r, req)
		var out provisionResp
		if r.Code == http.StatusOK {
			if err := json.Unmarshal(r.Body.Bytes(), &out); err != nil {
				t.Fatalf("解析 provision 响应失败: %v", err)
			}
		}
		return out, r
	}

	first, firstRec := provision("")
	if firstRec.Code != http.StatusOK || !first.Created || first.APIKey == "" || first.GroupID != 1 {
		t.Fatalf("首次 provision = (%d, %+v), want created=true group=1", firstRec.Code, first)
	}
	// 明文 key 响应禁止缓存。
	if got := firstRec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}

	second, secondRec := provision("")
	if secondRec.Code != http.StatusOK || second.Created || second.APIKey != first.APIKey {
		t.Fatalf("二次 provision = (%d, %+v), want 幂等返回同 key", secondRec.Code, second)
	}

	// 指定另一分组 → 领到另一把 key（幂等键含分组）。
	other, otherRec := provision(`{"group_id":2}`)
	if otherRec.Code != http.StatusOK || !other.Created || other.GroupID != 2 || other.APIKey == first.APIKey {
		t.Fatalf("跨组 provision = (%d, %+v), want 新 key group=2", otherRec.Code, other)
	}

	// 未带令牌 → 401 invalid_token。
	if r := env.bearerRequest(http.MethodPost, "/oauth/provision-key", ""); r.Code != http.StatusUnauthorized {
		t.Fatalf("无令牌 provision status = %d, want 401", r.Code)
	}
}

// TestOAuthProvisionKeyDisabledHTTP 用户已禁用该应用的 key：403 access_denied（应用侧据此拦截/放行）。
func TestOAuthProvisionKeyDisabledHTTP(t *testing.T) {
	env := newOAuthTestEnv(t, &stubOAuthProvisioner{err: appapikey.ErrProvisionedKeyDisabled})
	code := env.authorizeViaHTTP(t, "verifier-value")
	rec := env.exchangeToken(t, tokenForm(code, "verifier-value"))
	var token struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &token); err != nil || token.AccessToken == "" {
		t.Fatalf("解析 token 失败: %v", err)
	}

	r := env.bearerRequest(http.MethodPost, "/oauth/provision-key", token.AccessToken)
	if r.Code != http.StatusForbidden || oauthErrCode(t, r) != "access_denied" {
		t.Fatalf("禁用 key provision = (%d, %s), want (403, access_denied)", r.Code, r.Body.String())
	}
}
