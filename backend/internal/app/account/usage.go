package account

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/DouDOU-start/airgate-core/internal/pkg/logx"
)

// 用量快照约定（对齐 airgate-openai UsageView + airgate-claude windows）：
// 统一下发 used_percent / resets_at，前端进度条展示。

const (
	usageExtraKey               = "usage"
	usageStaleAfter             = 15 * time.Minute
	claudeUsageURL              = "https://api.anthropic.com/api/oauth/usage"
	claudeBetaOAuth             = "oauth-2025-04-20"
	codexUsageURL               = "https://chatgpt.com/backend-api/wham/usage"
	codexResetCreditsURL        = "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits"
	codexResetCreditsConsumeURL = "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits/consume"
	codexOriginator             = "codex_cli_rs"
	// xAI / Grok 用量：对齐 CPA Manager（cpa-manager-plus）billing 探测。
	// weekly  → GET cli-chat-proxy.grok.com/v1/billing?format=credits
	// monthly → GET cli-chat-proxy.grok.com/v1/billing
	// free-usage-exhausted 仅作为 billing 失败时的补充证据。
	xaiBillingWeeklyURL        = "https://cli-chat-proxy.grok.com/v1/billing?format=credits"
	xaiBillingMonthlyURL       = "https://cli-chat-proxy.grok.com/v1/billing"
	xaiGrokClientVersion       = "0.2.101"
	xaiGrokUserAgent           = "grok-pager/0.2.101 grok-shell/0.2.101 (macos; aarch64)"
	initialUsageRefreshTimeout = 20 * time.Second
	// SuperGrok 月额度（美分）：$150 / $1500，对齐 CPA resolveXaiPlan。
	xaiSuperGrokLimitCents      = 15_000.0
	xaiSuperGrokHeavyLimitCents = 150_000.0
)

// ErrUsageNotSupported 当前平台/凭证无法查询用量窗口。
var ErrUsageNotSupported = errors.New("该账号不支持用量窗口查询")

// UsageWindow 单个限流窗口。
type UsageWindow struct {
	Key           string     `json:"key"` // 5h / weekly / daily / ...
	Label         string     `json:"label,omitempty"`
	UsedPercent   float64    `json:"used_percent"`
	WindowMinutes int        `json:"window_minutes,omitempty"`
	ResetsAt      *time.Time `json:"resets_at,omitempty"`
	LimitID       string     `json:"limit_id,omitempty"`
	LimitName     string     `json:"limit_name,omitempty"`
}

// UsageCredits 积分余额（Codex 等）。
type UsageCredits struct {
	HasCredits bool   `json:"has_credits"`
	Unlimited  bool   `json:"unlimited"`
	Balance    string `json:"balance,omitempty"`
}

// UsageSnapshot 账号用量窗口快照（存 extra.usage，列表/刷新接口回显）。
type UsageSnapshot struct {
	CapturedAt            time.Time     `json:"captured_at"`
	Stale                 bool          `json:"stale"`
	PlanType              string        `json:"plan_type,omitempty"`
	Platform              string        `json:"platform,omitempty"`
	Windows               []UsageWindow `json:"windows"`
	Credits               *UsageCredits `json:"credits,omitempty"`
	ResetCreditsAvailable int           `json:"reset_credits_available,omitempty"`
	Error                 string        `json:"error,omitempty"`
}

// UsageResetResult 消费限额重置积分的结果。
type UsageResetResult struct {
	// Code: reset | nothing_to_reset | no_credit | already_redeemed
	Code         string        `json:"code"`
	WindowsReset int           `json:"windows_reset"`
	Account      Account       `json:"-"`
	Usage        UsageSnapshot `json:"-"`
}

// RefreshUsage 主动向上游查询用量窗口，写入 account.extra.usage 并返回。
func (s *Service) RefreshUsage(ctx context.Context, id int) (UsageSnapshot, Account, error) {
	item, err := s.FindByID(ctx, id, LoadOptions{WithProxy: true})
	if err != nil {
		return UsageSnapshot{}, Account{}, err
	}
	proxyURL := proxyURLFromRef(item.Proxy)
	refreshedBeforeFetch := false
	if oauthCredentialsNeedRefresh(item, time.Now()) {
		if err := s.refreshOAuthCredentials(ctx, &item, proxyURL); err != nil {
			return UsageSnapshot{}, item, fmt.Errorf("刷新 OAuth 凭证失败: %w", err)
		}
		refreshedBeforeFetch = true
	}
	fetcher := s.usageFetcher
	if fetcher == nil {
		fetcher = fetchUsageByPlatform
	}
	snap, err := fetcher(ctx, item.Platform, item.Type, item.Credentials, proxyURL)
	if err != nil && !refreshedBeforeFetch && accountHasOAuthRefreshPath(item) && isRefreshableAccountAuthError(item.Platform, err) {
		if refreshErr := s.refreshOAuthCredentials(ctx, &item, proxyURL); refreshErr != nil {
			return UsageSnapshot{}, item, fmt.Errorf("%v；刷新 OAuth 凭证失败: %w", err, refreshErr)
		}
		snap, err = fetcher(ctx, item.Platform, item.Type, item.Credentials, proxyURL)
	}
	if err != nil {
		return UsageSnapshot{}, item, err
	}
	snap.Platform = item.Platform
	snap.Stale = false
	if snap.CapturedAt.IsZero() {
		snap.CapturedAt = time.Now().UTC()
	}

	// 落 extra.usage；同步 plan_type 到 credentials 便于列表常驻展示
	extra := cloneAnyMap(item.Extra)
	if extra == nil {
		extra = map[string]any{}
	}
	raw, _ := json.Marshal(snap)
	var asMap map[string]any
	_ = json.Unmarshal(raw, &asMap)
	extra[usageExtraKey] = asMap
	if plan := strings.TrimSpace(snap.PlanType); plan != "" {
		extra["plan_type"] = plan
	}

	persist := PersistUpdateInput{
		Extra:    extra,
		HasExtra: true,
	}
	// 有 plan_type 时写回凭证（脱敏列表仍不可见 token，plan_type 非敏感）
	if plan := strings.TrimSpace(snap.PlanType); plan != "" {
		creds := cloneStringMap(item.Credentials)
		if creds == nil {
			creds = map[string]string{}
		}
		creds["plan_type"] = plan
		enc, email, err := s.prepareCredentials(creds)
		if err == nil {
			persist.CredentialsEnc = &enc
			if email != "" {
				persist.Email = &email
			}
		}
	}

	updated, err := s.repo.Update(ctx, id, persist)
	if err != nil {
		return snap, item, err
	}
	if err := s.decryptAccount(&updated); err != nil {
		return snap, item, err
	}
	updated.Usage = &snap
	updated.PlanType = resolvePlanType(updated)
	updated.SubscriptionActiveUntil = resolveSubscriptionActiveUntil(updated)
	return snap, updated, nil
}

