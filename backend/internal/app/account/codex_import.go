package account

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Codex 导入方式：
//  1. 浏览器授权（authorize + paste callback）— 见 oauth.go startCodexOAuth
//  2. Refresh Token 导入 — ImportCodexRefreshToken
//  3. Session 导入 — ImportCodexSession
// 设备码已移除。

const (
	codexOAuthClientID      = "app_EMoamEEZ73f0CkXaXp7hrann"
	codexOAuthTokenURL      = "https://auth.openai.com/oauth/token"
	chatGPTSessionURL       = "https://chatgpt.com/api/auth/session"
	chatGPTSessionCookie    = "__Secure-next-auth.session-token"
	chatGPTBrowserUserAgent = `Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36 Edg/131.0.0.0`
)

// ImportCodexRefreshToken 用 refresh_token 换取 access_token 等 OAuth 凭证。
// 与 airgate-openai import-refresh 对齐：JSON body + client_id/grant_type/refresh_token。
func ImportCodexRefreshToken(ctx context.Context, refreshToken, proxyURL, clientID string) (map[string]string, error) {
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return nil, fmt.Errorf("refresh_token 不能为空")
	}
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		clientID = codexOAuthClientID
	}

	payload, err := json.Marshal(map[string]string{
		"client_id":     clientID,
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, codexOAuthTokenURL, strings.NewReader(string(payload)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "codex-cli/0.91.0")

	resp, err := httpClient(proxyURL).Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 token 端点失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode >= 400 {
		// 管理员诊断依赖上游原文（error / error_description / 整段 body）
		return nil, fmt.Errorf("%s", formatUpstreamHTTPError(resp.StatusCode, data))
	}
	var tok struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(data, &tok); err != nil {
		return nil, fmt.Errorf("解析 token 响应失败: %w\n---\n%s", err, truncate(strings.TrimSpace(string(data)), 2000))
	}
	if tok.AccessToken == "" {
		return nil, fmt.Errorf("刷新响应缺少 access_token\n---\n%s", truncate(strings.TrimSpace(string(data)), 2000))
	}

	nextRT := tok.RefreshToken
	if nextRT == "" {
		nextRT = refreshToken
	}
	creds := map[string]string{
		"access_token":      tok.AccessToken,
		"refresh_token":     nextRT,
		"credential_origin": "import_refresh",
	}
	if tok.IDToken != "" {
		creds["id_token"] = tok.IDToken
	}
	if clientID != "" {
		creds["client_id"] = clientID
	}
	applyCodexIDTokenClaims(creds, tok.IDToken)
	if tok.ExpiresIn > 0 {
		creds["expired"] = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second).UTC().Format(time.RFC3339)
	}
	return creds, nil
}

// ---------- Session 导入 ----------

type codexSessionResponse struct {
	User struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Email string `json:"email"`
	} `json:"user"`
	Expires string `json:"expires"`
	Account struct {
		ID       string `json:"id"`
		PlanType string `json:"planType"`
	} `json:"account"`
	AccessToken  string `json:"accessToken"`
	SessionToken string `json:"sessionToken"`
	AuthProvider string `json:"authProvider"`
}

// ExchangeCodexSession 接受 chatgpt.com /api/auth/session JSON 或裸 session_token。
// 对齐 airgate-openai ImportFromSessionJSON。
func ExchangeCodexSession(ctx context.Context, raw, proxyURL string) (creds map[string]string, accountName string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, "", fmt.Errorf("session 输入不能为空")
	}

	sess, err := parseCodexSessionJSON(raw)
	if err != nil {
		if strings.HasPrefix(raw, "{") {
			return nil, "", err
		}
		// 裸 session_token：走 /api/auth/session 刷新
		refreshed, refreshErr := refreshCodexViaSession(ctx, raw, proxyURL)
		if refreshErr != nil {
			return nil, "", fmt.Errorf("既不是合法的 session JSON，也无法当作 session_token 刷新: %w", refreshErr)
		}
		sess = refreshed
	}

	creds, accountName = credentialsFromCodexSession(sess)
	return creds, accountName, nil
}

func parseCodexSessionJSON(raw string) (*codexSessionResponse, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("session JSON 不能为空")
	}
	if !strings.HasPrefix(raw, "{") {
		return nil, fmt.Errorf("不是合法的 JSON 对象")
	}
	var sess codexSessionResponse
	if err := json.Unmarshal([]byte(raw), &sess); err != nil {
		return nil, fmt.Errorf("解析 session JSON 失败: %w", err)
	}
	if strings.TrimSpace(sess.AccessToken) == "" {
		return nil, fmt.Errorf("session JSON 缺少 accessToken")
	}
	return &sess, nil
}

