package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
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

type stubGroupReader struct {
	groups []GroupInfo
}

func (s *stubGroupReader) AvailableForUser(context.Context, int) ([]GroupInfo, error) {
	return s.groups, nil
}

type stubProvisioner struct {
	calls   int
	lastKey string
}

func (s *stubProvisioner) ProvisionForClient(_ context.Context, userID int, clientID, keyName string, groupID int) (string, string, int, bool, error) {
	s.calls++
	// 模拟 groupID=0 → 默认分组 1 的解析，并按分组区分 key（幂等键含分组）。
	if groupID == 0 {
		groupID = 1
	}
	s.lastKey = fmt.Sprintf("sk-test-%s-g%d", clientID, groupID)
	return s.lastKey, "sk-test...abcd", groupID, s.calls == 1, nil
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
		1: {ID: 1, Email: "u@example.com", Username: "u", Role: "user", Status: "active", Balance: 12.5},
		2: {ID: 2, Email: "d@example.com", Username: "d", Role: "user", Status: "disabled"},
	}}
	prov := &stubProvisioner{}
	groups := &stubGroupReader{groups: []GroupInfo{{ID: 1, Name: "default", RateMultiplier: 1}}}
	return NewService(repo, grants, users, groups, prov), grants, prov
}

type stubWalletManager struct {
	debits int
}

type stubPaymentManager struct {
	createdUserID int
	returnURL     string
	orders        map[string]PaymentOrder
}

type stubBalanceLogReader struct {
	userID int
}

func (s *stubBalanceLogReader) ListBalanceLogs(_ context.Context, userID, page, pageSize int) (BalanceLogList, error) {
	s.userID = userID
	return BalanceLogList{
		List:  []BalanceLog{{ID: 1, Action: "add", Amount: 20, BeforeBalance: 10, AfterBalance: 30, Remark: "在线充值"}},
		Total: 1, Page: page, PageSize: pageSize,
	}, nil
}

func (s *stubPaymentManager) AvailableMethods(context.Context) PaymentMethodsResult {
	return PaymentMethodsResult{Configured: true, Methods: []PaymentMethod{{Key: "alipay", Label: "支付宝"}}}
}

func (s *stubPaymentManager) CreateOrder(_ context.Context, userID int, amount float64, method, _, _, returnURL string) (PaymentOrder, error) {
	s.createdUserID = userID
	s.returnURL = returnURL
	order := PaymentOrder{OutTradeNo: "R_TEST", Amount: amount, Method: method, Status: "pending"}
	if s.orders == nil {
		s.orders = map[string]PaymentOrder{}
	}
	s.orders[order.OutTradeNo] = order
	return order, nil
}

func (s *stubPaymentManager) GetUserOrder(_ context.Context, _ int, outTradeNo string) (PaymentOrder, error) {
	return s.orders[outTradeNo], nil
}

func (s *stubPaymentManager) ListUserOrders(context.Context, int, int, int) ([]PaymentOrder, int64, error) {
	return []PaymentOrder{s.orders["R_TEST"]}, 1, nil
}

func (s *stubWalletManager) Debit(_ context.Context, _ int, _, _, _, _ string) (WalletTransaction, error) {
	s.debits++
	return WalletTransaction{TransactionID: "wtx_test", Balance: 10, Idempotent: false}, nil
}

func (s *stubWalletManager) Refund(_ context.Context, _ int, _, _, _, _, _ string) (WalletTransaction, error) {
	return WalletTransaction{TransactionID: "wtx_refund", Balance: 12.5}, nil
}

func TestWalletScopes(t *testing.T) {
	svc, grants, _ := newTestService(testClient("secret"))
	wallet := &stubWalletManager{}
	svc.SetWalletManager(wallet)
	grants.tokens["read_only"] = TokenGrant{ClientID: "ac_test", UserID: 1, Scope: "profile wallet.read"}
	grants.tokens["shop"] = TokenGrant{ClientID: "ac_test", UserID: 1, Scope: "profile wallet.read wallet.debit wallet.refund"}

	balance, err := svc.WalletBalance(context.Background(), "read_only")
	if err != nil || balance != 12.5 {
		t.Fatalf("WalletBalance = %v, %v", balance, err)
	}
	if _, err := svc.DebitWallet(context.Background(), "read_only", WalletDebitInput{
		ExternalOrderNo: "MK001", Amount: "1",
	}); !errors.Is(err, ErrInsufficientScope) {
		t.Fatalf("read_only debit error = %v", err)
	}
	result, err := svc.DebitWallet(context.Background(), "shop", WalletDebitInput{
		ExternalOrderNo: "MK001", Amount: "1",
	})
	if err != nil || result.TransactionID != "wtx_test" || wallet.debits != 1 {
		t.Fatalf("shop debit = %+v, %v, calls=%d", result, err, wallet.debits)
	}
}

