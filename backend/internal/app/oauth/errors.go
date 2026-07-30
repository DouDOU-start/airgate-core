package oauth

import "errors"

var (
	// ErrClientNotFound 客户端不存在。
	ErrClientNotFound = errors.New("oauth client not found")
	// ErrClientDisabled 客户端已停用。
	ErrClientDisabled = errors.New("oauth client disabled")
	// ErrRedirectURIMismatch 回调地址不在白名单。
	ErrRedirectURIMismatch = errors.New("redirect_uri not allowed")
	// ErrInvalidRedirectURI 回调地址格式非法（须为绝对 http/https URL）。
	ErrInvalidRedirectURI = errors.New("invalid redirect_uri")
	// ErrPKCERequired 缺少 PKCE code_challenge 或 method 不是 S256。
	ErrPKCERequired = errors.New("pkce with S256 is required")
	// ErrInvalidGrant 授权码无效/过期/已使用，或 PKCE 校验失败。
	ErrInvalidGrant = errors.New("invalid grant")
	// ErrInvalidClientSecret client_id 与 secret 不匹配。
	ErrInvalidClientSecret = errors.New("invalid client credentials")
	// ErrUnsupportedGrantType 仅支持 authorization_code。
	ErrUnsupportedGrantType = errors.New("unsupported grant_type")
	// ErrInvalidToken 访问令牌无效或过期。
	ErrInvalidToken = errors.New("invalid access token")
	// ErrUserDisabled 用户已被禁用。
	ErrUserDisabled = errors.New("user disabled")
	// ErrInvalidScope 授权请求包含不受支持的 scope。
	ErrInvalidScope = errors.New("invalid scope")
	// ErrInsufficientScope 访问令牌未授予当前操作需要的 scope。
	ErrInsufficientScope = errors.New("insufficient scope")
	// ErrWalletUnavailable 钱包服务未装配。
	ErrWalletUnavailable = errors.New("wallet unavailable")
)
