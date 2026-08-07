package account

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
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/DouDOU-start/airgate-core/internal/pkg/logx"
)

// OAuth 会话状态。
const (
	OAuthStatusPending   = "pending"
	OAuthStatusCompleted = "completed"
	OAuthStatusFailed    = "failed"
)

const (
	antigravityOAuthClientIDEnv     = "ANTIGRAVITY_OAUTH_CLIENT_ID"
	antigravityOAuthClientSecretEnv = "ANTIGRAVITY_OAUTH_CLIENT_SECRET"
)

// OAuth 流程类型。
const (
	// OAuthFlowPasteCode 返回 authorize_url，用户浏览器完成登录后粘贴 code/回调 URL。
	OAuthFlowPasteCode = "paste_code"
	// OAuthFlowDevice 返回设备码 + 验证链接，后台轮询 token。
	OAuthFlowDevice = "device"
)

// OAuthStartInput 发起交互式 OAuth。
type OAuthStartInput struct {
	Platform       string
	Name           string
	ProxyURL       string
	GroupIDs       []int64
	ProxyID        *int64
	Priority       int
	Weight         int
	MaxConcurrency int
	RateMultiplier float64
	ProjectID      string
	// Mode 可选：browser（默认，paste_code）。Codex 已不再支持 device。
	Mode string
	// AccountID > 0 时为重新授权：完成后更新该账号凭证，不新建。
	AccountID int
}

// OAuthCompleteInput 粘贴 code 完成授权。
type OAuthCompleteInput struct {
	// Code 可为 authorization code、code#state、或完整回调 URL。
	Code string
}

