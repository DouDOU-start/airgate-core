package account

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/DouDOU-start/airgate-core/internal/relay/cpa"
)

const (
	claudeOAuthClientID     = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	claudeOAuthTokenURL     = "https://platform.claude.com/v1/oauth/token"
	claudeOAuthRefreshScope = "user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload"
)

type accountUpstreamHTTPError struct {
	status int
	body   []byte
	label  string
}

func (e *accountUpstreamHTTPError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("%s HTTP %d: %s", e.label, e.status, truncate(strings.TrimSpace(string(e.body)), 400))
}

func (e *accountUpstreamHTTPError) StatusCode() int {
	if e == nil {
		return 0
	}
	return e.status
}

func oauthCredentialsNeedRefresh(item Account, now time.Time) bool {
	if NormalizeAccountType(item.Type) != TypeOAuth || item.Credentials == nil || !accountHasOAuthRefreshPath(item) {
		return false
	}
	platform := strings.ToLower(strings.TrimSpace(item.Platform))
	if strings.TrimSpace(item.Credentials["access_token"]) == "" {
		return true
	}
	expiry, ok := credentialExpiration(item.Credentials)
	if !ok {
		return false
	}
	return !expiry.After(now.Add(oauthRefreshLead(platform)))
}

func accountHasOAuthRefreshPath(item Account) bool {
	if NormalizeAccountType(item.Type) != TypeOAuth || item.Credentials == nil {
		return false
	}
	if strings.TrimSpace(item.Credentials["refresh_token"]) != "" {
		return true
	}
	platform := strings.ToLower(strings.TrimSpace(item.Platform))
	return (platform == "codex" || platform == "openai") && strings.TrimSpace(item.Credentials["session_token"]) != ""
}

func oauthRefreshLead(platform string) time.Duration {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case "codex", "openai":
		return 5 * 24 * time.Hour
	case "claude", "anthropic":
		return 4 * time.Hour
	default:
		return 5 * time.Minute
	}
}

func credentialExpiration(credentials map[string]string) (time.Time, bool) {
	if credentials == nil {
		return time.Time{}, false
	}
	for _, key := range []string{"expired", "expire", "expires_at", "expiresAt", "expiry", "expires"} {
		raw := strings.TrimSpace(credentials[key])
		if raw == "" {
			continue
		}
		// Codex imports and OAuth responses use both Unix seconds and Unix
		// milliseconds. Accept either representation before trying date
		// strings so refresh decisions match the native executor.
		if number, err := strconv.ParseInt(raw, 10, 64); err == nil && number > 0 {
			if number > 100_000_000_000 {
				number /= 1000
			}
			return time.Unix(number, 0), true
		}
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05", "2006-01-02 15:04"} {
			if parsed, err := time.Parse(layout, raw); err == nil {
				return parsed, true
			}
		}
	}
	// Access-token-only imports frequently omit an explicit expiry while the
	// JWT still carries the authoritative exp claim. This is deliberately a
	// best-effort decode: signature verification belongs to the provider, and
	// malformed/opaque tokens simply retain the historical no-expiry behavior.
	if token := strings.TrimSpace(credentials["access_token"]); token != "" {
		parts := strings.Split(token, ".")
		if len(parts) >= 2 {
			payload, err := base64.RawURLEncoding.DecodeString(parts[1])
			if err != nil {
				payload, err = base64.URLEncoding.DecodeString(parts[1])
			}
			if err == nil {
				var claims map[string]json.RawMessage
				if json.Unmarshal(payload, &claims) == nil {
					if raw := claims["exp"]; len(raw) > 0 {
						var number json.Number
						if json.Unmarshal(raw, &number) == nil {
							if value, err := strconv.ParseInt(string(number), 10, 64); err == nil && value > 0 {
								if value > 100_000_000_000 {
									value /= 1000
								}
								return time.Unix(value, 0), true
							}
						}
						var textValue string
						if json.Unmarshal(raw, &textValue) == nil {
							if value, err := strconv.ParseInt(strings.TrimSpace(textValue), 10, 64); err == nil && value > 0 {
								if value > 100_000_000_000 {
									value /= 1000
								}
								return time.Unix(value, 0), true
							}
						}
					}
				}
			}
		}
	}
	return time.Time{}, false
}

func (s *Service) ensureOAuthCredentialsFresh(ctx context.Context, item *Account, proxyURL string) error {
	if item == nil || !oauthCredentialsNeedRefresh(*item, time.Now()) {
		return nil
	}
	return s.refreshOAuthCredentials(ctx, item, proxyURL)
}

func (s *Service) refreshOAuthCredentials(ctx context.Context, item *Account, proxyURL string) error {
	if item == nil {
		return fmt.Errorf("账号为空")
	}
	if item.Credentials == nil {
		item.Credentials = map[string]string{}
	}
	normalizeCredentialKeys(item.Credentials)
	platform := strings.ToLower(strings.TrimSpace(item.Platform))

	var sharedRefreshErr error
	if s != nil && s.oauthRefresher != nil && strings.TrimSpace(item.Credentials["refresh_token"]) != "" {
		refreshed, err := s.oauthRefresher.RefreshAccountCredentials(
			ctx,
			item.ID,
			item.Name,
			item.Platform,
			item.Type,
			item.Credentials,
			proxyURL,
		)
		if err == nil {
			return s.applyRefreshedCredentials(ctx, item, refreshed)
		}
		sharedRefreshErr = err
	}

	switch platform {
	case "codex", "openai":
		refreshed, err := refreshCodexCredentialsDirect(ctx, item.Credentials, proxyURL)
		if err != nil {
			if sharedRefreshErr != nil {
				return fmt.Errorf("统一刷新失败: %v；Codex 直连刷新失败: %w", sharedRefreshErr, err)
			}
			return err
		}
		return s.applyRefreshedCredentials(ctx, item, refreshed)
	case "claude", "anthropic":
		refreshed, err := refreshClaudeCredentialsDirect(ctx, item.Credentials, proxyURL)
		if err != nil {
			if sharedRefreshErr != nil {
				return fmt.Errorf("统一刷新失败: %v；Claude 直连刷新失败: %w", sharedRefreshErr, err)
			}
			return err
		}
		return s.applyRefreshedCredentials(ctx, item, refreshed)
	case "xai", "grok":
		if err := s.refreshXAIAccessToken(ctx, item, proxyURL); err != nil {
			if sharedRefreshErr != nil {
				return fmt.Errorf("统一刷新失败: %v；xAI 直连刷新失败: %w", sharedRefreshErr, err)
			}
			return err
		}
		return nil
	default:
		if sharedRefreshErr != nil {
			return sharedRefreshErr
		}
		return fmt.Errorf("平台 %s 暂无 OAuth 刷新实现", item.Platform)
	}
}

