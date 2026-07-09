package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/url"
	"time"

	"github.com/DouDOU-start/airgate-core/internal/pkg/logx"
)

const (
	// codeTTL 授权码有效期：SPA 拿到 code 后立即回跳应用换 token，2 分钟足够。
	codeTTL = 2 * time.Minute
	// tokenTTL 访问令牌有效期。令牌只用于身份/领 key 等控制面操作，
	// 模型调用走 sk- key（数据面），因此可以偏短；应用会话过期后重走静默授权即可。
	tokenTTL = 2 * time.Hour

	clientIDPrefix = "ac_"
	secretPrefix   = "acs_"
	codePrefix     = "oc_"
	tokenPrefix    = "oat_"
)

// Service 提供 OAuth 域用例编排。
type Service struct {
	repo   Repository
	grants GrantStore
	users  UserReader
	keys   KeyProvisioner
}

// NewService 创建 OAuth 服务。
func NewService(repo Repository, grants GrantStore, users UserReader, keys KeyProvisioner) *Service {
	return &Service{repo: repo, grants: grants, users: users, keys: keys}
}

// ==================== 管理面：客户端 CRUD ====================

// ListClients 列出全部客户端（管理面，数量少不分页）。
func (s *Service) ListClients(ctx context.Context) ([]Client, error) {
	return s.repo.List(ctx)
}

// CreateClient 创建客户端，返回明文 secret（仅此一次）。
func (s *Service) CreateClient(ctx context.Context, m ClientMutation) (Client, string, error) {
	if err := validateMutation(m); err != nil {
		return Client{}, "", err
	}
	clientID, err := randomToken(clientIDPrefix, 12)
	if err != nil {
		return Client{}, "", err
	}
	secret, err := randomToken(secretPrefix, 32)
	if err != nil {
		return Client{}, "", err
	}
	item, err := s.repo.Create(ctx, clientID, hashSecret(secret), secretHint(secret), m)
	if err != nil {
		logx.LoggerFromContext(ctx).Error("oauth_client_create_failed", logx.LogFieldError, err)
		return Client{}, "", err
	}
	logx.LoggerFromContext(ctx).Info("oauth_client_created", "client_id", item.ClientID, "name", item.Name)
	return item, secret, nil
}

// UpdateClient 更新客户端（全量字段替换，client_id/secret 不变）。
func (s *Service) UpdateClient(ctx context.Context, id int, m ClientMutation) (Client, error) {
	if err := validateMutation(m); err != nil {
		return Client{}, err
	}
	return s.repo.Update(ctx, id, m)
}

// DeleteClient 删除客户端。已签发的授权码/令牌在 TTL 内失效前会因客户端查不到而被拒。
func (s *Service) DeleteClient(ctx context.Context, id int) error {
	return s.repo.Delete(ctx, id)
}

// ResetSecret 重置 secret，返回新明文（仅此一次）；旧 secret 立即失效。
func (s *Service) ResetSecret(ctx context.Context, id int) (Client, string, error) {
	secret, err := randomToken(secretPrefix, 32)
	if err != nil {
		return Client{}, "", err
	}
	item, err := s.repo.UpdateSecret(ctx, id, hashSecret(secret), secretHint(secret))
	if err != nil {
		return Client{}, "", err
	}
	logx.LoggerFromContext(ctx).Info("oauth_client_secret_reset", "client_id", item.ClientID)
	return item, secret, nil
}

// ==================== 用户端：导航与授权 ====================

// NavApps 用户端导航展示的应用列表。
func (s *Service) NavApps(ctx context.Context) ([]Client, error) {
	return s.repo.ListNav(ctx)
}

// AuthorizeInfo 授权页信息：校验 client 与 redirect_uri，返回应用名称/图标/是否第一方。
// SPA 据 FirstParty 决定静默通过还是展示确认页。
func (s *Service) AuthorizeInfo(ctx context.Context, clientID, redirectURI string) (Client, error) {
	return s.validateClientRedirect(ctx, clientID, redirectURI)
}

// Authorize 为已登录用户签发授权码（PKCE S256 强制）。
func (s *Service) Authorize(ctx context.Context, userID int, input AuthorizeInput) (string, error) {
	client, err := s.validateClientRedirect(ctx, input.ClientID, input.RedirectURI)
	if err != nil {
		return "", err
	}
	if input.CodeChallenge == "" || input.CodeChallengeMethod != "S256" {
		return "", ErrPKCERequired
	}
	code, err := randomToken(codePrefix, 32)
	if err != nil {
		return "", err
	}
	grant := CodeGrant{
		ClientID:      client.ClientID,
		UserID:        userID,
		RedirectURI:   input.RedirectURI,
		CodeChallenge: input.CodeChallenge,
		Scope:         input.Scope,
	}
	if err := s.grants.SaveCode(ctx, code, grant, codeTTL); err != nil {
		logx.LoggerFromContext(ctx).Error("oauth_code_save_failed", logx.LogFieldError, err)
		return "", err
	}
	return code, nil
}

// ==================== 协议面：token / userinfo / provision ====================

