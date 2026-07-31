package dto

import "time"

// ==================== 管理面：OAuth 客户端 CRUD ====================

// OAuthClientResp OAuth 客户端响应（管理面）。secret 只出 hint。
type OAuthClientResp struct {
	ID           int      `json:"id"`
	ClientID     string   `json:"client_id"`
	SecretHint   string   `json:"secret_hint"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	RedirectURIs []string `json:"redirect_uris"`
	FirstParty   bool     `json:"first_party"`
	Enabled      bool     `json:"enabled"`
	ShowInNav    bool     `json:"show_in_nav"`
	LaunchURL    string   `json:"launch_url"`
	Icon         string   `json:"icon"`
	SortOrder    int      `json:"sort_order"`

	TimeMixin
}

// OAuthClientSecretResp 创建/重置 secret 的响应：client_secret 明文仅此一次。
type OAuthClientSecretResp struct {
	OAuthClientResp
	ClientSecret string `json:"client_secret"`
}

// CreateOAuthClientReq 创建 OAuth 客户端请求。
type CreateOAuthClientReq struct {
	Name         string   `json:"name" binding:"required"`
	Description  string   `json:"description"`
	RedirectURIs []string `json:"redirect_uris" binding:"required,min=1"`
	FirstParty   bool     `json:"first_party"`
	Enabled      *bool    `json:"enabled"`
	ShowInNav    bool     `json:"show_in_nav"`
	LaunchURL    string   `json:"launch_url"`
	Icon         string   `json:"icon"`
	SortOrder    int      `json:"sort_order"`
}

// UpdateOAuthClientReq 更新 OAuth 客户端请求（全量替换可写字段）。
type UpdateOAuthClientReq struct {
	Name         string   `json:"name" binding:"required"`
	Description  string   `json:"description"`
	RedirectURIs []string `json:"redirect_uris" binding:"required,min=1"`
	FirstParty   bool     `json:"first_party"`
	Enabled      bool     `json:"enabled"`
	ShowInNav    bool     `json:"show_in_nav"`
	LaunchURL    string   `json:"launch_url"`
	Icon         string   `json:"icon"`
	SortOrder    int      `json:"sort_order"`
}

// ==================== 用户端：授权与应用导航 ====================

// AuthorizeInfoQuery 授权页信息查询。
type AuthorizeInfoQuery struct {
	ClientID    string `form:"client_id" binding:"required"`
	RedirectURI string `form:"redirect_uri" binding:"required"`
}

// AuthorizeInfoResp 授权页展示信息。
type AuthorizeInfoResp struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
	FirstParty  bool   `json:"first_party"`
}

// AuthorizeReq 签发授权码请求（SPA 授权页转发，PKCE S256 强制）。
type AuthorizeReq struct {
	ClientID            string `json:"client_id" binding:"required"`
	RedirectURI         string `json:"redirect_uri" binding:"required"`
	Scope               string `json:"scope"`
	State               string `json:"state"`
	CodeChallenge       string `json:"code_challenge" binding:"required"`
	CodeChallengeMethod string `json:"code_challenge_method" binding:"required"`
}

// AuthorizeResp 授权码响应：SPA 据此拼接回跳地址。
type AuthorizeResp struct {
	Code  string `json:"code"`
	State string `json:"state"`
}

// AppEntryResp 用户端导航应用入口。
type AppEntryResp struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
	LaunchURL   string `json:"launch_url"`
}

// ==================== 协议面：/oauth/token 等 ====================
// 注意：协议端点响应不走 {code,data,message} 信封，按 RFC 6749 原始形态返回，
// 便于外部应用直接使用标准 OAuth 客户端库。

// OAuthTokenReq /oauth/token 请求（application/x-www-form-urlencoded 或 JSON）。
type OAuthTokenReq struct {
	GrantType    string `form:"grant_type" json:"grant_type"`
	Code         string `form:"code" json:"code"`
	RedirectURI  string `form:"redirect_uri" json:"redirect_uri"`
	ClientID     string `form:"client_id" json:"client_id"`
	ClientSecret string `form:"client_secret" json:"client_secret"`
	CodeVerifier string `form:"code_verifier" json:"code_verifier"`
}

// OAuthTokenResp /oauth/token 成功响应（RFC 6749）。
type OAuthTokenResp struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	Scope       string `json:"scope,omitempty"`
}

// OAuthErrorResp OAuth 协议错误响应（RFC 6749 §5.2）。
type OAuthErrorResp struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description,omitempty"`
}

// OAuthUserInfoResp /oauth/userinfo 响应（OIDC userinfo 形态 + 可用分组扩展）。
type OAuthUserInfoResp struct {
	Sub    string               `json:"sub"`
	Name   string               `json:"name"`
	Email  string               `json:"email"`
	Role   string               `json:"role"`
	Groups []OAuthUserGroupResp `json:"groups"`
}

