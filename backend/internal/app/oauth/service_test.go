package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"testing"
	"time"
)

// ==================== 测试替身 ====================

type stubRepo struct {
	clients map[string]Client // key: client_id
}

func (s *stubRepo) List(context.Context) ([]Client, error)        { return nil, nil }
func (s *stubRepo) FindByID(context.Context, int) (Client, error) { return Client{}, ErrClientNotFound }
func (s *stubRepo) FindByClientID(_ context.Context, clientID string) (Client, error) {
	if c, ok := s.clients[clientID]; ok {
		return c, nil
	}
	return Client{}, ErrClientNotFound
}
func (s *stubRepo) Create(_ context.Context, clientID, secretHash, secretHint string, m ClientMutation) (Client, error) {
	c := Client{ClientID: clientID, SecretHash: secretHash, SecretHint: secretHint, Name: m.Name, RedirectURIs: m.RedirectURIs, FirstParty: m.FirstParty, Enabled: m.Enabled}
	s.clients[clientID] = c
	return c, nil
}
func (s *stubRepo) Update(context.Context, int, ClientMutation) (Client, error) {
	return Client{}, ErrClientNotFound
}
func (s *stubRepo) UpdateSecret(context.Context, int, string, string) (Client, error) {
	return Client{}, ErrClientNotFound
}
func (s *stubRepo) Delete(context.Context, int) error         { return nil }
func (s *stubRepo) ListNav(context.Context) ([]Client, error) { return nil, nil }

type memGrantStore struct {
	codes  map[string]CodeGrant
	tokens map[string]TokenGrant
}

func newMemGrantStore() *memGrantStore {
	return &memGrantStore{codes: map[string]CodeGrant{}, tokens: map[string]TokenGrant{}}
}

func (s *memGrantStore) SaveCode(_ context.Context, code string, grant CodeGrant, _ time.Duration) error {
	s.codes[code] = grant
	return nil
}
func (s *memGrantStore) TakeCode(_ context.Context, code string) (CodeGrant, bool, error) {
	grant, ok := s.codes[code]
	if ok {
		delete(s.codes, code)
	}
	return grant, ok, nil
}
func (s *memGrantStore) SaveToken(_ context.Context, token string, grant TokenGrant, _ time.Duration) error {
	s.tokens[token] = grant
	return nil
}
func (s *memGrantStore) GetToken(_ context.Context, token string) (TokenGrant, bool, error) {
	grant, ok := s.tokens[token]
	return grant, ok, nil
}

type stubUserReader struct {
	users map[int]UserInfo
}

func (s *stubUserReader) BasicInfo(_ context.Context, id int) (UserInfo, error) {
	if u, ok := s.users[id]; ok {
		return u, nil
	}
	return UserInfo{}, errors.New("user not found")
}

type stubProvisioner struct {
	calls   int
	lastKey string
}

func (s *stubProvisioner) ProvisionForClient(_ context.Context, userID int, clientID, keyName string, _ int) (string, string, bool, error) {
	s.calls++
	s.lastKey = "sk-test-" + clientID
	return s.lastKey, "sk-test...abcd", s.calls == 1, nil
}

// ==================== 工具 ====================

func s256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func newTestService(clients ...Client) (*Service, *memGrantStore, *stubProvisioner) {
	repo := &stubRepo{clients: map[string]Client{}}
	for _, c := range clients {
		repo.clients[c.ClientID] = c
	}
	grants := newMemGrantStore()
	users := &stubUserReader{users: map[int]UserInfo{
		1: {ID: 1, Email: "u@example.com", Username: "u", Role: "user", Status: "active"},
		2: {ID: 2, Email: "d@example.com", Username: "d", Role: "user", Status: "disabled"},
	}}
	prov := &stubProvisioner{}
	return NewService(repo, grants, users, prov), grants, prov
}

func testClient(secret string) Client {
	return Client{
		ClientID:     "ac_test",
		SecretHash:   hashSecret(secret),
		Name:         "对话",
		RedirectURIs: []string{"https://chat.example.com/callback"},
		FirstParty:   true,
		Enabled:      true,
	}
}

// authorizeCode 走完 Authorize 流程拿一个 code。
func authorizeCode(t *testing.T, svc *Service, verifier string) string {
	t.Helper()
	code, err := svc.Authorize(context.Background(), 1, AuthorizeInput{
		ClientID:            "ac_test",
		RedirectURI:         "https://chat.example.com/callback",
		CodeChallenge:       s256(verifier),
		CodeChallengeMethod: "S256",
	})
	if err != nil {
		t.Fatalf("Authorize 失败: %v", err)
	}
	return code
}

// ==================== 测试 ====================