func refreshCodexViaSession(ctx context.Context, sessionToken, proxyURL string) (*codexSessionResponse, error) {
	sessionToken = strings.TrimSpace(sessionToken)
	if sessionToken == "" {
		return nil, fmt.Errorf("session_token 不能为空")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, chatGPTSessionURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Referer", "https://chatgpt.com/")
	req.Header.Set("User-Agent", chatGPTBrowserUserAgent)
	req.AddCookie(&http.Cookie{Name: chatGPTSessionCookie, Value: sessionToken})

	resp, err := httpClient(proxyURL).Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 session 端点失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("session 刷新失败: %s", formatUpstreamHTTPError(resp.StatusCode, body))
	}
	var sess codexSessionResponse
	if err := json.Unmarshal(body, &sess); err != nil {
		return nil, fmt.Errorf("解析 session 响应失败: %w\n---\n%s", err, truncate(strings.TrimSpace(string(body)), 2000))
	}
	if strings.TrimSpace(sess.AccessToken) == "" {
		return nil, fmt.Errorf("session 端点未返回 accessToken（session_token 可能已失效）\n---\n%s", truncate(strings.TrimSpace(string(body)), 2000))
	}
	if strings.TrimSpace(sess.SessionToken) == "" {
		sess.SessionToken = sessionToken
	}
	return &sess, nil
}

func credentialsFromCodexSession(sess *codexSessionResponse) (map[string]string, string) {
	creds := map[string]string{
		"access_token":      sess.AccessToken,
		"session_token":     sess.SessionToken,
		"credential_origin": "import_session",
	}
	if sess.Account.ID != "" {
		creds["chatgpt_account_id"] = sess.Account.ID
	}
	if sess.User.Email != "" {
		creds["email"] = sess.User.Email
	}
	if sess.Account.PlanType != "" {
		creds["plan_type"] = sess.Account.PlanType
	}
	if sess.Expires != "" {
		creds["subscription_active_until"] = sess.Expires
	}
	name := sess.User.Name
	if name == "" {
		name = sess.User.Email
	}
	return creds, name
}

// applyCodexIDTokenClaims 从 id_token 解析账号/订阅信息（对齐 airgate-openai parseIDToken）。
// 写入：email、chatgpt_account_id、plan_type、subscription_active_until。
func applyCodexIDTokenClaims(creds map[string]string, idToken string) {
	if creds == nil {
		return
	}
	claims := parseJWTPayload(idToken)
	if claims == nil {
		return
	}
	if email, _ := claims["email"].(string); email != "" {
		creds["email"] = email
	}
	if pt, _ := claims["chatgpt_plan_type"].(string); strings.TrimSpace(pt) != "" {
		creds["plan_type"] = strings.TrimSpace(pt)
	}
	if until := claimToString(claims["chatgpt_subscription_active_until"]); until != "" {
		creds["subscription_active_until"] = until
	}
	if auth, ok := claims["https://api.openai.com/auth"].(map[string]any); ok {
		if id, _ := auth["chatgpt_account_id"].(string); id != "" {
			creds["chatgpt_account_id"] = id
		}
		if pt, _ := auth["chatgpt_plan_type"].(string); strings.TrimSpace(pt) != "" {
			creds["plan_type"] = strings.TrimSpace(pt)
		}
		if until := claimToString(auth["chatgpt_subscription_active_until"]); until != "" {
			creds["subscription_active_until"] = until
		}
	}
}

// claimToString 将 JWT claim（string / float64 / json.Number）规范为展示用字符串。
// 数字按 unix 秒（或毫秒）转 RFC3339。
func claimToString(v any) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case float64:
		sec := int64(t)
		if sec > 1e12 {
			sec = sec / 1000
		}
		if sec <= 0 {
			return ""
		}
		return time.Unix(sec, 0).UTC().Format(time.RFC3339)
	case json.Number:
		if f, err := t.Float64(); err == nil {
			return claimToString(f)
		}
		return strings.TrimSpace(t.String())
	case int64:
		return claimToString(float64(t))
	case int:
		return claimToString(float64(t))
	default:
		s := strings.TrimSpace(fmt.Sprint(t))
		if s == "" || s == "<nil>" {
			return ""
		}
		return s
	}
}