// refreshUsageAfterImport 在账号落库后尝试获取首次用量。
// 查询失败不回滚账号导入，后续仍可通过列表中的刷新按钮重试。
func (s *Service) refreshUsageAfterImport(ctx context.Context, item Account) Account {
	if !accountSupportsUsageRefresh(item) {
		return item
	}
	refreshCtx, cancel := context.WithTimeout(ctx, initialUsageRefreshTimeout)
	defer cancel()
	_, updated, err := s.RefreshUsage(refreshCtx, item.ID)
	if err != nil {
		logx.LoggerFromContext(ctx).Warn("account_initial_usage_refresh_failed",
			logx.LogFieldAccountID, item.ID,
			logx.LogFieldPlatform, item.Platform,
			logx.LogFieldError, err)
		return item
	}
	return updated
}

func accountSupportsUsageRefresh(item Account) bool {
	if NormalizeAccountType(item.Type) != TypeOAuth || strings.TrimSpace(item.Credentials["access_token"]) == "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(item.Platform)) {
	case "codex", "claude", "xai", "grok":
		return true
	default:
		return false
	}
}

// ConsumeUsageReset 消费一枚 Codex「限额重置积分」以重置用量窗口，成功后刷新快照。
// 对齐 airgate-openai / CPA：POST /wham/rate-limit-reset-credits/consume。
func (s *Service) ConsumeUsageReset(ctx context.Context, id int, creditID string) (UsageResetResult, error) {
	item, err := s.FindByID(ctx, id, LoadOptions{WithProxy: true})
	if err != nil {
		return UsageResetResult{}, err
	}
	if strings.ToLower(strings.TrimSpace(item.Platform)) != "codex" {
		return UsageResetResult{}, fmt.Errorf("%w: 仅 Codex OAuth 支持重置额度", ErrUsageNotSupported)
	}
	proxyURL := proxyURLFromRef(item.Proxy)
	result, err := consumeCodexResetCredit(ctx, item.Credentials, proxyURL, creditID)
	if err != nil {
		return UsageResetResult{}, err
	}
	out := UsageResetResult{Code: result.Code, WindowsReset: result.WindowsReset}

	// 重置成功后必须重拉用量，否则旧 used_percent 仍显示
	if result.Code == "reset" {
		snap, acc, ferr := s.RefreshUsage(ctx, id)
		if ferr == nil {
			out.Usage = snap
			out.Account = acc
			return out, nil
		}
		// 消费已成功，刷新失败仍返回结果 + 旧账号
	}
	item.Usage = UsageFromExtra(item.Extra)
	item.PlanType = resolvePlanType(item)
	item.SubscriptionActiveUntil = resolveSubscriptionActiveUntil(item)
	out.Account = item
	if item.Usage != nil {
		out.Usage = *item.Usage
	}
	return out, nil
}

func resolvePlanType(a Account) string {
	if a.Credentials != nil {
		if p := strings.TrimSpace(a.Credentials["plan_type"]); p != "" {
			return p
		}
	}
	if a.Extra != nil {
		if p, ok := a.Extra["plan_type"].(string); ok && strings.TrimSpace(p) != "" {
			return strings.TrimSpace(p)
		}
	}
	if a.Usage != nil && strings.TrimSpace(a.Usage.PlanType) != "" {
		return strings.TrimSpace(a.Usage.PlanType)
	}
	if u := UsageFromExtra(a.Extra); u != nil && strings.TrimSpace(u.PlanType) != "" {
		return strings.TrimSpace(u.PlanType)
	}
	return ""
}