func TestAuthorizeValidation(t *testing.T) {
	tests := []struct {
		name    string
		input   AuthorizeInput
		wantErr error
	}{
		{
			name: "客户端不存在",
			input: AuthorizeInput{
				ClientID: "ac_missing", RedirectURI: "https://chat.example.com/callback",
				CodeChallenge: s256("v"), CodeChallengeMethod: "S256",
			},
			wantErr: ErrClientNotFound,
		},
		{
			name: "回调不在白名单",
			input: AuthorizeInput{
				ClientID: "ac_test", RedirectURI: "https://evil.example.com/callback",
				CodeChallenge: s256("v"), CodeChallengeMethod: "S256",
			},
			wantErr: ErrRedirectURIMismatch,
		},
		{
			name: "缺 PKCE challenge",
			input: AuthorizeInput{
				ClientID: "ac_test", RedirectURI: "https://chat.example.com/callback",
				CodeChallengeMethod: "S256",
			},
			wantErr: ErrPKCERequired,
		},
		{
			name: "method 不是 S256",
			input: AuthorizeInput{
				ClientID: "ac_test", RedirectURI: "https://chat.example.com/callback",
				CodeChallenge: s256("v"), CodeChallengeMethod: "plain",
			},
			wantErr: ErrPKCERequired,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _, _ := newTestService(testClient("secret"))
			_, err := svc.Authorize(context.Background(), 1, tt.input)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, 期望 %v", err, tt.wantErr)
			}
		})
	}
}

func TestAuthorizeDisabledClient(t *testing.T) {
	c := testClient("secret")
	c.Enabled = false
	svc, _, _ := newTestService(c)
	_, err := svc.Authorize(context.Background(), 1, AuthorizeInput{
		ClientID: "ac_test", RedirectURI: "https://chat.example.com/callback",
		CodeChallenge: s256("v"), CodeChallengeMethod: "S256",
	})
	if !errors.Is(err, ErrClientDisabled) {
		t.Fatalf("err = %v, 期望 ErrClientDisabled", err)
	}
}

func TestExchangeTokenHappyPath(t *testing.T) {
	svc, _, _ := newTestService(testClient("secret"))
	code := authorizeCode(t, svc, "verifier-value")

	out, err := svc.ExchangeToken(context.Background(), TokenInput{
		GrantType: "authorization_code", Code: code,
		RedirectURI: "https://chat.example.com/callback",
		ClientID:    "ac_test", ClientSecret: "secret", CodeVerifier: "verifier-value",
	})
	if err != nil {
		t.Fatalf("ExchangeToken 失败: %v", err)
	}
	if out.AccessToken == "" || out.TokenType != "Bearer" || out.ExpiresIn <= 0 {
		t.Fatalf("token 响应不完整: %+v", out)
	}

	// 令牌可解析出用户
	info, err := svc.ResolveUserInfo(context.Background(), out.AccessToken)
	if err != nil || info.ID != 1 {
		t.Fatalf("ResolveUserInfo = %+v, %v", info, err)
	}
}

func TestExchangeTokenRejections(t *testing.T) {
	base := TokenInput{
		GrantType:   "authorization_code",
		RedirectURI: "https://chat.example.com/callback",
		ClientID:    "ac_test", ClientSecret: "secret", CodeVerifier: "verifier-value",
	}
	tests := []struct {
		name    string
		mutate  func(*TokenInput)
		wantErr error
	}{
		{"grant_type 不支持", func(in *TokenInput) { in.GrantType = "client_credentials" }, ErrUnsupportedGrantType},
		{"secret 错误", func(in *TokenInput) { in.ClientSecret = "wrong" }, ErrInvalidClientSecret},
		{"client 不存在", func(in *TokenInput) { in.ClientID = "ac_missing" }, ErrInvalidClientSecret},
		{"code 无效", func(in *TokenInput) { in.Code = "oc_bogus" }, ErrInvalidGrant},
		{"redirect_uri 不一致", func(in *TokenInput) { in.RedirectURI = "https://chat.example.com/other" }, ErrInvalidGrant},
		{"PKCE verifier 不匹配", func(in *TokenInput) { in.CodeVerifier = "wrong-verifier" }, ErrInvalidGrant},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _, _ := newTestService(testClient("secret"))
			in := base
			in.Code = authorizeCode(t, svc, "verifier-value")
			tt.mutate(&in)
			if _, err := svc.ExchangeToken(context.Background(), in); !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, 期望 %v", err, tt.wantErr)
			}
		})
	}
}

