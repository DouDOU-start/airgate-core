// Package gemini 实现 gemini 渠道适配器（纯透传，零翻译）：
// 入口为 Gemini generateContent / streamGenerateContent / predict / countTokens
// （/v1beta/models/{model}:*），出口按同名动词直发上游。
//
// 职责收缩为：URL 拼接（model 重写发生在 URL 层）、x-goog-api-key 认证头、
// param_override（generationConfig 等字段表层覆盖）、usageMetadata 归一化到 dto.Usage
// （promptTokenCount/candidatesTokenCount/thoughtsTokenCount/cachedContentTokenCount）。
// 请求/响应体不做任何跨协议翻译。
package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
	"github.com/DouDOU-start/airgate-core/internal/relay/dto"
)

func init() {
	adaptor.Register("gemini", func() adaptor.Adaptor { return Adaptor{} })
}

// Adaptor gemini 协议适配器（无状态）。
type Adaptor struct{}

var _ adaptor.StreamObserving = Adaptor{}

// BuildRequest 构建 Gemini 上游请求：
// URL {base}/v1beta/models/{upstreamModel}:{动词}（generateContent 流式为
// streamGenerateContent?alt=sse；predict / countTokens 恒非流式），
// x-goog-api-key 认证 + header_override；请求体在原始字段表上仅做 param_override
// （Gemini 请求体不携带 model，模型名重写发生在 URL 层），其余原样透传。
func (Adaptor) BuildRequest(ctx context.Context, info *adaptor.RelayInfo, req *dto.ChatRequest) (*http.Request, error) {
	model := info.UpstreamModel
	if model == "" {
		model = req.Model
	}

	// 纯透传：gemini 渠道只可从 /v1beta 各动词入口路由到（Pick 协议过滤保证）。
	var url string
	switch info.Endpoint {
	case adaptor.EndpointGenerateContent:
		url = generateURL(info.ChannelKey.BaseURL, model, info.Stream)
	case adaptor.EndpointPredict:
		// Imagen 系按次生图端点（无流式形态）。
		url = methodURL(info.ChannelKey.BaseURL, model, "predict", false)
	case adaptor.EndpointCountTokens:
		// token 计数端点（零计费，记账跳过在 pipeline 侧）。
		url = methodURL(info.ChannelKey.BaseURL, model, "countTokens", false)
	default:
		return nil, errors.New("gemini 渠道仅支持 generateContent / predict / countTokens 端点")
	}

	body, err := rewriteBody(info, req)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-goog-api-key", info.APIKey)
	for k, v := range info.ChannelKey.HeaderOverride {
		httpReq.Header.Set(k, v)
	}
	return httpReq, nil
}

// rewriteBody 在 Clone 的字段表上套用 param_override（null 删除、其余覆盖），
// 各字段 RawMessage 原样保留（原 req 不动，failover 各 attempt 互不污染）。
func rewriteBody(info *adaptor.RelayInfo, req *dto.ChatRequest) ([]byte, error) {
	r := req.Clone()
	for k, v := range info.ChannelKey.ParamOverride {
		if v == nil {
			r.Remove(k)
			continue
		}
		if err := r.Set(k, v); err != nil {
			return nil, err
		}
	}
	return r.Marshal()
}

// methodURL 拼接 Gemini 动词端点 {base}/v1beta/models/{model}:{method}；sse 时加 ?alt=sse。
func methodURL(baseURL, model, method string, sse bool) string {
	base := strings.TrimRight(baseURL, "/")
	if !strings.HasSuffix(base, "/v1beta") && !strings.HasSuffix(base, "/v1") {
		base += "/v1beta"
	}
	url := base + "/models/" + model + ":" + method
	if sse {
		url += "?alt=sse"
	}
	return url
}

// generateURL 拼接 generateContent 端点。model 在 path，流式走 streamGenerateContent?alt=sse。
func generateURL(baseURL, model string, stream bool) string {
	if stream {
		return methodURL(baseURL, model, "streamGenerateContent", true)
	}
	return methodURL(baseURL, model, "generateContent", false)
}

// ParseNonStreamResponse 解析非流式 2xx 响应：
// 提取 usageMetadata 归一化为 dto.Usage（thoughtsTokenCount 并入输出计费），
// 把响应 modelVersion 回写为 RequestModel（隐藏渠道 model_mapping），其余字段原样。
// :predict（Imagen）另提取 predictions 数组长度写入 usage.Calls（产出张数，
// 按次×张数计费用）；该端点通常无 usageMetadata（token 用量全 0，仅携 Calls）。
// 解析失败时原样返回 body（usage=nil）。
func (Adaptor) ParseNonStreamResponse(info *adaptor.RelayInfo, body []byte) ([]byte, *dto.Usage) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		return body, nil
	}

	var usage *dto.Usage
	if raw, ok := fields["usageMetadata"]; ok && string(raw) != "null" {
		var gu geminiUsage
		if json.Unmarshal(raw, &gu) == nil && !gu.empty() {
			u := gu.dtoUsage()
			usage = &u
		}
	}

	if info.Endpoint == adaptor.EndpointPredict {
		if raw, ok := fields["predictions"]; ok && string(raw) != "null" {
			var items []json.RawMessage
			if json.Unmarshal(raw, &items) == nil && len(items) > 0 {
				if usage == nil {
					usage = &dto.Usage{}
				}
				usage.Calls = len(items)
			}
		}
	}

	if raw, ok := fields["modelVersion"]; ok {
		var upstream string
		if err := json.Unmarshal(raw, &upstream); err == nil && upstream != info.RequestModel {
			if rewritten, err := json.Marshal(info.RequestModel); err == nil {
				fields["modelVersion"] = rewritten
				if out, err := json.Marshal(fields); err == nil {
					body = out
				}
			}
		}
	}
	return body, usage
}

// NewStreamObserver 每条流一个观察器实例。
func (Adaptor) NewStreamObserver(info *adaptor.RelayInfo) adaptor.StreamObserver {
	return &streamObserver{}
}