func (s *Service) applyRefreshedCredentials(ctx context.Context, item *Account, refreshed map[string]string) error {
	if item == nil {
		return fmt.Errorf("账号为空")
	}
	merged := cloneStringMap(item.Credentials)
	if merged == nil {
		merged = map[string]string{}
	}
	for key, value := range refreshed {
		if strings.TrimSpace(value) != "" {
			merged[key] = strings.TrimSpace(value)
		}
	}
	normalizeCredentialKeys(merged)
	if strings.TrimSpace(merged["access_token"]) == "" {
		return fmt.Errorf("OAuth 刷新后缺少 access_token")
	}
	item.Credentials = merged
	item.PlanType = resolvePlanType(*item)
	item.SubscriptionActiveUntil = resolveSubscriptionActiveUntil(*item)
	if s != nil && s.repo != nil && item.ID > 0 {
		if _, err := s.Update(ctx, item.ID, UpdateInput{Credentials: merged}); err != nil {
			return fmt.Errorf("刷新凭证落库失败: %w", err)
		}
	}
	return nil
}

func refreshCodexCredentialsDirect(ctx context.Context, credentials map[string]string, proxyURL string) (map[string]string, error) {
	if sessionToken := strings.TrimSpace(credentials["session_token"]); sessionToken != "" {
		if exchanged, _, err := ExchangeCodexSession(ctx, sessionToken, proxyURL); err == nil && strings.TrimSpace(exchanged["access_token"]) != "" {
			return exchanged, nil
		} else if strings.TrimSpace(credentials["refresh_token"]) == "" {
			if err != nil {
				return nil, fmt.Errorf("session_token 刷新失败: %w", err)
			}
			return nil, fmt.Errorf("session_token 刷新后缺少 access_token")
		}
	}
	refreshToken := strings.TrimSpace(credentials["refresh_token"])
	if refreshToken == "" {
		return nil, fmt.Errorf("refresh_token 为空")
	}
	return ImportCodexRefreshToken(ctx, refreshToken, proxyURL, strings.TrimSpace(credentials["client_id"]))
}

func refreshClaudeCredentialsDirect(ctx context.Context, credentials map[string]string, proxyURL string) (map[string]string, error) {
	refreshToken := strings.TrimSpace(credentials["refresh_token"])
	if refreshToken == "" {
		return nil, fmt.Errorf("refresh_token 为空")
	}
	tokenURL := strings.TrimSpace(credentials["token_endpoint"])
	if tokenURL == "" {
		tokenURL = claudeOAuthTokenURL
	}
	payload := map[string]string{
		"client_id":     firstNonEmpty(strings.TrimSpace(credentials["client_id"]), claudeOAuthClientID),
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
		"scope":         firstNonEmpty(strings.TrimSpace(credentials["scope"]), claudeOAuthRefreshScope),
	}
	raw, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(string(raw)))
	if err != nil {
		return nil, fmt.Errorf("创建 Claude token 请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient(proxyURL).Do(req)
	if err != nil {
		return nil, fmt.Errorf("claude token 请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, testMaxBody))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &accountUpstreamHTTPError{status: resp.StatusCode, body: body, label: "Claude token"}
	}
	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("解析 Claude token 响应失败: %w", err)
	}
	if strings.TrimSpace(tokenResp.AccessToken) == "" {
		return nil, fmt.Errorf("claude token 响应缺少 access_token")
	}
	refreshed := map[string]string{
		"access_token":      strings.TrimSpace(tokenResp.AccessToken),
		"credential_origin": TypeOAuth,
		"token_endpoint":    tokenURL,
	}
	if strings.TrimSpace(tokenResp.RefreshToken) != "" {
		refreshed["refresh_token"] = strings.TrimSpace(tokenResp.RefreshToken)
	}
	if strings.TrimSpace(tokenResp.TokenType) != "" {
		refreshed["token_type"] = strings.TrimSpace(tokenResp.TokenType)
	}
	if tokenResp.ExpiresIn > 0 {
		refreshed["expires_in"] = fmt.Sprintf("%d", tokenResp.ExpiresIn)
		refreshed["expired"] = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second).UTC().Format(time.RFC3339)
	}
	refreshed["last_refresh"] = time.Now().UTC().Format(time.RFC3339)
	return refreshed, nil
}

func isRefreshableAccountAuthError(platform string, err error) bool {
	if err == nil {
		return false
	}
	statusCode := 0
	var statusErr interface{ StatusCode() int }
	if errors.As(err, &statusErr) && statusErr != nil {
		statusCode = statusErr.StatusCode()
	}
	return cpa.IsRefreshableAuthFailure(platform, statusCode, err.Error())
}
