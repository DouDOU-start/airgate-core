// Package anthropic 实现 anthropic 渠道适配器（纯透传，零翻译）：
// 入口为 Anthropic Messages（/v1/messages）与 token 计数（/v1/messages/count_tokens），
// 出口直发上游同名端点。
//
// 职责收缩为：URL 拼接、x-api-key/anthropic-version 认证头、
// 渠道模型名重写与 param_override（原始字段表上定点改写）、
// usage 归一化到 dto.Usage（含 5m/1h 双档缓存写明细）。
// 请求/响应体不做任何跨协议翻译。
package anthropic

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
	adaptor.Register("anthropic", func() adaptor.Adaptor { return Adaptor{} })
}

// anthropicVersion Messages API 版本头（缺省值；可被渠道 header_override 覆盖）。
const anthropicVersion = "2023-06-01"

// Adaptor anthropic 协议适配器（无状态）。
type Adaptor struct{}

// 确保实现了流观察能力接口。
var _ adaptor.StreamObserving = Adaptor{}

// BuildRequest 构建 Anthropic 上游请求：
// URL {base}/v1/messages（count_tokens 端点拼 /count_tokens 后缀），
// x-api-key 认证 + anthropic-version + header_override，
// 请求体在原始字段表上仅做 model 重写与 param_override，其余原样透传
// （原生客户端自带 max_tokens 等必填字段，网关不兜底、不改写）。
func (Adaptor) BuildRequest(ctx context.Context, info *adaptor.RelayInfo, req *dto.ChatRequest) (*http.Request, error) {
	// 纯透传：anthropic 渠道只可从 /v1/messages 系入口路由到（Pick 协议过滤保证），
	// 其余端点即装配错误——明确报错（管线转 400，不 failover）。
	var url string
	switch info.Endpoint {
	case adaptor.EndpointMessages:
		url = messagesURL(info.ChannelKey.BaseURL)
	case adaptor.EndpointMessagesCountTokens:
		// token 计数端点（零计费，记账跳过在 pipeline 侧）；请求改写与 messages 一致。
		url = messagesURL(info.ChannelKey.BaseURL) + "/count_tokens"
	default:
		return nil, errors.New("anthropic 渠道仅支持 /v1/messages 与 /v1/messages/count_tokens 端点")
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
	httpReq.Header.Set("x-api-key", info.APIKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)
	for k, v := range info.ChannelKey.HeaderOverride {
		httpReq.Header.Set(k, v)
	}
	return httpReq, nil
}

// rewriteBody 在 Clone 的字段表上做 model 重写 + param_override
// （原 req 不动，failover 各 attempt 互不污染），各字段 RawMessage 原样保留。
func rewriteBody(info *adaptor.RelayInfo, req *dto.ChatRequest) ([]byte, error) {
	r := req.Clone()
	if info.UpstreamModel != "" && info.UpstreamModel != req.Model {
		if err := r.Set("model", info.UpstreamModel); err != nil {
			return nil, err
		}
	}
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

// messagesURL 拼接 {base}/v1/messages（base 已含 /v1 时不重复）。
func messagesURL(baseURL string) string {
	base := strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(base, "/v1") {
		return base + "/messages"
	}
	return base + "/v1/messages"
}

// ParseNonStreamResponse 解析非流式 2xx 响应：
// 提取 Anthropic usage 并归一化（input_tokens 不含缓存读，PromptTokens 须补上，
// 含 5m/1h 双档缓存写明细），把响应 model 字段回写为 RequestModel，其余字段原样。
// 解析失败时原样返回 body（usage=nil），交由上层按无 usage 处理。
func (Adaptor) ParseNonStreamResponse(info *adaptor.RelayInfo, body []byte) ([]byte, *dto.Usage) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		return body, nil
	}

	var usage *dto.Usage
	if raw, ok := fields["usage"]; ok && string(raw) != "null" {
		var au anthropicUsage
		if json.Unmarshal(raw, &au) == nil {
			u := normalizeUsage(au).dtoUsage()
			usage = &u
		}
	}

	if raw, ok := fields["model"]; ok {
		var upstream string
		if err := json.Unmarshal(raw, &upstream); err == nil && upstream != info.RequestModel {
			if rewritten, err := json.Marshal(info.RequestModel); err == nil {
				fields["model"] = rewritten
				if out, err := json.Marshal(fields); err == nil {
					body = out
				}
			}
		}
	}
	return body, usage
}

// NewStreamObserver 每条流一个观察器实例（有状态：累积 usage 与完成/错误信号）。
func (Adaptor) NewStreamObserver(info *adaptor.RelayInfo) adaptor.StreamObserver {
	return &streamObserver{}
}
