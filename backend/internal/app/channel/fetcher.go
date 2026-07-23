package channel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/DouDOU-start/airgate-core/internal/pkg/upstreamclient"
)

// DefaultModelFetcher 默认上游模型列表拉取器。
//
// 各渠道类型端点：
//   - openai_compatible：GET {base_url}/v1/models，Bearer 认证，解析 data[].id
//   - anthropic：GET {base_url}/v1/models，x-api-key 认证，解析 data[].id
//   - gemini：GET {base_url}/v1beta/models，x-goog-api-key 认证，解析 models[].name（去掉 "models/" 前缀）
type DefaultModelFetcher struct {
	// Client 可注入的 HTTP 客户端（测试用）；nil 时构建
	// 15s 超时、不跟随重定向的出口客户端（upstreamclient）。
	Client *http.Client
}

// FetchModels 拉取上游模型列表；拿不到（网络错误/非 2xx/解析失败/空列表）即报错。
func (f DefaultModelFetcher) FetchModels(ctx context.Context, channelType, baseURL, apiKey string) ([]string, error) {
	client := f.Client
	if client == nil {
		client = upstreamclient.NewClient(15 * time.Second)
	}
	base := strings.TrimRight(baseURL, "/")

	switch channelType {
	case "gemini":
		// base_url 已含版本段时不重复拼接。
		endpoint := base + "/v1beta/models"
		if strings.HasSuffix(base, "/v1beta") || strings.HasSuffix(base, "/v1") {
			endpoint = base + "/models"
		}
		body, err := fetchJSON(ctx, client, endpoint, map[string]string{"x-goog-api-key": apiKey})
		if err != nil {
			return nil, err
		}
		var resp struct {
			Models []struct {
				Name string `json:"name"`
			} `json:"models"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("解析模型列表响应失败: %w", err)
		}
		models := make([]string, 0, len(resp.Models))
		for _, m := range resp.Models {
			if m.Name == "" {
				continue
			}
			models = append(models, strings.TrimPrefix(m.Name, "models/"))
		}
		return ensureNonEmpty(models)
	case "anthropic":
		return fetchOpenAIStyleModels(ctx, client, base, map[string]string{
			"x-api-key":         apiKey,
			"anthropic-version": "2023-06-01",
		})
	default: // openai_compatible：按 OpenAI 兼容端点拉取
		return fetchOpenAIStyleModels(ctx, client, base, map[string]string{
			"Authorization": "Bearer " + apiKey,
		})
	}
}

// fetchOpenAIStyleModels 拉取 OpenAI 形态的模型列表端点（{base}/v1/models → data[].id）。
func fetchOpenAIStyleModels(ctx context.Context, client *http.Client, base string, headers map[string]string) ([]string, error) {
	endpoint := base + "/v1/models"
	if strings.HasSuffix(base, "/v1") {
		endpoint = base + "/models"
	}
	body, err := fetchJSON(ctx, client, endpoint, headers)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("解析模型列表响应失败: %w", err)
	}
	models := make([]string, 0, len(resp.Data))
	for _, m := range resp.Data {
		if m.ID != "" {
			models = append(models, m.ID)
		}
	}
	return ensureNonEmpty(models)
}

// FetchBalance 经 key 查询上游账户余额（USD）。
// 渠道 type 表示转发协议，不代表上游是否提供账户余额接口；例如 Anthropic 协议
// 的中转站也可能实现统一的 /v1/usage。因此这里对所有类型按同一兼容链路探测，
// 是否参与自动刷新由 key.balance_check_enabled 决定。
//
// 中转站的余额接口无统一标准，按主流→兼容顺序尝试：
//  1. GET {base}/v1/usage → 顶层 remaining（USD）。sub2api 等新型平台的口径，
//     钱包/额度/订阅三种模式都返回 remaining。
//  2. 回退 GET {base}/v1/dashboard/billing/subscription（+ /usage）→
//     hard_limit_usd − total_usage/100。new-api / one-api 等 OpenAI 早期计费口径。
//
// 前者未返回有效余额时继续尝试后者。部分中转站虽然注册了 /v1/usage，
// 但会对普通 API Key 返回 401/403/405，而旧计费接口仍可正常查询余额。
func (f DefaultModelFetcher) FetchBalance(ctx context.Context, _ string, baseURL, apiKey string) (float64, error) {
	client := f.Client
	if client == nil {
		client = upstreamclient.NewClient(15 * time.Second)
	}
	base := strings.TrimRight(baseURL, "/")
	auth := map[string]string{"Authorization": "Bearer " + apiKey}

	// ① /v1/usage：顶层 remaining（sub2api 等）。
	body, status, usageErr := httpGet(ctx, client, joinV1(base, "usage"), auth)
	if usageErr == nil && status >= 200 && status < 300 {
		var u struct {
			Remaining *float64 `json:"remaining"`
			Balance   *float64 `json:"balance"`
		}
		if json.Unmarshal(body, &u) == nil {
			if u.Remaining != nil {
				return *u.Remaining, nil
			}
			if u.Balance != nil {
				return *u.Balance, nil
			}
		}
		// 200 但无 remaining/balance 字段：该平台 /v1/usage 不含余额，转试计费接口。
	}

	// ② 回退 /dashboard/billing/subscription（+ /usage）：OpenAI 早期计费口径。
	bal, dashboardErr := fetchDashboardBilling(ctx, client, base, auth)
	if dashboardErr == nil {
		return bal, nil
	}
	if usageErr != nil {
		return 0, fmt.Errorf("余额接口均查询失败: /v1/usage: %v; dashboard billing: %w", usageErr, dashboardErr)
	}
	if status < 200 || status >= 300 {
		return 0, fmt.Errorf("余额接口均查询失败: /v1/usage: %v; dashboard billing: %w", httpStatusError(status, body), dashboardErr)
	}
	return 0, fmt.Errorf("余额接口均查询失败: /v1/usage 未返回余额字段; dashboard billing: %w", dashboardErr)
}

// fetchDashboardBilling 走 OpenAI 早期计费接口算余额（new-api / one-api 兼容）。
func fetchDashboardBilling(ctx context.Context, client *http.Client, base string, auth map[string]string) (float64, error) {
	subBody, err := fetchJSON(ctx, client, joinV1(base, "dashboard/billing/subscription"), auth)
	if err != nil {
		return 0, err
	}
	var sub struct {
		HardLimitUSD float64 `json:"hard_limit_usd"`
	}
	if err := json.Unmarshal(subBody, &sub); err != nil {
		return 0, fmt.Errorf("解析余额响应失败: %w", err)
	}
	// usage 为可选增强：拿到则算差值，拿不到就用 subscription 的额度兜底。
	usageBody, err := fetchJSON(ctx, client, joinV1(base, "dashboard/billing/usage"), auth)
	if err != nil {
		return sub.HardLimitUSD, nil
	}
	var usage struct {
		TotalUsage float64 `json:"total_usage"` // 美分
	}
	if err := json.Unmarshal(usageBody, &usage); err != nil {
		return sub.HardLimitUSD, nil
	}
	return sub.HardLimitUSD - usage.TotalUsage/100, nil
}

// joinV1 拼接 {base}/v1/{seg}（base 已含 /v1 时不重复，仿 fetch-models）。
func joinV1(base, seg string) string {
	if strings.HasSuffix(base, "/v1") {
		return base + "/" + seg
	}
	return base + "/v1/" + seg
}

// httpGet 发起 GET 请求，返回响应体与状态码；error 仅表网络/读取失败（非 2xx 不算 error，
// 由调用方按状态码决策——余额探测需区分 404 转兜底 vs 其它错误直接报）。
func httpGet(ctx context.Context, client *http.Client, endpoint string, headers map[string]string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("创建请求失败: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("请求上游失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("读取响应失败: %w", err)
	}
	return body, resp.StatusCode, nil
}

// httpStatusError 非 2xx 状态封装为错误（携带截断的错误体片段）。
func httpStatusError(status int, body []byte) error {
	snippet := strings.TrimSpace(string(body))
	if len(snippet) > 512 {
		snippet = snippet[:512]
	}
	return fmt.Errorf("上游返回 HTTP %d: %s", status, snippet)
}

// fetchJSON 发起 GET 请求并返回响应体；非 2xx 时携带截断的错误体报错。
func fetchJSON(ctx context.Context, client *http.Client, endpoint string, headers map[string]string) ([]byte, error) {
	body, status, err := httpGet(ctx, client, endpoint, headers)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, httpStatusError(status, body)
	}
	return body, nil
}

// ensureNonEmpty 空列表视为失败（契约：拿不到就报错）。
func ensureNonEmpty(models []string) ([]string, error) {
	if len(models) == 0 {
		return nil, fmt.Errorf("上游未返回任何模型")
	}
	return models, nil
}
