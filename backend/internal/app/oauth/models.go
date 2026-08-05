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
	ID            int
	ClientID      string
	SecretHash    string
	SecretHint    string
	Name          string
	Description   string
	RedirectURIs  []string
	AllowedScopes []string
	FirstParty    bool
	Enabled       bool
	ShowInNav     bool
	LaunchURL     string
	Icon          string
	SortOrder     int
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// ClientMutation 创建/更新客户端的可写字段集合。
type ClientMutation struct {
	Name          string
	Description   string
	RedirectURIs  []string
	AllowedScopes []string
	FirstParty    bool
	Enabled       bool
	ShowInNav     bool
	LaunchURL     string
	Icon          string
	SortOrder     int
}

// Repository 客户端持久化接口（由 infra/store 实现）。
type Repository interface {
	List(ctx context.Context) ([]Client, error)
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
	Balance  float64
}

// UserReader 读取用户基本信息（由 infra/store 实现）。
type UserReader interface {
	BasicInfo(ctx context.Context, id int) (UserInfo, error)
}

// GroupInfo userinfo 端点返回的用户可用分组（应用据此做「按组领 key / 分组货架」）。
type GroupInfo struct {
	ID             int
	Name           string
	RateMultiplier float64
	Note           string
}

// GroupReader 读取用户可用分组（由 app/group.Service 适配实现）。
type GroupReader interface {
	AvailableForUser(ctx context.Context, userID int) ([]GroupInfo, error)
}

// KeyProvisioner 为用户按应用按分组 get-or-create 一把 sk- key（由 app/apikey.Service 实现）。
// 返回明文 key（既有 key 经 AES-GCM 解回）、展示 hint、实际落点分组与是否新建。
type KeyProvisioner interface {
	ProvisionForClient(ctx context.Context, userID int, clientID, keyName string, groupID int) (plainKey, keyHint string, resolvedGroupID int, created bool, err error)
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

// ProvisionResult provision-key 结果。GroupID 为 key 的实际落点分组（group_id=0 时为默认分组）。
type ProvisionResult struct {
	APIKey  string
	KeyHint string
	GroupID int
	Created bool
}

// WalletTransaction 是外部应用余额扣款或退款的协议结果。
type WalletTransaction struct {
	TransactionID string
	Balance       float64
	Idempotent    bool
}

// WalletManager 由 app/wallet.Service 适配实现，OAuth 域只负责令牌、用户与 scope 校验。
type WalletManager interface {
	Debit(ctx context.Context, userID int, clientID, externalOrderNo, amount, subject string) (WalletTransaction, error)
	Refund(ctx context.Context, userID int, clientID, externalRefundNo, relatedTransactionID, amount, reason string) (WalletTransaction, error)
}

// BalanceLog 是 OAuth 应用可读取的用户余额变更记录。
type BalanceLog struct {
	ID            int64
	Action        string
	Amount        float64
	BeforeBalance float64
	AfterBalance  float64
	Remark        string
	CreatedAt     string
}

// BalanceLogList 是余额流水分页结果。
type BalanceLogList struct {
	List     []BalanceLog
	Total    int64
	Page     int
	PageSize int
}

// BalanceLogReader 由 user.Service 适配实现，只读取令牌用户自己的余额流水。
type BalanceLogReader interface {
	ListBalanceLogs(ctx context.Context, userID, page, pageSize int) (BalanceLogList, error)
}

// WalletDebitInput 外部应用扣款入参。
type WalletDebitInput struct {
	ExternalOrderNo string
	Amount          string
	Subject         string
}

// WalletRefundInput 外部应用退款入参。
type WalletRefundInput struct {
	ExternalRefundNo   string
	RelatedTransaction string
	Amount             string
	Reason             string
}

// PaymentMethod 是外部应用可展示的支付方式元信息。
type PaymentMethod struct {
	Key         string
	Label       string
	Icon        string
	Description string
}

// PaymentMethodsResult 是当前支付模块的可用状态与支付方式。
type PaymentMethodsResult struct {
	Methods    []PaymentMethod
	Configured bool
}

// PaymentOrder 是 OAuth 协议对外暴露的充值订单。
type PaymentOrder struct {
	OutTradeNo    string
	Method        string
	ProviderID    string
	Amount        float64
	Status        string
	Subject       string
	PaymentURL    string
	QRCodeContent string
	PaidAt        *time.Time
	ExpiresAt     time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// PaymentManager 由 payment.Service 适配实现，充值订单与余额入账仍完全归 Core 管理。
type PaymentManager interface {
	AvailableMethods(ctx context.Context) PaymentMethodsResult
	CreateOrder(ctx context.Context, userID int, amount float64, method, subject, clientIP, returnURL string) (PaymentOrder, error)
	GetUserOrder(ctx context.Context, userID int, outTradeNo string) (PaymentOrder, error)
	ListUserOrders(ctx context.Context, userID, page, pageSize int) ([]PaymentOrder, int64, error)
}

// PaymentOrderInput OAuth 应用创建充值订单的输入。
type PaymentOrderInput struct {
	Amount   float64
	Method   string
	Subject  string
	ClientIP string
}