// ExchangeToken 授权码换访问令牌：校验 client secret、授权码一次性取出、PKCE 校验。
func (s *Service) ExchangeToken(ctx context.Context, input TokenInput) (TokenOutput, error) {
	logger := logx.LoggerFromContext(ctx)
	if input.GrantType != "authorization_code" {
		return TokenOutput{}, ErrUnsupportedGrantType
	}
	client, err := s.repo.FindByClientID(ctx, input.ClientID)
	if err != nil {
		return TokenOutput{}, ErrInvalidClientSecret
	}
	if !client.Enabled {
		return TokenOutput{}, ErrClientDisabled
	}
	if subtle.ConstantTimeCompare([]byte(client.SecretHash), []byte(hashSecret(input.ClientSecret))) != 1 {
		logger.Warn("oauth_token_rejected", "client_id", input.ClientID, logx.LogFieldReason, "bad_secret")
		return TokenOutput{}, ErrInvalidClientSecret
	}

	grant, ok, err := s.grants.TakeCode(ctx, input.Code)
	if err != nil {
		logger.Error("oauth_code_take_failed", logx.LogFieldError, err)
		return TokenOutput{}, err
	}
	if !ok || grant.ClientID != client.ClientID || grant.RedirectURI != input.RedirectURI {
		return TokenOutput{}, ErrInvalidGrant
	}
	if !verifyPKCE(grant.CodeChallenge, input.CodeVerifier) {
		logger.Warn("oauth_token_rejected", "client_id", input.ClientID, logx.LogFieldReason, "pkce_mismatch")
		return TokenOutput{}, ErrInvalidGrant
	}

	token, err := randomToken(tokenPrefix, 32)
	if err != nil {
		return TokenOutput{}, err
	}
	tg := TokenGrant{ClientID: client.ClientID, UserID: grant.UserID, Scope: grant.Scope}
	if err := s.grants.SaveToken(ctx, token, tg, tokenTTL); err != nil {
		logger.Error("oauth_token_save_failed", logx.LogFieldError, err)
		return TokenOutput{}, err
	}
	logger.Info("oauth_token_issued", "client_id", client.ClientID, logx.LogFieldUserID, grant.UserID)
	return TokenOutput{
		AccessToken: token,
		TokenType:   "Bearer",
		ExpiresIn:   int(tokenTTL.Seconds()),
		Scope:       grant.Scope,
	}, nil
}

// resolveActiveUser 解析访问令牌并确认用户仍可用。
func (s *Service) resolveActiveUser(ctx context.Context, token string) (TokenGrant, UserInfo, error) {
	grant, ok, err := s.grants.GetToken(ctx, token)
	if err != nil {
		return TokenGrant{}, UserInfo{}, err
	}
	if !ok {
		return TokenGrant{}, UserInfo{}, ErrInvalidToken
	}
	info, err := s.users.BasicInfo(ctx, grant.UserID)
	if err != nil {
		return TokenGrant{}, UserInfo{}, err
	}
	if info.Status != "active" {
		return TokenGrant{}, UserInfo{}, ErrUserDisabled
	}
	return grant, info, nil
}

// ResolveUserInfo userinfo 端点：访问令牌 → 用户基本信息。
func (s *Service) ResolveUserInfo(ctx context.Context, token string) (UserInfo, error) {
	_, info, err := s.resolveActiveUser(ctx, token)
	return info, err
}

// ProvisionKey 为令牌对应用户 get-or-create 一把该应用专属的 sk- key。
// groupID=0 时由 apikey 域选默认分组。
func (s *Service) ProvisionKey(ctx context.Context, token string, groupID int) (ProvisionResult, error) {
	grant, _, err := s.resolveActiveUser(ctx, token)
	if err != nil {
		return ProvisionResult{}, err
	}
	client, err := s.repo.FindByClientID(ctx, grant.ClientID)
	if err != nil {
		return ProvisionResult{}, ErrClientNotFound
	}
	if !client.Enabled {
		return ProvisionResult{}, ErrClientDisabled
	}
	plainKey, hint, created, err := s.keys.ProvisionForClient(ctx, grant.UserID, client.ClientID, client.Name, groupID)
	if err != nil {
		return ProvisionResult{}, err
	}
	if created {
		logx.LoggerFromContext(ctx).Info("oauth_key_provisioned",
			"client_id", client.ClientID, logx.LogFieldUserID, grant.UserID)
	}
	return ProvisionResult{APIKey: plainKey, KeyHint: hint, Created: created}, nil
}

// ==================== 内部工具 ====================

// validateClientRedirect 按 client_id 查客户端并校验启用状态与回调白名单（精确匹配）。
func (s *Service) validateClientRedirect(ctx context.Context, clientID, redirectURI string) (Client, error) {
	client, err := s.repo.FindByClientID(ctx, clientID)
	if err != nil {
		return Client{}, ErrClientNotFound
	}
	if !client.Enabled {
		return Client{}, ErrClientDisabled
	}
	for _, allowed := range client.RedirectURIs {
		if allowed == redirectURI {
			return client, nil
		}
	}
	return Client{}, ErrRedirectURIMismatch
}

// validateMutation 校验回调地址与入口地址均为绝对 http/https URL。
func validateMutation(m ClientMutation) error {
	if len(m.RedirectURIs) == 0 {
		return ErrInvalidRedirectURI
	}
	for _, raw := range m.RedirectURIs {
		if !isAbsoluteHTTPURL(raw) {
			return ErrInvalidRedirectURI
		}
	}
	if m.LaunchURL != "" && !isAbsoluteHTTPURL(m.LaunchURL) {
		return ErrInvalidRedirectURI
	}
	return nil
}

func isAbsoluteHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// verifyPKCE 校验 S256：base64url(sha256(verifier)) == challenge。
func verifyPKCE(challenge, verifier string) bool {
	if challenge == "" || verifier == "" {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	computed := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(computed), []byte(challenge)) == 1
}

// hashSecret client secret 的 SHA-256 hex。
func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// secretHint 尾 4 位提示。
func secretHint(secret string) string {
	if len(secret) <= 4 {
		return secret
	}
	return "..." + secret[len(secret)-4:]
}

// randomToken 生成 prefix + n 字节随机数的 hex 编码。
func randomToken(prefix string, n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成随机令牌失败: %w", err)
	}
	return prefix + hex.EncodeToString(buf), nil
}