// OAuthSession 交互式 OAuth 会话（返回给前端）。
type OAuthSession struct {
	ID       string `json:"id"`
	Platform string `json:"platform"`
	Status   string `json:"status"`
	// Flow: paste_code / device
	Flow    string `json:"flow"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`

	// 授权链接：前端直接展示、打开或复制
	AuthorizeURL string `json:"authorize_url,omitempty"`
	// device：设备码授权
	UserCode                string `json:"user_code,omitempty"`
	VerificationURI         string `json:"verification_uri,omitempty"`
	VerificationURIComplete string `json:"verification_uri_complete,omitempty"`

	AccountID   int       `json:"account_id,omitempty"`
	AccountName string    `json:"account_name,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type oauthSessionEntry struct {
	mu       sync.RWMutex
	actionMu sync.Mutex
	doneOnce sync.Once

	public OAuthSession
	input  OAuthStartInput

	// PKCE / 设备码私有态
	codeVerifier  string
	state         string
	redirectURI   string
	deviceCode    string
	pollInterval  time.Duration
	tokenEndpoint string

	done chan struct{}
}

type oauthSessionStore struct {
	mu       sync.RWMutex
	sessions map[string]*oauthSessionEntry
}

var globalOAuthSessions = &oauthSessionStore{sessions: make(map[string]*oauthSessionEntry)}

const oauthSessionTTL = 15 * time.Minute

// OAuthLoginHints 平台说明。
func OAuthLoginHints(platform string) map[string]any {
	p := strings.ToLower(strings.TrimSpace(platform))
	out := map[string]any{"platform": p}
	switch p {
	case "claude":
		out["flow"] = OAuthFlowPasteCode
		out["instruction"] = "点击「生成授权链接」后打开链接完成 Anthropic 登录。授权页会跳转到带 code 的地址，把地址栏里的 code（或完整 URL）粘贴回来完成绑定。"
	case "codex":
		out["flows"] = []string{OAuthFlowPasteCode, "import_refresh", "import_session"}
		out["instruction"] = "Codex 支持：浏览器授权、Refresh Token 导入、Session 导入。"
	case "antigravity":
		out["flow"] = OAuthFlowPasteCode
		out["instruction"] = "点击「生成授权链接」后打开 Google 授权。完成后浏览器会跳转到 localhost，把地址栏完整 URL 或 code 粘贴回来即可。"
	case "kimi":
		out["flow"] = OAuthFlowDevice
		out["instruction"] = "点击「生成授权链接」后展示验证链接与用户码，在 Kimi 页面确认授权即可，后台自动完成绑定。"
	case "xai":
		out["flow"] = OAuthFlowDevice
		out["instruction"] = "点击「生成授权链接」后展示验证链接与用户码，在 xAI 页面确认授权即可，后台自动完成绑定。"
	default:
		out["instruction"] = "该平台请手动粘贴凭证创建账号。"
	}
	return out
}

// StartOAuth 交互式发起：立即返回授权链接或设备码，前端展示后由用户完成授权。
// 传入 AccountID 时为重新授权：校验账号存在且平台一致，完成后更新凭证而非新建。
func (s *Service) StartOAuth(ctx context.Context, input OAuthStartInput) (OAuthSession, error) {
	platform := strings.ToLower(strings.TrimSpace(input.Platform))
	switch platform {
	case "claude", "codex", "antigravity", "kimi", "xai":
	default:
		return OAuthSession{}, fmt.Errorf("%w: %s", ErrUnsupportedPlatform, platform)
	}
	input.Platform = platform

	if err := s.prepareOAuthReauth(ctx, &input); err != nil {
		return OAuthSession{}, err
	}

	id := uuid.NewString()
	entry := &oauthSessionEntry{
		public: OAuthSession{
			ID:        id,
			Platform:  platform,
			Status:    OAuthStatusPending,
			CreatedAt: time.Now().UTC(),
		},
		input: input,
		done:  make(chan struct{}),
	}

	var err error
	switch platform {
	case "claude":
		err = s.startClaudeOAuth(entry)
	case "codex":
		if strings.EqualFold(strings.TrimSpace(input.Mode), "device") {
			return OAuthSession{}, fmt.Errorf("codex 已不再支持设备码授权，请使用浏览器授权、Refresh Token 或 Session 导入")
		}
		err = s.startCodexOAuth(entry)
	case "antigravity":
		err = s.startAntigravityOAuth(entry)
	case "kimi":
		err = s.startKimiOAuth(ctx, entry)
	case "xai":
		err = s.startXAIOauth(ctx, entry)
	}
	if err != nil {
		return OAuthSession{}, err
	}

	globalOAuthSessions.put(entry)
	public := entry.snapshot()
	if public.Flow == OAuthFlowDevice {
		go s.devicePollLoop(entry)
	}
	logx.LoggerFromContext(ctx).Info("account_oauth_interactive_start",
		"session_id", id,
		logx.LogFieldPlatform, platform,
		"flow", public.Flow,
		"has_authorize_url", public.AuthorizeURL != "",
		logx.LogFieldAccountID, input.AccountID)
	return public, nil
}

// prepareOAuthReauth 重新授权时校验目标账号，并补齐 ProxyURL/Name。
func (s *Service) prepareOAuthReauth(ctx context.Context, input *OAuthStartInput) error {
	if input == nil || input.AccountID <= 0 {
		return nil
	}
	item, err := s.FindByID(ctx, input.AccountID, LoadOptions{WithProxy: true})
	if err != nil {
		return err
	}
	want := strings.ToLower(strings.TrimSpace(input.Platform))
	got := strings.ToLower(strings.TrimSpace(item.Platform))
	if want != "" && got != "" && want != got {
		return fmt.Errorf("账号平台不匹配：期望 %s，实际 %s", want, item.Platform)
	}
	if input.Platform == "" {
		input.Platform = item.Platform
	}
	if strings.TrimSpace(input.Name) == "" {
		input.Name = item.Name
	}
	if strings.TrimSpace(input.ProxyURL) == "" {
		input.ProxyURL = proxyURLFromRef(item.Proxy)
	}
	return nil
}

// GetOAuthSession 查询会话（device 流会顺带推进一次 token 轮询）。
func (s *Service) GetOAuthSession(id string) (OAuthSession, error) {
	entry := globalOAuthSessions.get(id)
	if entry == nil {
		return OAuthSession{}, fmt.Errorf("OAuth 会话不存在或已过期")
	}
	public := entry.snapshot()
	if public.Status == OAuthStatusPending && public.Flow == OAuthFlowDevice {
		s.pollDeviceOnce(entry)
	}
	return entry.snapshot(), nil
}

// CompleteOAuth 用粘贴的 authorization code / 回调 URL 完成 paste_code 流程。
func (s *Service) CompleteOAuth(ctx context.Context, sessionID string, input OAuthCompleteInput) (OAuthSession, error) {
	entry := globalOAuthSessions.get(sessionID)
	if entry == nil {
		return OAuthSession{}, fmt.Errorf("OAuth 会话不存在或已过期")
	}
	entry.actionMu.Lock()
	defer entry.actionMu.Unlock()

	public := entry.snapshot()
	if public.Status != OAuthStatusPending {
		return public, nil
	}
	if public.Flow != OAuthFlowPasteCode {
		return OAuthSession{}, fmt.Errorf("当前会话不需要粘贴 code（设备码流程请在验证页完成授权）")
	}
	raw := strings.TrimSpace(input.Code)
	if raw == "" {
		return OAuthSession{}, fmt.Errorf("code 不能为空")
	}

	code, pastedState := extractAuthCode(raw)
	if code == "" {
		return OAuthSession{}, fmt.Errorf("未能从输入中解析 authorization code")
	}
	if pastedState != "" && entry.state != "" && pastedState != entry.state {
		return OAuthSession{}, fmt.Errorf("state 不匹配，请使用本次生成的授权链接完成登录")
	}
	if pastedState == "" {
		pastedState = entry.state
	}

	var (
		creds map[string]string
		err   error
	)
	switch public.Platform {
	case "claude":
		creds, err = exchangeClaudeCode(ctx, code, pastedState, entry.codeVerifier, entry.redirectURI, entry.input.ProxyURL)
	case "codex":
		creds, err = exchangeCodexCode(ctx, code, entry.codeVerifier, entry.redirectURI, entry.input.ProxyURL)
	case "antigravity":
		creds, err = exchangeAntigravityCode(ctx, code, entry.redirectURI, entry.input.ProxyURL)
	default:
		err = fmt.Errorf("平台 %s 不支持粘贴 code", public.Platform)
	}
	if err != nil {
		globalOAuthSessions.fail(sessionID, err.Error())
		if e := globalOAuthSessions.get(sessionID); e != nil {
			return e.snapshot(), err
		}
		return OAuthSession{}, err
	}
	if err := s.finishOAuthWithCredentials(ctx, entry, creds); err != nil {
		globalOAuthSessions.fail(sessionID, err.Error())
		if e := globalOAuthSessions.get(sessionID); e != nil {
			return e.snapshot(), err
		}
		return OAuthSession{}, err
	}
	return entry.snapshot(), nil
}

// ImportCodexRefresh 用 RT 换 token 并创建 oauth 账号；AccountID>0 时更新已有账号。
func (s *Service) ImportCodexRefresh(ctx context.Context, input OAuthStartInput, refreshToken, clientID string) (Account, error) {
	input.Platform = "codex"
	if err := s.prepareOAuthReauth(ctx, &input); err != nil {
		return Account{}, err
	}
	creds, err := ImportCodexRefreshToken(ctx, refreshToken, input.ProxyURL, clientID)
	if err != nil {
		return Account{}, err
	}
	return s.createFromCodexImport(ctx, input, creds, "")
}

// ImportCodexSession 用 session JSON/token 创建 oauth 账号；AccountID>0 时更新已有账号。
func (s *Service) ImportCodexSession(ctx context.Context, input OAuthStartInput, sessionRaw string) (Account, error) {
	input.Platform = "codex"
	if err := s.prepareOAuthReauth(ctx, &input); err != nil {
		return Account{}, err
	}
	creds, name, err := ExchangeCodexSession(ctx, sessionRaw, input.ProxyURL)
	if err != nil {
		return Account{}, err
	}
	return s.createFromCodexImport(ctx, input, creds, name)
}

func (s *Service) createFromCodexImport(ctx context.Context, input OAuthStartInput, creds map[string]string, fallbackName string) (Account, error) {
	var item Account
	var err error
	if input.AccountID > 0 {
		item, err = s.applyOAuthCredentials(ctx, input, "codex", creds)
	} else {
		name := strings.TrimSpace(input.Name)
		if name == "" {
			if e := creds["email"]; e != "" {
				name = e
			} else if fallbackName != "" {
				name = fallbackName
			} else {
				name = "codex-" + time.Now().Format("0102-1504")
			}
		}
		item, err = s.Create(ctx, CreateInput{
			Name:           name,
			Platform:       "codex",
			Type:           TypeOAuth,
			Credentials:    creds,
			Priority:       input.Priority,
			Weight:         input.Weight,
			MaxConcurrency: input.MaxConcurrency,
			ProxyID:        input.ProxyID,
			RateMultiplier: input.RateMultiplier,
			GroupIDs:       input.GroupIDs,
		})
	}
	if err != nil {
		return Account{}, err
	}
	return s.refreshUsageAfterImport(ctx, item), nil
}

// ---------- Claude (paste code) ----------

func (s *Service) startClaudeOAuth(entry *oauthSessionEntry) error {
	verifier, challenge, err := generatePKCE()
	if err != nil {
		return err
	}
	state, err := randomBase64URL(24)
	if err != nil {
		return err
	}
	entry.codeVerifier = verifier
	entry.state = state
	entry.redirectURI = "https://platform.claude.com/oauth/code/callback"

	q := url.Values{}
	q.Set("code", "true")
	q.Set("client_id", "9d1c250a-e61b-44d9-88ed-5944d1962f5e")
	q.Set("response_type", "code")
	q.Set("redirect_uri", entry.redirectURI)
	q.Set("scope", "org:create_api_key user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload")
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("state", state)

	entry.public.Flow = OAuthFlowPasteCode
	entry.public.AuthorizeURL = "https://claude.ai/oauth/authorize?" + q.Encode()
	entry.public.Message = "请打开授权链接完成登录，然后将回调页的 code（或完整 URL / code#state）粘贴回来。"
	return nil
}

func exchangeClaudeCode(ctx context.Context, code, state, verifier, redirectURI, proxyURL string) (map[string]string, error) {
	body := map[string]any{
		"code":          code,
		"grant_type":    "authorization_code",
		"client_id":     "9d1c250a-e61b-44d9-88ed-5944d1962f5e",
		"redirect_uri":  redirectURI,
		"code_verifier": verifier,
		"state":         state,
	}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://platform.claude.com/v1/oauth/token", strings.NewReader(string(raw)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient(proxyURL).Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("claude token 交换失败 HTTP %d: %s", resp.StatusCode, truncate(string(data), 200))
	}
	var tok struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
		Account      *struct {
			EmailAddress string `json:"email_address"`
			UUID         string `json:"uuid"`
		} `json:"account"`
	}
	if err := json.Unmarshal(data, &tok); err != nil {
		return nil, err
	}
	if tok.AccessToken == "" {
		return nil, fmt.Errorf("响应缺少 access_token")
	}
	creds := map[string]string{
		"access_token":      tok.AccessToken,
		"refresh_token":     tok.RefreshToken,
		"credential_origin": "oauth",
		"token_endpoint":    claudeOAuthTokenURL,
		"client_id":         claudeOAuthClientID,
	}
	if tok.Account != nil {
		if tok.Account.EmailAddress != "" {
			creds["email"] = tok.Account.EmailAddress
		}
		if tok.Account.UUID != "" {
			creds["account_uuid"] = tok.Account.UUID
		}
	}
	if tok.ExpiresIn > 0 {
		creds["expires_in"] = fmt.Sprintf("%d", tok.ExpiresIn)
		creds["expired"] = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second).UTC().Format(time.RFC3339)
	}
	return creds, nil
}

// ---------- Codex (paste code，redirect 仍用官方 localhost) ----------

func (s *Service) startCodexOAuth(entry *oauthSessionEntry) error {
	verifier, challenge, err := generatePKCE()
	if err != nil {
		return err
	}
	state, err := randomBase64URL(24)
	if err != nil {
		return err
	}
	redirectURI := "http://localhost:1455/auth/callback"
	entry.codeVerifier = verifier
	entry.state = state
	entry.redirectURI = redirectURI

	q := url.Values{}
	q.Set("client_id", "app_EMoamEEZ73f0CkXaXp7hrann")
	q.Set("scope", "openid profile email offline_access api.connectors.read api.connectors.invoke")
	q.Set("response_type", "code")
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("id_token_add_organizations", "true")
	q.Set("codex_cli_simplified_flow", "true")

	entry.public.Flow = OAuthFlowPasteCode
	entry.public.AuthorizeURL = "https://auth.openai.com/oauth/authorize?" + q.Encode()
	entry.public.Message = "请打开授权链接完成 ChatGPT 登录。跳转到 localhost 后（可能无法连接），复制地址栏完整 URL 或 code 参数粘贴回来。"
	return nil
}

func exchangeCodexCode(ctx context.Context, code, verifier, redirectURI, proxyURL string) (map[string]string, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", "app_EMoamEEZ73f0CkXaXp7hrann")
	form.Set("code_verifier", verifier)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://auth.openai.com/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "codex-cli/0.91.0")

	resp, err := httpClient(proxyURL).Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("codex token 交换失败 HTTP %d: %s", resp.StatusCode, truncate(string(data), 200))
	}
	var tok struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(data, &tok); err != nil {
		return nil, err
	}
	if tok.AccessToken == "" {
		return nil, fmt.Errorf("响应缺少 access_token")
	}
	creds := map[string]string{
		"access_token":      tok.AccessToken,
		"refresh_token":     tok.RefreshToken,
		"id_token":          tok.IDToken,
		"credential_origin": "oauth",
	}
	if tok.ExpiresIn > 0 {
		creds["expires_in"] = fmt.Sprintf("%d", tok.ExpiresIn)
		creds["expired"] = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second).UTC().Format(time.RFC3339)
	}
	applyCodexIDTokenClaims(creds, tok.IDToken)
	return creds, nil
}

// ---------- Antigravity (paste code) ----------

func (s *Service) startAntigravityOAuth(entry *oauthSessionEntry) error {
	clientID, _, err := antigravityOAuthClientCredentials()
	if err != nil {
		return err
	}
	state, err := randomBase64URL(24)
	if err != nil {
		return err
	}
	redirectURI := "http://localhost:51121/oauth-callback"
	entry.state = state
	entry.redirectURI = redirectURI

	scopes := []string{
		"https://www.googleapis.com/auth/cloud-platform",
		"https://www.googleapis.com/auth/userinfo.email",
		"https://www.googleapis.com/auth/userinfo.profile",
		"https://www.googleapis.com/auth/cclog",
		"https://www.googleapis.com/auth/experimentsandconfigs",
	}
	q := url.Values{}
	q.Set("access_type", "offline")
	q.Set("client_id", clientID)
	q.Set("prompt", "consent")
	q.Set("redirect_uri", redirectURI)
	q.Set("response_type", "code")
	q.Set("scope", strings.Join(scopes, " "))
	q.Set("state", state)

	entry.public.Flow = OAuthFlowPasteCode
	entry.public.AuthorizeURL = "https://accounts.google.com/o/oauth2/v2/auth?" + q.Encode()
	entry.public.Message = "请打开授权链接完成 Google 登录。跳转到 localhost 后，复制地址栏完整 URL 或 code 参数粘贴回来。"
	return nil
}

func exchangeAntigravityCode(ctx context.Context, code, redirectURI, proxyURL string) (map[string]string, error) {
	clientID, clientSecret, err := antigravityOAuthClientCredentials()
	if err != nil {
		return nil, err
	}
	form := url.Values{}
	form.Set("code", code)
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)
	form.Set("redirect_uri", redirectURI)
	form.Set("grant_type", "authorization_code")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://oauth2.googleapis.com/token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := httpClient(proxyURL).Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("antigravity token 交换失败 HTTP %d: %s", resp.StatusCode, truncate(string(data), 200))
	}
	var tok struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
		IDToken      string `json:"id_token"`
	}
	if err := json.Unmarshal(data, &tok); err != nil {
		return nil, err
	}
	if tok.AccessToken == "" {
		return nil, fmt.Errorf("响应缺少 access_token")
	}
	creds := map[string]string{
		"access_token":      tok.AccessToken,
		"refresh_token":     tok.RefreshToken,
		"id_token":          tok.IDToken,
		"credential_origin": "oauth",
	}
	if email := fetchGoogleEmail(ctx, tok.AccessToken, proxyURL); email != "" {
		creds["email"] = email
	}
	if tok.ExpiresIn > 0 {
		creds["expired"] = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second).UTC().Format(time.RFC3339)
	}
	return creds, nil
}

func antigravityOAuthClientCredentials() (clientID, clientSecret string, err error) {
	clientID = strings.TrimSpace(os.Getenv(antigravityOAuthClientIDEnv))
	clientSecret = strings.TrimSpace(os.Getenv(antigravityOAuthClientSecretEnv))
	if clientID == "" || clientSecret == "" {
		return "", "", fmt.Errorf("antigravity OAuth 未配置，请设置环境变量 %s 和 %s", antigravityOAuthClientIDEnv, antigravityOAuthClientSecretEnv)
	}
	return clientID, clientSecret, nil
}

func fetchGoogleEmail(ctx context.Context, accessToken, proxyURL string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.googleapis.com/oauth2/v2/userinfo?alt=json", nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := httpClient(proxyURL).Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	var u struct {
		Email string `json:"email"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&u)
	return strings.TrimSpace(u.Email)
}