// OAuthUserGroupResp userinfo 附带的用户可用分组（应用据此做按组领 key / 分组货架）。
type OAuthUserGroupResp struct {
	ID             int     `json:"id"`
	Name           string  `json:"name"`
	RateMultiplier float64 `json:"rate_multiplier"`
	Note           string  `json:"note"`
}

// ProvisionKeyReq /oauth/provision-key 请求。group_id 缺省时用默认分组；
// 同一应用可按分组为用户领多把 key（幂等键 = 用户×应用×分组）。
type ProvisionKeyReq struct {
	GroupID int `json:"group_id"`
}

// ProvisionKeyResp /oauth/provision-key 响应。api_key 为明文（应用后端持有，勿下发浏览器）；
// group_id 为 key 实际落点分组（缺省请求时为默认分组），应用按组存 key 用。
type ProvisionKeyResp struct {
	APIKey  string `json:"api_key"`
	KeyHint string `json:"key_hint"`
	GroupID int    `json:"group_id"`
	Created bool   `json:"created"`
}

// OAuthWalletResp 当前平台余额。
type OAuthWalletResp struct {
	Balance string `json:"balance"`
}

// OAuthBalanceLogResp 用户余额变更记录。
type OAuthBalanceLogResp struct {
	ID            int64  `json:"id"`
	Action        string `json:"action"`
	Amount        string `json:"amount"`
	BeforeBalance string `json:"before_balance"`
	AfterBalance  string `json:"after_balance"`
	Remark        string `json:"remark"`
	CreatedAt     string `json:"created_at"`
}

// OAuthBalanceLogListResp 用户余额流水分页结果。
type OAuthBalanceLogListResp struct {
	List     []OAuthBalanceLogResp `json:"list"`
	Total    int64                 `json:"total"`
	Page     int                   `json:"page"`
	PageSize int                   `json:"page_size"`
}

// OAuthWalletDebitReq 外部应用扣款请求。
type OAuthWalletDebitReq struct {
	ExternalOrderNo string `json:"external_order_no" binding:"required,max=128"`
	Amount          string `json:"amount" binding:"required"`
	Subject         string `json:"subject" binding:"max=500"`
}

// OAuthWalletRefundReq 外部应用退款请求。
type OAuthWalletRefundReq struct {
	ExternalRefundNo   string `json:"external_refund_no" binding:"required,max=128"`
	RelatedTransaction string `json:"related_transaction_id" binding:"required"`
	Amount             string `json:"amount" binding:"required"`
	Reason             string `json:"reason" binding:"max=500"`
}

// OAuthWalletTransactionResp 钱包变更结果。
type OAuthWalletTransactionResp struct {
	TransactionID string `json:"transaction_id"`
	Balance       string `json:"balance"`
	Idempotent    bool   `json:"idempotent"`
}

// OAuthPaymentMethodResp OAuth 应用可展示的支付方式。
type OAuthPaymentMethodResp struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Icon        string `json:"icon"`
	Description string `json:"description"`
}

// OAuthPaymentMethodsResp 当前支付模块的可用状态与支付方式。
type OAuthPaymentMethodsResp struct {
	Methods    []OAuthPaymentMethodResp `json:"methods"`
	Configured bool                     `json:"configured"`
}

// OAuthCreatePaymentOrderReq OAuth 应用创建充值订单请求。
type OAuthCreatePaymentOrderReq struct {
	Amount   float64 `json:"amount" binding:"required,gt=0"`
	Method   string  `json:"method" binding:"required"`
	Subject  string  `json:"subject" binding:"max=500"`
	ClientIP string  `json:"client_ip" binding:"max=128"`
}

// OAuthPaymentOrderResp OAuth 应用充值订单响应。
type OAuthPaymentOrderResp struct {
	OutTradeNo    string     `json:"out_trade_no"`
	Method        string     `json:"method"`
	ProviderID    string     `json:"provider_id"`
	Amount        float64    `json:"amount"`
	Status        string     `json:"status"`
	Subject       string     `json:"subject"`
	PaymentURL    string     `json:"payment_url,omitempty"`
	QRCodeContent string     `json:"qr_code_content,omitempty"`
	PaidAt        *time.Time `json:"paid_at,omitempty"`
	ExpiresAt     time.Time  `json:"expires_at"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// OAuthPaymentOrderListResp OAuth 应用充值订单分页响应。
type OAuthPaymentOrderListResp struct {
	List  []OAuthPaymentOrderResp `json:"list"`
	Total int64                   `json:"total"`
}
