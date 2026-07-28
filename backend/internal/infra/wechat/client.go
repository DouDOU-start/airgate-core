// Package wechat 提供微信公众号接口的最小客户端。
package wechat

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"
)

const (
	defaultBaseURL = "https://api.weixin.qq.com"
	tokenCacheKey  = "agw:wechat:access_token:"
	maxBodyBytes   = 1 << 20
)

// Credentials 微信公众号应用凭证。
type Credentials struct {
	AppID     string
	AppSecret string
}

// TemplateData 模板消息中的单个字段。
type TemplateData struct {
	Value string `json:"value"`
}

// TemplateMessage 微信公众号模板消息。
type TemplateMessage struct {
	ToUser     string                  `json:"touser"`
	TemplateID string                  `json:"template_id"`
	URL        string                  `json:"url,omitempty"`
	Data       map[string]TemplateData `json:"data"`
}

// APIError 微信接口返回的业务错误。
type APIError struct {
	Code    int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("微信接口错误：errcode=%d，errmsg=%s", e.Code, e.Message)
}

type cachedToken struct {
	value     string
	expiresAt time.Time
}

// Client 微信公众号接口客户端。access_token 优先缓存在 Redis，Redis 不可用时
// 回退进程内缓存；singleflight 避免同一实例并发刷新令牌。
type Client struct {
	httpClient *http.Client
	rdb        *redis.Client
	baseURL    string

	tokenGroup singleflight.Group
	mu         sync.Mutex
	memory     map[string]cachedToken
}

// New 创建微信公众号客户端。
func New(rdb *redis.Client) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 10 * time.Second},
		rdb:        rdb,
		baseURL:    defaultBaseURL,
		memory:     make(map[string]cachedToken),
	}
}

// SendTemplate 发送模板消息。令牌失效时会清理缓存并自动重试一次。
func (c *Client) SendTemplate(ctx context.Context, credentials Credentials, message TemplateMessage) error {
	if strings.TrimSpace(credentials.AppID) == "" || strings.TrimSpace(credentials.AppSecret) == "" {
		return errors.New("微信公众号 AppID 或 AppSecret 未配置")
	}
	if strings.TrimSpace(message.ToUser) == "" || strings.TrimSpace(message.TemplateID) == "" {
		return errors.New("微信公众号接收人 OpenID 或模板 ID 未配置")
	}

	token, err := c.accessToken(ctx, credentials)
	if err != nil {
		return err
	}
	if err := c.sendTemplateWithToken(ctx, token, message); err != nil {
		var apiErr *APIError
		if !errors.As(err, &apiErr) || !isTokenError(apiErr.Code) {
			return err
		}
		c.invalidateToken(ctx, credentials)
		token, err = c.accessToken(ctx, credentials)
		if err != nil {
			return err
		}
		return c.sendTemplateWithToken(ctx, token, message)
	}
	return nil
}

// ExchangeOAuthCode 使用公众号网页授权 code 换取当前扫码用户的 OpenID。
func (c *Client) ExchangeOAuthCode(ctx context.Context, credentials Credentials, code string) (string, error) {
	if strings.TrimSpace(credentials.AppID) == "" || strings.TrimSpace(credentials.AppSecret) == "" {
		return "", errors.New("微信公众号 AppID 或 AppSecret 未配置")
	}
	if strings.TrimSpace(code) == "" {
		return "", errors.New("微信网页授权 code 为空")
	}

	endpoint, err := url.Parse(c.baseURL + "/sns/oauth2/access_token")
	if err != nil {
		return "", err
	}
	query := endpoint.Query()
	query.Set("appid", strings.TrimSpace(credentials.AppID))
	query.Set("secret", strings.TrimSpace(credentials.AppSecret))
	query.Set("code", strings.TrimSpace(code))
	query.Set("grant_type", "authorization_code")
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return "", err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("交换微信网页授权 code 失败：%w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return "", fmt.Errorf("读取微信网页授权响应失败：%w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("交换微信网页授权 code 失败：HTTP %d", resp.StatusCode)
	}

	var result struct {
		OpenID  string `json:"openid"`
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("解析微信网页授权响应失败：%w", err)
	}
	if result.ErrCode != 0 {
		return "", &APIError{Code: result.ErrCode, Message: result.ErrMsg}
	}
	if strings.TrimSpace(result.OpenID) == "" {
		return "", errors.New("微信网页授权响应缺少 OpenID")
	}
	return strings.TrimSpace(result.OpenID), nil
}