// ---------- Kimi device ----------

func (s *Service) startKimiOAuth(ctx context.Context, entry *oauthSessionEntry) error {
	deviceID := uuid.NewString()
	form := url.Values{}
	form.Set("client_id", "17e5f671-d194-4dfb-9706-5516cb48c098")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://auth.kimi.com/api/oauth/device_authorization", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Msh-Platform", "AirGate")
	req.Header.Set("X-Msh-Device-Id", deviceID)

	resp, err := httpClient(entry.input.ProxyURL).Do(req)
	if err != nil {
		return fmt.Errorf("kimi 设备码请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("kimi 设备码 HTTP %d: %s", resp.StatusCode, truncate(string(data), 200))
	}
	var dc struct {
		DeviceCode              string `json:"device_code"`
		UserCode                string `json:"user_code"`
		VerificationURI         string `json:"verification_uri"`
		VerificationURIComplete string `json:"verification_uri_complete"`
		ExpiresIn               int    `json:"expires_in"`
		Interval                int    `json:"interval"`
	}
	if err := json.Unmarshal(data, &dc); err != nil {
		return err
	}
	entry.deviceCode = dc.DeviceCode
	entry.codeVerifier = deviceID
	entry.pollInterval = time.Duration(dc.Interval) * time.Second
	if entry.pollInterval < 5*time.Second {
		entry.pollInterval = 5 * time.Second
	}

	entry.public.Flow = OAuthFlowDevice
	entry.public.UserCode = dc.UserCode
	entry.public.VerificationURI = dc.VerificationURI
	entry.public.VerificationURIComplete = dc.VerificationURIComplete
	entry.public.AuthorizeURL = firstNonEmpty(dc.VerificationURIComplete, dc.VerificationURI)
	entry.public.Message = "请打开验证链接并输入用户码完成授权，完成后会自动创建账号。"

	return nil
}

// ---------- xAI device ----------

func (s *Service) startXAIOauth(ctx context.Context, entry *oauthSessionEntry) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://auth.x.ai/.well-known/openid-configuration", nil)
	if err != nil {
		return err
	}
	resp, err := httpClient(entry.input.ProxyURL).Do(req)
	if err != nil {
		return fmt.Errorf("xAI discovery 失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var disc struct {
		DeviceAuthorizationEndpoint string `json:"device_authorization_endpoint"`
		TokenEndpoint               string `json:"token_endpoint"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&disc); err != nil {
		return err
	}
	entry.tokenEndpoint = disc.TokenEndpoint

	form := url.Values{}
	form.Set("client_id", "b1a00492-073a-47ea-816f-4c329264a828")
	form.Set("scope", "openid profile email offline_access grok-cli:access api:access")
	req2, err := http.NewRequestWithContext(ctx, http.MethodPost, disc.DeviceAuthorizationEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp2, err := httpClient(entry.input.ProxyURL).Do(req2)
	if err != nil {
		return fmt.Errorf("xAI 设备码请求失败: %w", err)
	}
	defer func() { _ = resp2.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp2.Body, 1<<20))
	if resp2.StatusCode != http.StatusOK {
		return fmt.Errorf("xAI 设备码 HTTP %d: %s", resp2.StatusCode, truncate(string(data), 200))
	}
	var dc struct {
		DeviceCode              string `json:"device_code"`
		UserCode                string `json:"user_code"`
		VerificationURI         string `json:"verification_uri"`
		VerificationURIComplete string `json:"verification_uri_complete"`
		ExpiresIn               int    `json:"expires_in"`
		Interval                int    `json:"interval"`
	}
	if err := json.Unmarshal(data, &dc); err != nil {
		return err
	}
	entry.deviceCode = dc.DeviceCode
	entry.pollInterval = time.Duration(dc.Interval) * time.Second
	if entry.pollInterval < 5*time.Second {
		entry.pollInterval = 5 * time.Second
	}

	entry.public.Flow = OAuthFlowDevice
	entry.public.UserCode = dc.UserCode
	entry.public.VerificationURI = dc.VerificationURI
	entry.public.VerificationURIComplete = dc.VerificationURIComplete
	entry.public.AuthorizeURL = firstNonEmpty(dc.VerificationURIComplete, dc.VerificationURI)
	entry.public.Message = "请打开验证链接并输入用户码完成授权，完成后会自动创建账号。"

	return nil
}

// ---------- shared finish / poll ----------

func (s *Service) devicePollLoop(entry *oauthSessionEntry) {
	deadline := time.Now().Add(15 * time.Minute)
	ticker := time.NewTicker(entry.pollInterval)
	defer ticker.Stop()
	for {
		if entry.snapshot().Status != OAuthStatusPending {
			return
		}
		if time.Now().After(deadline) {
			entry.actionMu.Lock()
			globalOAuthSessions.fail(entry.snapshot().ID, "授权超时")
			entry.actionMu.Unlock()
			return
		}
		select {
		case <-entry.done:
			return
		case <-ticker.C:
			s.pollDeviceOnce(entry)
			if entry.snapshot().Status != OAuthStatusPending {
				return
			}
		}
	}
}

func (s *Service) pollDeviceOnce(entry *oauthSessionEntry) {
	entry.actionMu.Lock()
	defer entry.actionMu.Unlock()

	public := entry.snapshot()
	if public.Status != OAuthStatusPending || entry.deviceCode == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var (
		creds   map[string]string
		err     error
		pending bool
	)
	switch public.Platform {
	case "kimi":
		creds, pending, err = pollKimiToken(ctx, entry.deviceCode, entry.codeVerifier, entry.input.ProxyURL)
	case "xai":
		creds, pending, err = pollXAIToken(ctx, entry.deviceCode, entry.tokenEndpoint, entry.input.ProxyURL)
	default:
		return
	}
	if pending {
		return
	}
	if err != nil {
		globalOAuthSessions.fail(public.ID, err.Error())
		return
	}
	if err := s.finishOAuthWithCredentials(ctx, entry, creds); err != nil {
		globalOAuthSessions.fail(public.ID, err.Error())
	}
}

func pollKimiToken(ctx context.Context, deviceCode, deviceID, proxyURL string) (map[string]string, bool, error) {
	form := url.Values{}
	form.Set("client_id", "17e5f671-d194-4dfb-9706-5516cb48c098")
	form.Set("device_code", deviceCode)
	form.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://auth.kimi.com/api/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Msh-Device-Id", deviceID)
	resp, err := httpClient(proxyURL).Do(req)
	if err != nil {
		return nil, true, nil
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var body map[string]any
	_ = json.Unmarshal(data, &body)
	errCode, _ := body["error"].(string)
	if errCode == "authorization_pending" || errCode == "slow_down" {
		return nil, true, nil
	}
	if resp.StatusCode != http.StatusOK {
		if errCode != "" {
			return nil, false, fmt.Errorf("kimi: %s", errCode)
		}
		return nil, false, fmt.Errorf("kimi token HTTP %d", resp.StatusCode)
	}
	access, _ := body["access_token"].(string)
	refresh, _ := body["refresh_token"].(string)
	if access == "" {
		return nil, false, fmt.Errorf("kimi 响应缺少 access_token")
	}
	creds := map[string]string{
		"access_token":      access,
		"refresh_token":     refresh,
		"device_id":         deviceID,
		"credential_origin": "oauth",
		"type":              "kimi",
	}
	if tokenType, _ := body["token_type"].(string); strings.TrimSpace(tokenType) != "" {
		creds["token_type"] = strings.TrimSpace(tokenType)
	}
	if expiresIn := anyToInt(body["expires_in"]); expiresIn > 0 {
		creds["expires_in"] = fmt.Sprintf("%d", expiresIn)
		creds["expired"] = time.Now().Add(time.Duration(expiresIn) * time.Second).UTC().Format(time.RFC3339)
	}
	return creds, false, nil
}

func pollXAIToken(ctx context.Context, deviceCode, tokenEndpoint, proxyURL string) (map[string]string, bool, error) {
	if tokenEndpoint == "" {
		tokenEndpoint = "https://auth.x.ai/oauth2/token"
	}
	form := url.Values{}
	form.Set("client_id", "b1a00492-073a-47ea-816f-4c329264a828")
	form.Set("device_code", deviceCode)
	form.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := httpClient(proxyURL).Do(req)
	if err != nil {
		return nil, true, nil
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var body map[string]any
	_ = json.Unmarshal(data, &body)
	errCode, _ := body["error"].(string)
	if errCode == "authorization_pending" || errCode == "slow_down" {
		return nil, true, nil
	}
	if resp.StatusCode != http.StatusOK {
		if errCode != "" {
			return nil, false, fmt.Errorf("xAI: %s", errCode)
		}
		return nil, false, fmt.Errorf("xAI token HTTP %d", resp.StatusCode)
	}
	access, _ := body["access_token"].(string)
	refresh, _ := body["refresh_token"].(string)
	if access == "" {
		return nil, false, fmt.Errorf("xAI 响应缺少 access_token")
	}
	creds := map[string]string{
		"access_token":      access,
		"refresh_token":     refresh,
		"auth_kind":         TypeOAuth,
		"credential_origin": TypeOAuth,
		"token_endpoint":    tokenEndpoint,
		"type":              "xai",
	}
	if tokenType, _ := body["token_type"].(string); strings.TrimSpace(tokenType) != "" {
		creds["token_type"] = strings.TrimSpace(tokenType)
	}
	if expiresIn := anyToInt(body["expires_in"]); expiresIn > 0 {
		creds["expires_in"] = fmt.Sprintf("%d", expiresIn)
		creds["expired"] = time.Now().Add(time.Duration(expiresIn) * time.Second).UTC().Format(time.RFC3339)
	}
	if idt, _ := body["id_token"].(string); idt != "" {
		creds["id_token"] = idt
		applyXAIIdentityClaims(creds, idt)
	}
	return creds, false, nil
}

// applyXAIIdentityClaims 从 xAI id_token 写入 email / subject / plan_type（若 claim 中有）。
// subject 会作为 billing 请求的 x-userid（对齐 CPA Manager）。
func applyXAIIdentityClaims(creds map[string]string, idToken string) {
	if creds == nil {
		return
	}
	claims := parseJWTPayload(idToken)
	if claims == nil {
		return
	}
	if email, _ := claims["email"].(string); strings.TrimSpace(email) != "" {
		creds["email"] = strings.TrimSpace(email)
	}
	if name, _ := claims["name"].(string); strings.TrimSpace(name) != "" {
		creds["name"] = strings.TrimSpace(name)
	}
	if sub := firstNonEmpty(
		claimToString(claims["sub"]),
		claimToString(claims["user_id"]),
		claimToString(claims["userId"]),
	); sub != "" {
		creds["subject"] = sub
	}
	if plan := planTypeFromClaims(claims); plan != "" {
		creds["plan_type"] = plan
	}
}

func (s *Service) finishOAuthWithCredentials(ctx context.Context, entry *oauthSessionEntry, creds map[string]string) error {
	public := entry.snapshot()
	var account Account
	var err error
	if entry.input.AccountID > 0 {
		account, err = s.applyOAuthCredentials(ctx, entry.input, public.Platform, creds)
	} else {
		name := strings.TrimSpace(entry.input.Name)
		if name == "" {
			if e := creds["email"]; e != "" {
				name = e
			} else {
				name = public.Platform + "-" + time.Now().Format("0102-1504")
			}
		}
		account, err = s.Create(ctx, CreateInput{
			Name:           name,
			Platform:       public.Platform,
			Type:           "oauth",
			Credentials:    creds,
			Priority:       entry.input.Priority,
			Weight:         entry.input.Weight,
			MaxConcurrency: entry.input.MaxConcurrency,
			ProxyID:        entry.input.ProxyID,
			RateMultiplier: entry.input.RateMultiplier,
			GroupIDs:       entry.input.GroupIDs,
		})
	}
	if err != nil {
		return err
	}
	reauth := entry.input.AccountID > 0
	globalOAuthSessions.complete(public.ID, account.ID, account.Name, reauth)
	return nil
}

// applyOAuthCredentials 将新 OAuth 凭证合并写入已有账号，并恢复为 active。
// 保留名称/分组/优先级等调度配置，仅更新凭证与类型。
func (s *Service) applyOAuthCredentials(ctx context.Context, input OAuthStartInput, platform string, creds map[string]string) (Account, error) {
	if input.AccountID <= 0 {
		return Account{}, fmt.Errorf("缺少重新授权目标账号")
	}
	existing, err := s.FindByID(ctx, input.AccountID, LoadOptions{})
	if err != nil {
		return Account{}, err
	}
	want := strings.ToLower(strings.TrimSpace(platform))
	got := strings.ToLower(strings.TrimSpace(existing.Platform))
	if want != "" && got != "" && want != got {
		return Account{}, fmt.Errorf("账号平台不匹配：期望 %s，实际 %s", want, existing.Platform)
	}
	typ := TypeOAuth
	active := StateActive
	return s.Update(ctx, input.AccountID, UpdateInput{
		Type:        &typ,
		Credentials: creds,
		State:       &active,
	})
}

// ---------- session store ----------

func (s *oauthSessionStore) put(entry *oauthSessionEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[entry.snapshot().ID] = entry
	cutoff := time.Now().Add(-oauthSessionTTL)
	for id, e := range s.sessions {
		if e.snapshot().CreatedAt.Before(cutoff) {
			e.stop()
			delete(s.sessions, id)
		}
	}
}

func (s *oauthSessionStore) get(id string) *oauthSessionEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessions[id]
}

func (s *oauthSessionStore) fail(id, msg string) {
	if e := s.get(id); e != nil {
		e.fail(msg)
	}
}

func (s *oauthSessionStore) complete(id string, accountID int, name string, reauth bool) {
	if e := s.get(id); e != nil {
		e.complete(accountID, name, reauth)
	}
}

func (e *oauthSessionEntry) snapshot() OAuthSession {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.public
}

func (e *oauthSessionEntry) fail(msg string) {
	e.mu.Lock()
	if e.public.Status != OAuthStatusPending {
		e.mu.Unlock()
		return
	}
	e.public.Status = OAuthStatusFailed
	e.public.Error = msg
	e.public.Message = "授权失败"
	e.mu.Unlock()
	e.stop()
}

func (e *oauthSessionEntry) complete(accountID int, name string, reauth bool) {
	e.mu.Lock()
	if e.public.Status != OAuthStatusPending {
		e.mu.Unlock()
		return
	}
	e.public.Status = OAuthStatusCompleted
	e.public.AccountID = accountID
	e.public.AccountName = name
	if reauth {
		e.public.Message = "重新授权成功，凭证已更新"
	} else {
		e.public.Message = "授权成功，账号已创建"
	}
	e.public.Error = ""
	e.mu.Unlock()
	e.stop()
}

func (e *oauthSessionEntry) stop() {
	e.doneOnce.Do(func() { close(e.done) })
}

// ---------- helpers ----------

// extractAuthCode 从纯 code、code#state、或完整回调 URL 中解析 authorization code。
func extractAuthCode(raw string) (code, state string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}

	// 完整 URL
	if strings.Contains(raw, "://") || strings.HasPrefix(raw, "http") {
		if u, err := url.Parse(raw); err == nil {
			code = firstNonEmpty(u.Query().Get("code"), u.Query().Get("authorization_code"))
			state = u.Query().Get("state")
			// Claude 有时把 code#state 放在 fragment
			if frag := strings.TrimSpace(u.Fragment); frag != "" {
				if code == "" {
					if idx := strings.Index(frag, "#"); idx >= 0 {
						code = frag[:idx]
						if state == "" {
							state = frag[idx+1:]
						}
					} else if !strings.Contains(frag, "=") {
						code = frag
					}
				}
			}
			return strings.TrimSpace(code), strings.TrimSpace(state)
		}
	}

	// code#state
	if idx := strings.Index(raw, "#"); idx >= 0 && !strings.Contains(raw, "=") {
		return strings.TrimSpace(raw[:idx]), strings.TrimSpace(raw[idx+1:])
	}

	// 纯 code，或 query 片段 code=...
	if strings.Contains(raw, "code=") {
		if !strings.Contains(raw, "://") {
			raw = "http://local/?" + strings.TrimPrefix(raw, "?")
		}
		if u, err := url.Parse(raw); err == nil {
			return strings.TrimSpace(u.Query().Get("code")), strings.TrimSpace(u.Query().Get("state"))
		}
	}
	return raw, ""
}

func generatePKCE() (verifier, challenge string, err error) {
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

func randomBase64URL(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func httpClient(proxyURL string) *http.Client {
	c := &http.Client{Timeout: 60 * time.Second}
	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL == "" {
		return c
	}
	u, err := url.Parse(proxyURL)
	if err != nil {
		return c
	}
	c.Transport = &http.Transport{Proxy: http.ProxyURL(u)}
	return c
}

func parseJWTPayload(jwt string) map[string]any {
	parts := strings.Split(jwt, ".")
	if len(parts) < 2 {
		return nil
	}
	payload := parts[1]
	if m := len(payload) % 4; m != 0 {
		payload += strings.Repeat("=", 4-m)
	}
	raw, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		raw, err = base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil {
			return nil
		}
	}
	var claims map[string]any
	if json.Unmarshal(raw, &claims) != nil {
		return nil
	}
	return claims
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
