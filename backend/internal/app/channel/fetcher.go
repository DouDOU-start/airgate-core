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
//   - openai_compatible / custom：GET {base_url}/v1/models，Bearer 认证，解析 data[].id
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
	default: // openai_compatible / custom：按 OpenAI 兼容端点尽力拉取
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

// fetchJSON 发起 GET 请求并返回响应体；非 2xx 时携带截断的错误体报错。
func fetchJSON(ctx context.Context, client *http.Client, endpoint string, headers map[string]string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求上游失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet := strings.TrimSpace(string(body))
		if len(snippet) > 512 {
			snippet = snippet[:512]
		}
		return nil, fmt.Errorf("上游返回 HTTP %d: %s", resp.StatusCode, snippet)
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
