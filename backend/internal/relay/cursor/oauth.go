package cursor

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	loginURL   = "https://cursor.com/loginDeepControl"
	pollURL    = "https://api2.cursor.sh/auth/poll"
	refreshURL = "https://api2.cursor.sh/auth/exchange_user_api_key"
)

// AuthParams 是一次 OAuth PKCE 登录的参数集。
type AuthParams struct {
	Verifier  string
	Challenge string
	UUID      string
	LoginURL  string
}

// GenerateAuthParams 生成 PKCE verifier/challenge 与登录 URL。
// 用户在浏览器打开 LoginURL 完成授权后，服务端可用 UUID+Verifier 轮询取票。
func GenerateAuthParams() (*AuthParams, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	verifier := base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	id := uuid.NewString()

	q := url.Values{}
	q.Set("challenge", challenge)
	q.Set("uuid", id)
	q.Set("mode", "login")
	q.Set("redirectTarget", "cli")

	return &AuthParams{
		Verifier:  verifier,
		Challenge: challenge,
		UUID:      id,
		LoginURL:  loginURL + "?" + q.Encode(),
	}, nil
}

// Tokens 是一组 Cursor 凭证。
type Tokens struct {
	AccessToken  string
	RefreshToken string
	// ExpiresAt 从 access token 的 JWT exp 解析（提前 5 分钟），解析失败为零值。
	ExpiresAt time.Time
}

// PollOnce 轮询一次授权结果。pending=true 表示用户尚未完成授权。
func PollOnce(ctx context.Context, hc *http.Client, uuid, verifier string) (tokens *Tokens, pending bool, err error) {
	if hc == nil {
		hc = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s?uuid=%s&verifier=%s", pollURL, url.QueryEscape(uuid), url.QueryEscape(verifier)), nil)
	if err != nil {
		return nil, false, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		// 网络抖动按 pending 处理，由上层的会话超时兜底终止。
		return nil, true, nil
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return nil, true, nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("cursor auth poll 失败: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var data struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, false, fmt.Errorf("cursor auth poll 响应解析失败: %w", err)
	}
	if data.AccessToken == "" {
		return nil, true, nil
	}
	return &Tokens{
		AccessToken:  data.AccessToken,
		RefreshToken: data.RefreshToken,
		ExpiresAt:    TokenExpiry(data.AccessToken),
	}, false, nil
}

// RefreshToken 用 refresh token（user api key）换新 access token。
func RefreshToken(ctx context.Context, hc *http.Client, refreshToken string) (*Tokens, error) {
	if hc == nil {
		hc = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, refreshURL, strings.NewReader("{}"))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+refreshToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, &ConnectError{
			Code:       "unauthenticated",
			Message:    fmt.Sprintf("cursor token 刷新失败: %s", strings.TrimSpace(string(body))),
			HTTPStatus: resp.StatusCode,
		}
	}
	var data struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("cursor token 刷新响应解析失败: %w", err)
	}
	if data.AccessToken == "" {
		return nil, fmt.Errorf("cursor token 刷新响应缺少 accessToken")
	}
	out := &Tokens{
		AccessToken:  data.AccessToken,
		RefreshToken: data.RefreshToken,
		ExpiresAt:    TokenExpiry(data.AccessToken),
	}
	if out.RefreshToken == "" {
		out.RefreshToken = refreshToken
	}
	return out, nil
}

// TokenExpiry 从 JWT access token 解析过期时间（提前 5 分钟）。失败返回零值。
func TokenExpiry(token string) time.Time {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if json.Unmarshal(payload, &claims) != nil || claims.Exp == 0 {
		return time.Time{}
	}
	return time.Unix(claims.Exp, 0).Add(-5 * time.Minute)
}

// NewHTTPClient 构造带可选代理的普通 HTTP 客户端（OAuth 用，无需 h2 双工）。
func NewHTTPClient(proxyURL string) (*http.Client, error) {
	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL == "" {
		return &http.Client{Timeout: 30 * time.Second}, nil
	}
	parsed, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("解析代理 URL 失败: %w", err)
	}
	return &http.Client{
		Timeout:   30 * time.Second,
		Transport: &http.Transport{Proxy: http.ProxyURL(parsed)},
	}, nil
}