// resolveSubscriptionActiveUntil 解析订阅到期时间（非 token expired）。
// 优先 credentials.subscription_active_until，其次 extra，再从 id_token claim 兜底。
func resolveSubscriptionActiveUntil(a Account) string {
	if a.Credentials != nil {
		if p := strings.TrimSpace(a.Credentials["subscription_active_until"]); p != "" {
			return p
		}
	}
	if a.Extra != nil {
		if p, ok := a.Extra["subscription_active_until"].(string); ok && strings.TrimSpace(p) != "" {
			return strings.TrimSpace(p)
		}
	}
	// 存量账号：登录时未落库，列表时从 id_token 现解析（不写库）
	if a.Credentials != nil {
		if idt := strings.TrimSpace(a.Credentials["id_token"]); idt != "" {
			tmp := map[string]string{}
			applyCodexIDTokenClaims(tmp, idt)
			if p := strings.TrimSpace(tmp["subscription_active_until"]); p != "" {
				return p
			}
		}
	}
	return ""
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// UsageFromExtra 从 account.extra 解析用量快照（列表用，不穿透上游）。
func UsageFromExtra(extra map[string]any) *UsageSnapshot {
	if extra == nil {
		return nil
	}
	raw, ok := extra[usageExtraKey]
	if !ok || raw == nil {
		return nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var snap UsageSnapshot
	if err := json.Unmarshal(b, &snap); err != nil {
		return nil
	}
	if len(snap.Windows) == 0 && snap.Credits == nil && snap.ResetCreditsAvailable <= 0 && snap.Error == "" {
		return nil
	}
	if !snap.CapturedAt.IsZero() {
		snap.Stale = time.Since(snap.CapturedAt) > usageStaleAfter
	}
	return &snap
}

func fetchUsageByPlatform(ctx context.Context, platform, accountType string, creds map[string]string, proxyURL string) (UsageSnapshot, error) {
	p := strings.ToLower(strings.TrimSpace(platform))
	switch p {
	case "codex":
		return fetchCodexUsage(ctx, creds, proxyURL)
	case "claude":
		return fetchClaudeUsage(ctx, creds, proxyURL)
	case "xai", "grok":
		return fetchXAIUsage(ctx, creds, proxyURL)
	default:
		return UsageSnapshot{}, fmt.Errorf("%w: platform=%s", ErrUsageNotSupported, p)
	}
}

// ---------- Codex /wham/usage ----------

func fetchCodexUsage(ctx context.Context, creds map[string]string, proxyURL string) (UsageSnapshot, error) {
	accessToken := strings.TrimSpace(creds["access_token"])
	if accessToken == "" {
		// 仅 API Key 的 Codex 无法查 ChatGPT 订阅窗口
		return UsageSnapshot{}, fmt.Errorf("%w: codex 需要 OAuth access_token", ErrUsageNotSupported)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, codexUsageURL, nil)
	if err != nil {
		return UsageSnapshot{}, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("originator", codexOriginator)
	req.Header.Set("User-Agent", codexOriginator)
	if accountID := strings.TrimSpace(creds["chatgpt_account_id"]); accountID != "" {
		req.Header.Set("ChatGPT-Account-Id", accountID)
	}

	resp, err := httpClient(proxyURL).Do(req)
	if err != nil {
		return UsageSnapshot{}, fmt.Errorf("查询 Codex 用量失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return UsageSnapshot{}, &accountUpstreamHTTPError{status: resp.StatusCode, body: body, label: "codex 用量"}
	}

	var payload struct {
		PlanType  string `json:"plan_type"`
		RateLimit *struct {
			PrimaryWindow   *codexUsageWindowPayload `json:"primary_window"`
			SecondaryWindow *codexUsageWindowPayload `json:"secondary_window"`
		} `json:"rate_limit"`
		Credits *struct {
			HasCredits bool            `json:"has_credits"`
			Unlimited  bool            `json:"unlimited"`
			Balance    json.RawMessage `json:"balance"`
		} `json:"credits"`
		AdditionalRateLimits []struct {
			LimitName      string `json:"limit_name"`
			MeteredFeature string `json:"metered_feature"`
			RateLimit      *struct {
				PrimaryWindow   *codexUsageWindowPayload `json:"primary_window"`
				SecondaryWindow *codexUsageWindowPayload `json:"secondary_window"`
			} `json:"rate_limit"`
		} `json:"additional_rate_limits"`
		RateLimitResetCredits *struct {
			AvailableCount int `json:"available_count"`
		} `json:"rate_limit_reset_credits"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return UsageSnapshot{}, fmt.Errorf("解析 Codex 用量失败: %w", err)
	}

	now := time.Now().UTC()
	snap := UsageSnapshot{CapturedAt: now, PlanType: payload.PlanType, Windows: []UsageWindow{}}
	if payload.RateLimit != nil {
		if w := payload.RateLimit.PrimaryWindow.toWindow(now, 300); w != nil {
			snap.Windows = append(snap.Windows, *w)
		}
		if w := payload.RateLimit.SecondaryWindow.toWindow(now, 10080); w != nil {
			snap.Windows = append(snap.Windows, *w)
		}
	}
	for _, extra := range payload.AdditionalRateLimits {
		if extra.RateLimit == nil {
			continue
		}
		// 家族 ID：metered_feature 优先（如 codex_bengalfox = Spark），否则 limit_name
		limitID := normalizeCodexLimitID(firstNonEmpty(
			strings.TrimSpace(extra.MeteredFeature),
			strings.TrimSpace(extra.LimitName),
		))
		if limitID == "" || limitID == "codex" {
			// 主限额已在 rate_limit 中处理
			continue
		}
		limitName := strings.TrimSpace(extra.LimitName)
		// Spark 家族给更友好的展示名
		if strings.Contains(strings.ToLower(limitID), "bengalfox") ||
			strings.Contains(strings.ToLower(limitName), "spark") {
			if limitName == "" {
				limitName = "Spark"
			}
		}
		if w := extra.RateLimit.PrimaryWindow.toWindow(now, 300); w != nil {
			w.LimitID = limitID
			w.LimitName = limitName
			if strings.Contains(strings.ToLower(limitID+limitName), "spark") ||
				strings.Contains(strings.ToLower(limitID), "bengalfox") {
				w.Label = "Spark " + w.Label
			}
			snap.Windows = append(snap.Windows, *w)
		}
		if w := extra.RateLimit.SecondaryWindow.toWindow(now, 10080); w != nil {
			w.LimitID = limitID
			w.LimitName = limitName
			if strings.Contains(strings.ToLower(limitID+limitName), "spark") ||
				strings.Contains(strings.ToLower(limitID), "bengalfox") {
				w.Label = "Spark " + w.Label
			}
			snap.Windows = append(snap.Windows, *w)
		}
	}
	if payload.Credits != nil {
		bal := strings.Trim(string(payload.Credits.Balance), `"`)
		if payload.Credits.HasCredits || payload.Credits.Unlimited || bal != "" && bal != "null" {
			snap.Credits = &UsageCredits{
				HasCredits: payload.Credits.HasCredits,
				Unlimited:  payload.Credits.Unlimited,
				Balance:    bal,
			}
		}
	}
	if payload.RateLimitResetCredits != nil {
		snap.ResetCreditsAvailable = payload.RateLimitResetCredits.AvailableCount
	} else {
		// usage 偶发不带 rate_limit_reset_credits：单独拉明细补齐次数
		if n, err := fetchCodexResetCreditsAvailable(ctx, creds, proxyURL); err == nil {
			snap.ResetCreditsAvailable = n
		}
	}
	if len(snap.Windows) == 0 && snap.Credits == nil && snap.ResetCreditsAvailable <= 0 {
		return UsageSnapshot{}, fmt.Errorf("上游未返回用量窗口数据")
	}
	return snap, nil
}

// fetchCodexResetCreditsAvailable GET /wham/rate-limit-reset-credits → available_count。
func fetchCodexResetCreditsAvailable(ctx context.Context, creds map[string]string, proxyURL string) (int, error) {
	accessToken := strings.TrimSpace(creds["access_token"])
	if accessToken == "" {
		return 0, fmt.Errorf("%w: codex 需要 OAuth access_token", ErrUsageNotSupported)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, codexResetCreditsURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("originator", codexOriginator)
	req.Header.Set("User-Agent", codexOriginator)
	if accountID := strings.TrimSpace(creds["chatgpt_account_id"]); accountID != "" {
		req.Header.Set("ChatGPT-Account-Id", accountID)
	}
	resp, err := httpClient(proxyURL).Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("reset-credits HTTP %d: %s", resp.StatusCode, truncate(string(body), 160))
	}
	var details struct {
		AvailableCount int `json:"available_count"`
	}
	if err := json.Unmarshal(body, &details); err != nil {
		return 0, err
	}
	return details.AvailableCount, nil
}

type codexUsageWindowPayload struct {
	UsedPercent        float64 `json:"used_percent"`
	LimitWindowSeconds int     `json:"limit_window_seconds"`
	// 兼容部分上游字段名
	WindowMinutes     int   `json:"window_minutes"`
	ResetAfterSeconds int   `json:"reset_after_seconds"`
	ResetAt           int64 `json:"reset_at"`
}

func (p *codexUsageWindowPayload) toWindow(now time.Time, fallbackMinutes int) *UsageWindow {
	if p == nil {
		return nil
	}
	// 有任一用量/重置/窗口时长字段即认为有效（0% 已用也应展示，如 Spark 刚重置）
	mins := p.LimitWindowSeconds / 60
	if mins <= 0 {
		mins = p.WindowMinutes
	}
	if p.UsedPercent <= 0 && p.ResetAfterSeconds <= 0 && mins <= 0 && p.ResetAt <= 0 {
		return nil
	}
	if mins <= 0 {
		mins = fallbackMinutes
	}
	key := windowKeyFromMinutes(mins)
	w := &UsageWindow{
		Key:           key,
		Label:         windowLabel(key),
		UsedPercent:   p.UsedPercent,
		WindowMinutes: mins,
	}
	if p.ResetAfterSeconds > 0 {
		t := now.Add(time.Duration(p.ResetAfterSeconds) * time.Second)
		w.ResetsAt = &t
	} else if p.ResetAt > 0 {
		// 上游 reset_at 可能是 unix 秒或毫秒
		sec := p.ResetAt
		if sec > 1e12 {
			sec = sec / 1000
		}
		t := time.Unix(sec, 0).UTC()
		w.ResetsAt = &t
	}
	return w
}

// ---------- Codex 重置额度 ----------

type codexResetConsumeResult struct {
	Code         string `json:"code"`
	WindowsReset int    `json:"windows_reset"`
}

func consumeCodexResetCredit(ctx context.Context, creds map[string]string, proxyURL, creditID string) (*codexResetConsumeResult, error) {
	accessToken := strings.TrimSpace(creds["access_token"])
	if accessToken == "" {
		return nil, fmt.Errorf("%w: codex 需要 OAuth access_token", ErrUsageNotSupported)
	}
	bodyMap := map[string]string{
		"redeem_request_id": newCodexRedeemRequestID(),
	}
	if id := strings.TrimSpace(creditID); id != "" {
		bodyMap["credit_id"] = id
	}
	raw, _ := json.Marshal(bodyMap)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, codexResetCreditsConsumeURL, strings.NewReader(string(raw)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("originator", codexOriginator)
	req.Header.Set("User-Agent", codexOriginator)
	if accountID := strings.TrimSpace(creds["chatgpt_account_id"]); accountID != "" {
		req.Header.Set("ChatGPT-Account-Id", accountID)
	}

	resp, err := httpClient(proxyURL).Do(req)
	if err != nil {
		return nil, fmt.Errorf("重置额度请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("重置额度 HTTP %d: %s", resp.StatusCode, truncate(string(data), 200))
	}
	var result codexResetConsumeResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("解析重置结果失败: %w", err)
	}
	if result.Code == "" {
		result.Code = "reset"
	}
	return &result, nil
}

func newCodexRedeemRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// ---------- Claude /api/oauth/usage ----------

func fetchClaudeUsage(ctx context.Context, creds map[string]string, proxyURL string) (UsageSnapshot, error) {
	accessToken := strings.TrimSpace(creds["access_token"])
	if accessToken == "" {
		return UsageSnapshot{}, fmt.Errorf("%w: claude 需要 OAuth access_token", ErrUsageNotSupported)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, claudeUsageURL, nil)
	if err != nil {
		return UsageSnapshot{}, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("anthropic-beta", claudeBetaOAuth)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	// Claude Code 伪装（最小集）
	req.Header.Set("User-Agent", "claude-cli/2.1.112 (external, cli)")
	req.Header.Set("X-App", "cli")
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := httpClient(proxyURL).Do(req)
	if err != nil {
		return UsageSnapshot{}, fmt.Errorf("查询 Claude 用量失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return UsageSnapshot{}, &accountUpstreamHTTPError{status: resp.StatusCode, body: body, label: "claude 用量"}
	}

	var payload struct {
		FiveHour struct {
			Utilization float64 `json:"utilization"`
			ResetsAt    string  `json:"resets_at"`
		} `json:"five_hour"`
		SevenDay struct {
			Utilization float64 `json:"utilization"`
			ResetsAt    string  `json:"resets_at"`
		} `json:"seven_day"`
		SevenDaySonnet struct {
			Utilization float64 `json:"utilization"`
			ResetsAt    string  `json:"resets_at"`
		} `json:"seven_day_sonnet"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return UsageSnapshot{}, fmt.Errorf("解析 Claude 用量失败: %w", err)
	}

	now := time.Now().UTC()
	snap := UsageSnapshot{CapturedAt: now, Windows: []UsageWindow{}}
	appendClaude := func(key, label string, util float64, resetsAt string, mins int) {
		// utilization 可能是 0-1 或 0-100
		pct := util
		if pct > 0 && pct <= 1.0 {
			pct = pct * 100
		}
		if pct <= 0 && resetsAt == "" {
			return
		}
		w := UsageWindow{
			Key:           key,
			Label:         label,
			UsedPercent:   pct,
			WindowMinutes: mins,
		}
		if resetsAt != "" {
			if t, err := time.Parse(time.RFC3339, resetsAt); err == nil {
				tt := t.UTC()
				w.ResetsAt = &tt
			}
		}
		snap.Windows = append(snap.Windows, w)
	}
	appendClaude("5h", "5 小时", payload.FiveHour.Utilization, payload.FiveHour.ResetsAt, 300)
	appendClaude("weekly", "每周", payload.SevenDay.Utilization, payload.SevenDay.ResetsAt, 10080)
	if payload.SevenDaySonnet.ResetsAt != "" || payload.SevenDaySonnet.Utilization > 0 {
		appendClaude("model:7d:sonnet", "7d Sonnet", payload.SevenDaySonnet.Utilization, payload.SevenDaySonnet.ResetsAt, 10080)
	}
	if len(snap.Windows) == 0 {
		return UsageSnapshot{}, fmt.Errorf("上游未返回用量窗口数据")
	}
	return snap, nil
}

// ---------- xAI / Grok ----------
//
// 对齐 CPA Manager 的 XAI 配额条：
//  1) GET /v1/billing?format=credits → 周额度（credit_usage_percent / product_usage）
//  2) GET /v1/billing               → 月额度 / 按量（monthly_limit / used / on_demand_*）
// 请求头与 grok-cli 一致：x-xai-token-auth + x-grok-client-version + User-Agent。
// free-usage-exhausted 只在 billing 返回 402/429 时作为兜底窗口。

func fetchXAIUsage(ctx context.Context, creds map[string]string, proxyURL string) (UsageSnapshot, error) {
	accessToken := strings.TrimSpace(creds["access_token"])
	if accessToken == "" {
		return UsageSnapshot{}, fmt.Errorf("%w: xai/grok 需要 OAuth access_token", ErrUsageNotSupported)
	}

	now := time.Now().UTC()
	snap := UsageSnapshot{CapturedAt: now, Windows: []UsageWindow{}}

	// 已有凭证 / id_token 的 plan 先占位；billing 的月额度会覆盖
	if p := strings.TrimSpace(creds["plan_type"]); p != "" {
		snap.PlanType = p
	}
	userID := strings.TrimSpace(creds["subject"])
	if userID == "" {
		userID = strings.TrimSpace(creds["user_id"])
	}
	if idt := strings.TrimSpace(creds["id_token"]); idt != "" {
		claims := parseJWTPayload(idt)
		if plan := planTypeFromClaims(claims); plan != "" {
			snap.PlanType = plan
		}
		if userID == "" {
			userID = firstNonEmpty(
				claimToString(claims["sub"]),
				claimToString(claims["user_id"]),
				claimToString(claims["userId"]),
			)
		}
	}

	weekly, weeklyErr := xaiRequestBilling(ctx, accessToken, proxyURL, userID, xaiBillingWeeklyURL)
	monthly, monthlyErr := xaiRequestBilling(ctx, accessToken, proxyURL, userID, xaiBillingMonthlyURL)
	return resolveXAIBillingUsage(snap, now, weekly, monthly, weeklyErr, monthlyErr)
}

// resolveXAIBillingUsage 汇总两个 billing 响应。只有拿到可展示窗口才算刷新成功，
// 避免上游暂时失败时用仅含订阅档位的空快照覆盖数据库中的旧用量。
func resolveXAIBillingUsage(
	snap UsageSnapshot,
	now time.Time,
	weekly, monthly *xaiBillingSummary,
	weeklyErr, monthlyErr error,
) (UsageSnapshot, error) {
	// 优先合并两边的 config；任一侧成功即可出条
	var summary *xaiBillingSummary
	if weekly != nil {
		summary = weekly
	}
	if monthly != nil {
		summary = mergeXAIBillingSummary(summary, monthly)
	}
	if summary != nil {
		snap.Windows = append(snap.Windows, summary.toWindows()...)
		if plan := planTypeFromXAIMonthlyLimit(summary.MonthlyLimitCents); plan != "" {
			snap.PlanType = plan
		}
		if len(snap.Windows) > 0 {
			return snap, nil
		}
	}

	// billing 全失败：尝试从错误体解析 free-usage 窗口
	for _, errText := range []error{weeklyErr, monthlyErr} {
		if errText == nil {
			continue
		}
		if w, plan, ok := parseXAIFreeUsageExhausted(errText.Error(), now); ok {
			snap.Windows = append(snap.Windows, w)
			if plan != "" {
				snap.PlanType = plan
			}
			return snap, nil
		}
	}

	// 订阅档位不能代替用量窗口；失败时返回错误，由调用方保留旧快照。
	if weeklyErr != nil {
		return UsageSnapshot{}, weeklyErr
	}
	if monthlyErr != nil {
		return UsageSnapshot{}, monthlyErr
	}
	return UsageSnapshot{}, fmt.Errorf("xAI billing 未返回可用用量数据")
}

func xaiRequestBilling(ctx context.Context, accessToken, proxyURL, userID, targetURL string) (*xaiBillingSummary, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("x-xai-token-auth", "xai-grok-cli")
	req.Header.Set("x-grok-client-version", xaiGrokClientVersion)
	req.Header.Set("User-Agent", xaiGrokUserAgent)
	req.Header.Set("Accept", "*/*")
	if userID != "" {
		req.Header.Set("x-userid", userID)
	}

	resp, err := httpClient(proxyURL).Do(req)
	if err != nil {
		return nil, fmt.Errorf("xAI billing 请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &accountUpstreamHTTPError{status: resp.StatusCode, body: body, label: "xAI billing"}
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("解析 xAI billing 失败: %w", err)
	}
	config := asAnyMap(payload["config"])
	if config == nil {
		// 少数响应可能直接把字段放在根
		config = payload
	}
	summary := parseXAIBillingConfig(config)
	if summary == nil {
		return nil, fmt.Errorf("xAI billing 响应无可用 config")
	}
	return summary, nil
}

// xaiBillingSummary 对齐 CPA buildXaiBillingSummary 的关键字段。
type xaiBillingSummary struct {
	HasWeeklyData       bool
	HasMonthlyData      bool
	UsagePercent        *float64 // weekly credit_usage_percent
	UsedPercent         *float64 // monthly included used%
	OnDemandUsedPercent *float64
	MonthlyLimitCents   *float64
	PeriodEnd           string
	BillingPeriodEnd    string
	ProductUsage        []xaiProductUsage
}

type xaiProductUsage struct {
	Product      string
	UsagePercent *float64
}

func parseXAIBillingConfig(config map[string]any) *xaiBillingSummary {
	if config == nil {
		return nil
	}
	usage := readNullableFloat(config, "credit_usage_percent", "creditUsagePercent")
	monthlyLimit := readXAICentFloat(config, "monthly_limit", "monthlyLimit")
	used := readXAICentFloat(config, "used")
	onDemandCap := readXAICentFloat(config, "on_demand_cap", "onDemandCap")
	onDemandUsed := readXAICentFloat(config, "on_demand_used", "onDemandUsed")

	includedUsed := used
	if includedUsed != nil && monthlyLimit != nil && *monthlyLimit > 0 {
		v := *includedUsed
		if v > *monthlyLimit {
			v = *monthlyLimit
		}
		includedUsed = &v
	}
	if onDemandUsed == nil && used != nil && monthlyLimit != nil {
		v := *used - *monthlyLimit
		if v < 0 {
			v = 0
		}
		onDemandUsed = &v
	}
	var monthlyUsed *float64
	if monthlyLimit != nil && *monthlyLimit > 0 && includedUsed != nil {
		v := (*includedUsed / *monthlyLimit) * 100
		monthlyUsed = &v
	}
	var onDemandUsedPercent *float64
	if onDemandCap != nil && *onDemandCap > 0 && onDemandUsed != nil {
		v := (*onDemandUsed / *onDemandCap) * 100
		onDemandUsedPercent = &v
	}

	period := asAnyMap(firstNonNil(config["current_period"], config["currentPeriod"]))
	periodType := strings.ToLower(readStringAny(period, "type"))
	periodEnd := readStringAny(period, "end")
	billingPeriodEnd := firstNonEmpty(
		readStringAny(config, "billing_period_end"),
		readStringAny(config, "billingPeriodEnd"),
	)

	productUsage := make([]xaiProductUsage, 0)
	if items := readAnySlice(config, "product_usage", "productUsage"); len(items) > 0 {
		for i, raw := range items {
			item := asAnyMap(raw)
			if item == nil {
				continue
			}
			product := firstNonEmpty(readStringAny(item, "product"), fmt.Sprintf("Product %d", i+1))
			productUsage = append(productUsage, xaiProductUsage{
				Product:      product,
				UsagePercent: readNullableFloat(item, "usage_percent", "usagePercent"),
			})
		}
	}

	hasWeeklyData := usage != nil || len(productUsage) > 0 || strings.Contains(periodType, "weekly")
	hasMonthlyData := monthlyLimit != nil || used != nil || (!hasWeeklyData && (onDemandCap != nil || billingPeriodEnd != ""))
	if !hasWeeklyData && !hasMonthlyData {
		return nil
	}
	return &xaiBillingSummary{
		HasWeeklyData:       hasWeeklyData,
		HasMonthlyData:      hasMonthlyData,
		UsagePercent:        usage,
		UsedPercent:         monthlyUsed,
		OnDemandUsedPercent: onDemandUsedPercent,
		MonthlyLimitCents:   monthlyLimit,
		PeriodEnd:           periodEnd,
		BillingPeriodEnd:    billingPeriodEnd,
		ProductUsage:        productUsage,
	}
}

func mergeXAIBillingSummary(primary, fallback *xaiBillingSummary) *xaiBillingSummary {
	if primary == nil {
		return fallback
	}
	if fallback == nil {
		return primary
	}
	merged := *primary
	if merged.UsagePercent == nil {
		merged.UsagePercent = fallback.UsagePercent
	}
	if merged.UsedPercent == nil {
		merged.UsedPercent = fallback.UsedPercent
	}
	if merged.OnDemandUsedPercent == nil {
		merged.OnDemandUsedPercent = fallback.OnDemandUsedPercent
	}
	if merged.MonthlyLimitCents == nil {
		merged.MonthlyLimitCents = fallback.MonthlyLimitCents
	}
	merged.HasWeeklyData = merged.HasWeeklyData || fallback.HasWeeklyData
	merged.HasMonthlyData = merged.HasMonthlyData || fallback.HasMonthlyData
	if merged.PeriodEnd == "" {
		merged.PeriodEnd = fallback.PeriodEnd
	}
	if merged.BillingPeriodEnd == "" {
		merged.BillingPeriodEnd = fallback.BillingPeriodEnd
	}
	if len(merged.ProductUsage) == 0 {
		merged.ProductUsage = fallback.ProductUsage
	}
	return &merged
}

func (s *xaiBillingSummary) toWindows() []UsageWindow {
	if s == nil {
		return nil
	}
	out := make([]UsageWindow, 0, 4+len(s.ProductUsage))
	if s.HasWeeklyData && s.UsagePercent != nil {
		w := UsageWindow{
			Key:           "weekly",
			Label:         "周额度",
			UsedPercent:   clampPercent(*s.UsagePercent),
			WindowMinutes: 7 * 24 * 60,
		}
		if t := parseRFC3339Any(s.PeriodEnd); t != nil {
			w.ResetsAt = t
		}
		out = append(out, w)
	}
	for _, item := range s.ProductUsage {
		if item.UsagePercent == nil {
			continue
		}
		w := UsageWindow{
			Key:         "product",
			Label:       item.Product,
			UsedPercent: clampPercent(*item.UsagePercent),
			LimitName:   item.Product,
			LimitID:     "xai-product",
		}
		if t := parseRFC3339Any(s.PeriodEnd); t != nil {
			w.ResetsAt = t
		}
		out = append(out, w)
	}
	if s.UsedPercent != nil || s.MonthlyLimitCents != nil {
		pct := 0.0
		if s.UsedPercent != nil {
			pct = *s.UsedPercent
		}
		w := UsageWindow{
			Key:           "monthly",
			Label:         "月额度",
			UsedPercent:   clampPercent(pct),
			WindowMinutes: 30 * 24 * 60,
		}
		if t := parseRFC3339Any(s.BillingPeriodEnd); t != nil {
			w.ResetsAt = t
		}
		out = append(out, w)
	}
	if s.OnDemandUsedPercent != nil {
		w := UsageWindow{
			Key:         "on_demand",
			Label:       "按量",
			UsedPercent: clampPercent(*s.OnDemandUsedPercent),
			LimitID:     "xai-on-demand",
			LimitName:   "on_demand",
		}
		if t := parseRFC3339Any(s.BillingPeriodEnd); t != nil {
			w.ResetsAt = t
		}
		out = append(out, w)
	}
	return out
}

func planTypeFromXAIMonthlyLimit(cents *float64) string {
	if cents == nil {
		return ""
	}
	switch *cents {
	case xaiSuperGrokLimitCents:
		return "super"
	case xaiSuperGrokHeavyLimitCents:
		return "super_heavy"
	default:
		return ""
	}
}

// planTypeFromClaims 从 userinfo / JWT / 杂项 JSON 中抽取订阅档位。
func planTypeFromClaims(claims map[string]any) string {
	if claims == nil {
		return ""
	}
	keys := []string{
		"plan_type", "plan", "tier", "subscription", "subscription_type",
		"subscription_plan", "product", "sku", "entitlement",
	}
	for _, k := range keys {
		if s := claimToString(claims[k]); s != "" {
			return normalizeXAIPlanType(s)
		}
	}
	if sub, ok := claims["subscription"].(map[string]any); ok {
		for _, k := range []string{"plan", "plan_type", "tier", "type", "name"} {
			if s := claimToString(sub[k]); s != "" {
				return normalizeXAIPlanType(s)
			}
		}
	}
	return ""
}

func normalizeXAIPlanType(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "_", "")
	s = strings.ReplaceAll(s, "-", "")
	switch {
	case s == "" || s == "null" || s == "none":
		return ""
	case strings.Contains(s, "super") && strings.Contains(s, "heavy"):
		return "super_heavy"
	case strings.Contains(s, "super"):
		return "super"
	case strings.Contains(s, "premium"):
		return "premium"
	case strings.Contains(s, "pro"):
		return "pro"
	case strings.Contains(s, "plus"):
		return "plus"
	case strings.Contains(s, "team") || strings.Contains(s, "business"):
		return "team"
	case strings.Contains(s, "enterprise"):
		return "enterprise"
	case strings.Contains(s, "free"):
		return "free"
	default:
		return strings.TrimSpace(raw)
	}
}

// parseXAIFreeUsageExhausted 解析 CPA 同款 free-usage-exhausted 文案：
// "... tokens (actual/limit): 1065387/1000000"
func parseXAIFreeUsageExhausted(text string, now time.Time) (UsageWindow, string, bool) {
	low := strings.ToLower(text)
	if !strings.Contains(low, "free-usage") && !strings.Contains(low, "included free usage") && !strings.Contains(low, "free usage") {
		return UsageWindow{}, "", false
	}
	actual, limit := 0.0, 0.0
	marker := "tokens (actual/limit):"
	if i := strings.Index(low, marker); i >= 0 {
		rest := strings.TrimSpace(text[i+len(marker):])
		var a, b float64
		if _, err := fmt.Sscanf(rest, "%f/%f", &a, &b); err == nil && b > 0 {
			actual, limit = a, b
		}
	}
	if limit <= 0 {
		for _, part := range strings.Fields(strings.ReplaceAll(text, "—", " ")) {
			part = strings.Trim(part, ".,;")
			if !strings.Contains(part, "/") {
				continue
			}
			var a, b float64
			if _, err := fmt.Sscanf(part, "%f/%f", &a, &b); err == nil && b > 0 && a >= 0 {
				actual, limit = a, b
				break
			}
		}
	}
	pct := 100.0
	if limit > 0 {
		pct = actual / limit * 100
		if pct > 100 {
			pct = 100
		}
	}
	resetAt := now.Add(24 * time.Hour)
	w := UsageWindow{
		Key:           "24h",
		Label:         "24 小时",
		UsedPercent:   pct,
		WindowMinutes: 24 * 60,
		ResetsAt:      &resetAt,
	}
	return w, "free", true
}

func clampPercent(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func asAnyMap(v any) map[string]any {
	if v == nil {
		return nil
	}
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

func firstNonNil(values ...any) any {
	for _, v := range values {
		if v != nil {
			return v
		}
	}
	return nil
}

func readStringAny(m map[string]any, keys ...string) string {
	if m == nil {
		return ""
	}
	for _, k := range keys {
		if s := claimToString(m[k]); s != "" {
			return s
		}
	}
	return ""
}

func readAnySlice(m map[string]any, keys ...string) []any {
	if m == nil {
		return nil
	}
	for _, k := range keys {
		switch v := m[k].(type) {
		case []any:
			return v
		}
	}
	return nil
}

func readNullableFloat(m map[string]any, keys ...string) *float64 {
	if m == nil {
		return nil
	}
	for _, k := range keys {
		if v, ok := toFloat64(m[k]); ok {
			return &v
		}
	}
	return nil
}

// readXAICentFloat 支持裸数字或 { "val": N } 形态（CPA normalizeXaiCentValue）。
func readXAICentFloat(m map[string]any, keys ...string) *float64 {
	if m == nil {
		return nil
	}
	for _, k := range keys {
		raw := m[k]
		if raw == nil {
			continue
		}
		if nested := asAnyMap(raw); nested != nil {
			if v, ok := toFloat64(nested["val"]); ok {
				return &v
			}
			continue
		}
		if v, ok := toFloat64(raw); ok {
			return &v
		}
	}
	return nil
}

func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case string:
		s := strings.TrimSpace(n)
		if s == "" {
			return 0, false
		}
		var f float64
		if _, err := fmt.Sscanf(s, "%f", &f); err == nil {
			return f, true
		}
	}
	return 0, false
}

func parseRFC3339Any(s string) *time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		u := t.UTC()
		return &u
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		u := t.UTC()
		return &u
	}
	// 纯日期
	if t, err := time.Parse("2006-01-02", s); err == nil {
		u := t.UTC()
		return &u
	}
	return nil
}

// ---------- helpers ----------

func normalizeCodexLimitID(id string) string {
	id = strings.TrimSpace(strings.ToLower(id))
	if id == "" || id == "codex" || id == "default" {
		return ""
	}
	return id
}

func windowKeyFromMinutes(minutes int) string {
	approx := func(target int) bool {
		if minutes <= 0 {
			return false
		}
		diff := minutes - target
		if diff < 0 {
			diff = -diff
		}
		return float64(diff) <= float64(target)*0.05
	}
	switch {
	case approx(300):
		return "5h"
	case approx(1440):
		return "daily"
	case approx(10080):
		return "weekly"
	case approx(43200):
		return "monthly"
	case approx(525600):
		return "annual"
	default:
		return "window"
	}
}

func windowLabel(key string) string {
	switch key {
	case "5h":
		return "5 小时"
	case "daily":
		return "每日"
	case "weekly":
		return "每周"
	case "monthly":
		return "每月"
	case "annual":
		return "每年"
	default:
		return "用量"
	}
}

func proxyURLFromRef(p *ProxyRef) string {
	if p == nil || p.Address == "" || p.Port <= 0 {
		return ""
	}
	scheme := strings.ToLower(strings.TrimSpace(p.Protocol))
	if scheme == "" {
		scheme = "http"
	}
	host := fmt.Sprintf("%s:%d", p.Address, p.Port)
	user := strings.TrimSpace(p.Username)
	pass := strings.TrimSpace(p.Password)
	if user != "" {
		if pass != "" {
			return fmt.Sprintf("%s://%s:%s@%s", scheme, user, pass, host)
		}
		return fmt.Sprintf("%s://%s@%s", scheme, user, host)
	}
	return fmt.Sprintf("%s://%s", scheme, host)
}

func cloneAnyMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
