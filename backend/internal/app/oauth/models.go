// Package oauth 提供第三方应用接入的 OAuth2 授权码（+PKCE）用例编排。
//
// core 作为统一身份源：独立部署的应用（对话、创作中心等）注册为 OAuth client，
// 经授权码流程获取用户身份，再经 provision-key 领取该用户名下的 sk- key 调 /v1 网关。
// core 侧不含任何具体应用逻辑——应用只是 oauth_clients 表里的数据行。
package oauth

import (
	"context"
	"time"
)

// Client OAuth 客户端（第三方应用）领域对象。
type Client struct {
	ID           int
	ClientID     string
	SecretHash   string
	SecretHint   string
	Name         string
	Description  string
	RedirectURIs []string
	FirstParty   bool
	Enabled      bool
	ShowInNav    bool
	LaunchURL    string
	Icon         string
	SortOrder    int
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// ClientMutation 创建/更新客户端的可写字段集合。
type ClientMutation struct {
	Name         string
	Description  string
	RedirectURIs []string
	FirstParty   bool
	Enabled      bool
	ShowInNav    bool
	LaunchURL    string
	Icon         string
	SortOrder    int
}

// Repository 客户端持久化接口（由 infra/store 实现）。
type Repository interface {
	List(ctx context.Context) ([]Client, error)
	FindByID(ctx context.Context, id int) (Client, error)
	FindByClientID(ctx context.Context, clientID string) (Client, error)
	Create(ctx context.Context, clientID, secretHash, secretHint string, m ClientMutation) (Client, error)
	Update(ctx context.Context, id int, m ClientMutation) (Client, error)
	UpdateSecret(ctx context.Context, id int, secretHash, secretHint string) (Client, error)
	Delete(ctx context.Context, id int) error
	// ListNav 返回启用且展示在导航的客户端，按 sort_order 升序。
	ListNav(ctx context.Context) ([]Client, error)
}

// CodeGrant 授权码携带的授权上下文（一次性，短 TTL）。
type CodeGrant struct {
	ClientID      string `json:"client_id"`
	UserID        int    `json:"user_id"`
	RedirectURI   string `json:"redirect_uri"`
	CodeChallenge string `json:"code_challenge"`
	Scope         string `json:"scope"`
}

// TokenGrant 访问令牌携带的授权上下文。
type TokenGrant struct {
	ClientID string `json:"client_id"`
	UserID   int    `json:"user_id"`
	Scope    string `json:"scope"`
}

// GrantStore 授权码与访问令牌的短期存储（Redis 实现，带 TTL）。
type GrantStore interface {
	SaveCode(ctx context.Context, code string, grant CodeGrant, ttl time.Duration) error
	// TakeCode 原子取出并删除授权码（保证一次性使用）；不存在时 ok=false。
	TakeCode(ctx context.Context, code string) (grant CodeGrant, ok bool, err error)
	SaveToken(ctx context.Context, token string, grant TokenGrant, ttl time.Duration) error
	GetToken(ctx context.Context, token string) (grant TokenGrant, ok bool, err error)
}

// UserInfo userinfo 端点返回的用户基本信息。
type UserInfo struct {
	ID       int
	Email    string
	Username string
	Role     string
	Status   string
}

// UserReader 读取用户基本信息（由 infra/store 实现）。
type UserReader interface {
	BasicInfo(ctx context.Context, id int) (UserInfo, error)
}

// KeyProvisioner 为用户按应用 get-or-create 一把 sk- key（由 app/apikey.Service 实现）。
// 返回明文 key（既有 key 经 AES-GCM 解回）、展示 hint 与是否新建。
type KeyProvisioner interface {
	ProvisionForClient(ctx context.Context, userID int, clientID, keyName string, groupID int) (plainKey, keyHint string, created bool, err error)
}

// AuthorizeInput 授权码签发入参（用户已登录，由 SPA 授权页转发）。
type AuthorizeInput struct {
	ClientID            string
	RedirectURI         string
	Scope               string
	CodeChallenge       string
	CodeChallengeMethod string
}

// TokenInput /oauth/token 兑换入参。
type TokenInput struct {
	GrantType    string
	Code         string
	RedirectURI  string
	ClientID     string
	ClientSecret string
	CodeVerifier string
}

// TokenOutput /oauth/token 响应（RFC 6749 形态）。
type TokenOutput struct {
	AccessToken string
	TokenType   string
	ExpiresIn   int
	Scope       string
}

// ProvisionResult provision-key 结果。
type ProvisionResult struct {
	APIKey  string
	KeyHint string
	Created bool
}