func TestExchangeTokenCodeSingleUse(t *testing.T) {
	svc, _, _ := newTestService(testClient("secret"))
	code := authorizeCode(t, svc, "verifier-value")
	in := TokenInput{
		GrantType: "authorization_code", Code: code,
		RedirectURI: "https://chat.example.com/callback",
		ClientID:    "ac_test", ClientSecret: "secret", CodeVerifier: "verifier-value",
	}
	if _, err := svc.ExchangeToken(context.Background(), in); err != nil {
		t.Fatalf("首次兑换失败: %v", err)
	}
	if _, err := svc.ExchangeToken(context.Background(), in); !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("二次兑换 err = %v, 期望 ErrInvalidGrant", err)
	}
}

func TestResolveUserInfoRejections(t *testing.T) {
	svc, grants, _ := newTestService(testClient("secret"))

	if _, err := svc.ResolveUserInfo(context.Background(), "oat_bogus"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("无效令牌 err = %v, 期望 ErrInvalidToken", err)
	}

	// 用户被禁用
	_ = grants.SaveToken(context.Background(), "oat_disabled", TokenGrant{ClientID: "ac_test", UserID: 2}, time.Minute)
	if _, err := svc.ResolveUserInfo(context.Background(), "oat_disabled"); !errors.Is(err, ErrUserDisabled) {
		t.Fatalf("禁用用户 err = %v, 期望 ErrUserDisabled", err)
	}
}

func TestProvisionKey(t *testing.T) {
	svc, grants, prov := newTestService(testClient("secret"))
	_ = grants.SaveToken(context.Background(), "oat_ok", TokenGrant{ClientID: "ac_test", UserID: 1}, time.Minute)

	first, err := svc.ProvisionKey(context.Background(), "oat_ok", 0)
	if err != nil {
		t.Fatalf("首次 provision 失败: %v", err)
	}
	if !first.Created || first.APIKey == "" {
		t.Fatalf("首次 provision 结果异常: %+v", first)
	}

	second, err := svc.ProvisionKey(context.Background(), "oat_ok", 0)
	if err != nil {
		t.Fatalf("二次 provision 失败: %v", err)
	}
	if second.Created || second.APIKey != first.APIKey {
		t.Fatalf("二次 provision 应返回既有 key: %+v", second)
	}
	if prov.calls != 2 {
		t.Fatalf("provisioner 调用次数 = %d, 期望 2", prov.calls)
	}
}

func TestProvisionKeyDisabledClient(t *testing.T) {
	c := testClient("secret")
	_, grants, _ := newTestService(c)
	_ = grants.SaveToken(context.Background(), "oat_ok", TokenGrant{ClientID: "ac_test", UserID: 1}, time.Minute)

	// 令牌签发后客户端被停用：provision 应被拒
	repo := &stubRepo{clients: map[string]Client{}}
	disabled := c
	disabled.Enabled = false
	repo.clients["ac_test"] = disabled
	svc := NewService(repo, grants, &stubUserReader{users: map[int]UserInfo{1: {ID: 1, Status: "active"}}}, &stubProvisioner{})

	if _, err := svc.ProvisionKey(context.Background(), "oat_ok", 0); !errors.Is(err, ErrClientDisabled) {
		t.Fatalf("err = %v, 期望 ErrClientDisabled", err)
	}
}

func TestCreateClientValidation(t *testing.T) {
	svc, _, _ := newTestService()
	tests := []struct {
		name string
		m    ClientMutation
	}{
		{"空回调列表", ClientMutation{Name: "x", RedirectURIs: nil}},
		{"相对路径回调", ClientMutation{Name: "x", RedirectURIs: []string{"/callback"}}},
		{"非 http scheme", ClientMutation{Name: "x", RedirectURIs: []string{"ftp://a.com/cb"}}},
		{"入口地址非法", ClientMutation{Name: "x", RedirectURIs: []string{"https://a.com/cb"}, LaunchURL: "notaurl"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := svc.CreateClient(context.Background(), tt.m); !errors.Is(err, ErrInvalidRedirectURI) {
				t.Fatalf("err = %v, 期望 ErrInvalidRedirectURI", err)
			}
		})
	}

	// 合法输入：返回明文 secret 且入库为哈希
	item, secret, err := svc.CreateClient(context.Background(), ClientMutation{
		Name: "对话", RedirectURIs: []string{"https://chat.example.com/callback"}, Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateClient 失败: %v", err)
	}
	if secret == "" || item.SecretHash != hashSecret(secret) {
		t.Fatalf("secret 哈希不一致")
	}
	if item.ClientID == "" {
		t.Fatalf("client_id 未生成")
	}
}

func TestVerifyPKCE(t *testing.T) {
	if !verifyPKCE(s256("abc"), "abc") {
		t.Fatal("正确 verifier 应通过")
	}
	if verifyPKCE(s256("abc"), "abd") {
		t.Fatal("错误 verifier 应拒绝")
	}
	if verifyPKCE("", "") {
		t.Fatal("空 challenge/verifier 应拒绝")
	}
}