func (c *Client) accessToken(ctx context.Context, credentials Credentials) (string, error) {
	cacheID := tokenCacheID(credentials)
	if token := c.cachedAccessToken(ctx, cacheID); token != "" {
		return token, nil
	}

	value, err, _ := c.tokenGroup.Do(cacheID, func() (any, error) {
		if token := c.cachedAccessToken(ctx, cacheID); token != "" {
			return token, nil
		}
		token, ttl, err := c.fetchAccessToken(ctx, credentials)
		if err != nil {
			return "", err
		}
		c.cacheAccessToken(ctx, cacheID, token, ttl)
		return token, nil
	})
	if err != nil {
		return "", err
	}
	return value.(string), nil
}

func (c *Client) fetchAccessToken(ctx context.Context, credentials Credentials) (string, time.Duration, error) {
	endpoint, err := url.Parse(c.baseURL + "/cgi-bin/token")
	if err != nil {
		return "", 0, err
	}
	query := endpoint.Query()
	query.Set("grant_type", "client_credential")
	query.Set("appid", strings.TrimSpace(credentials.AppID))
	query.Set("secret", strings.TrimSpace(credentials.AppSecret))
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return "", 0, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("获取微信 access_token 失败：%w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return "", 0, fmt.Errorf("读取微信 access_token 响应失败：%w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", 0, fmt.Errorf("获取微信 access_token 失败：HTTP %d", resp.StatusCode)
	}

	var result struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		ErrCode     int    `json:"errcode"`
		ErrMsg      string `json:"errmsg"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", 0, fmt.Errorf("解析微信 access_token 响应失败：%w", err)
	}
	if result.ErrCode != 0 {
		return "", 0, &APIError{Code: result.ErrCode, Message: result.ErrMsg}
	}
	if result.AccessToken == "" {
		return "", 0, errors.New("微信 access_token 响应缺少令牌")
	}

	ttl := time.Duration(result.ExpiresIn) * time.Second
	if ttl <= 0 {
		ttl = 90 * time.Minute
	}
	// 提前五分钟失效，避免临界点拿到即将过期的令牌。
	if ttl > 10*time.Minute {
		ttl -= 5 * time.Minute
	} else {
		ttl /= 2
	}
	return result.AccessToken, ttl, nil
}

func (c *Client) sendTemplateWithToken(ctx context.Context, token string, message TemplateMessage) error {
	body, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("编码微信模板消息失败：%w", err)
	}

	endpoint, err := url.Parse(c.baseURL + "/cgi-bin/message/template/send")
	if err != nil {
		return err
	}
	query := endpoint.Query()
	query.Set("access_token", token)
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("发送微信模板消息失败：%w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return fmt.Errorf("读取微信模板消息响应失败：%w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("发送微信模板消息失败：HTTP %d", resp.StatusCode)
	}

	var result struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return fmt.Errorf("解析微信模板消息响应失败：%w", err)
	}
	if result.ErrCode != 0 {
		return &APIError{Code: result.ErrCode, Message: result.ErrMsg}
	}
	return nil
}

func (c *Client) cachedAccessToken(ctx context.Context, appID string) string {
	if c.rdb != nil {
		if value, err := c.rdb.Get(ctx, tokenCacheKey+appID).Result(); err == nil && value != "" {
			return value
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.memory[appID]
	if !ok || time.Now().After(entry.expiresAt) {
		delete(c.memory, appID)
		return ""
	}
	return entry.value
}

func (c *Client) cacheAccessToken(ctx context.Context, appID, token string, ttl time.Duration) {
	c.mu.Lock()
	c.memory[appID] = cachedToken{value: token, expiresAt: time.Now().Add(ttl)}
	c.mu.Unlock()

	if c.rdb != nil {
		_ = c.rdb.Set(ctx, tokenCacheKey+appID, token, ttl).Err()
	}
}

func (c *Client) invalidateToken(ctx context.Context, credentials Credentials) {
	cacheID := tokenCacheID(credentials)
	c.mu.Lock()
	delete(c.memory, cacheID)
	c.mu.Unlock()
	if c.rdb != nil {
		_ = c.rdb.Del(ctx, tokenCacheKey+cacheID).Err()
	}
}

func tokenCacheID(credentials Credentials) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(credentials.AppID) + "\x00" + strings.TrimSpace(credentials.AppSecret)))
	return fmt.Sprintf("%x", sum[:12])
}

func isTokenError(code int) bool {
	switch code {
	case 40001, 40014, 42001:
		return true
	default:
		return false
	}
}