func TestWalletHistoryUsesTokenUser(t *testing.T) {
	svc, grants, _ := newTestService(testClient("secret"))
	reader := &stubBalanceLogReader{}
	svc.SetBalanceLogReader(reader)
	grants.tokens["read_only"] = TokenGrant{ClientID: "ac_test", UserID: 1, Scope: "profile wallet.read"}
	grants.tokens["no_wallet"] = TokenGrant{ClientID: "ac_test", UserID: 1, Scope: "profile"}

	result, err := svc.WalletHistory(context.Background(), "read_only", 2, 10)
	if err != nil || result.Total != 1 || result.Page != 2 || reader.userID != 1 {
		t.Fatalf("WalletHistory = %+v, %v, user=%d", result, err, reader.userID)
	}
	if _, err := svc.WalletHistory(context.Background(), "no_wallet", 1, 20); !errors.Is(err, ErrInsufficientScope) {
		t.Fatalf("无 wallet.read scope err = %v", err)
	}
}

func TestPaymentScopesAndSafeReturnURL(t *testing.T) {
	client := testClient("secret")
	client.LaunchURL = "https://chat.example.com/store"
	svc, grants, _ := newTestService(client)
	payment := &stubPaymentManager{}
	svc.SetPaymentManager(payment)
	grants.tokens["read_only"] = TokenGrant{ClientID: "ac_test", UserID: 1, Scope: "profile payment.read"}
	grants.tokens["recharge"] = TokenGrant{ClientID: "ac_test", UserID: 1, Scope: "profile payment.read payment.create"}

	methods, err := svc.PaymentMethods(context.Background(), "read_only")
	if err != nil || !methods.Configured || len(methods.Methods) != 1 {
		t.Fatalf("PaymentMethods = %+v, %v", methods, err)
	}
	if _, err := svc.CreatePaymentOrder(context.Background(), "read_only", PaymentOrderInput{Amount: 20, Method: "alipay"}); !errors.Is(err, ErrInsufficientScope) {
		t.Fatalf("只读令牌创建订单 err = %v", err)
	}
	order, err := svc.CreatePaymentOrder(context.Background(), "recharge", PaymentOrderInput{Amount: 20, Method: "alipay"})
	if err != nil || order.OutTradeNo != "R_TEST" {
		t.Fatalf("CreatePaymentOrder = %+v, %v", order, err)
	}
	if payment.createdUserID != 1 || payment.returnURL != "https://chat.example.com/store" {
		t.Fatalf("支付下单身份或回跳地址错误: user=%d return=%q", payment.createdUserID, payment.returnURL)
	}
	got, err := svc.GetPaymentOrder(context.Background(), "recharge", "R_TEST")
	if err != nil || got.OutTradeNo != "R_TEST" {
		t.Fatalf("GetPaymentOrder = %+v, %v", got, err)
	}
}

func TestPaymentReturnURLRejectsCrossOriginLaunchURL(t *testing.T) {
	client := testClient("secret")
	client.LaunchURL = "https://evil.example.com/steal"
	got, err := paymentReturnURL(client)
	if err != nil || got != "https://chat.example.com/" {
		t.Fatalf("paymentReturnURL = %q, %v", got, err)
	}
}

func TestNormalizePaymentScopes(t *testing.T) {
	got, err := normalizeScope("payment.create profile payment.read payment.create")
	if err != nil || got != "payment.create payment.read profile" {
		t.Fatalf("normalizeScope = %q, %v", got, err)
	}
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
	svc := NewService(repo, grants, &stubUserReader{users: map[int]UserInfo{1: {ID: 1, Status: "active"}}}, &stubGroupReader{}, &stubProvisioner{})

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
