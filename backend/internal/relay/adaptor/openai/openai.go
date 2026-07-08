// Package openai 实现 openai_compatible 渠道适配器：
// OpenAI chat completions 协议直发（URL 拼接、Bearer 认证、定点改写、usage 提取）。
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
	"github.com/DouDOU-start/airgate-core/internal/relay/dto"
)

func init() {
	adaptor.Register("openai_compatible", func() adaptor.Adaptor { return Adaptor{} })
	// custom 类型按 OpenAI 兼容协议处理（与渠道 fetch-models 的默认分支口径一致）。
	adaptor.Register("custom", func() adaptor.Adaptor { return Adaptor{} })
}

// Adaptor openai_compatible 协议适配器（无状态）。
type Adaptor struct{}

// BuildRequest 构建上游请求。
//
// 按 info.Endpoint 分支选 URL 与请求改写策略：
//   - responses：ResponsesURL；仅公共改写（model 重写 + param_override），
//     绝不注入 stream_options（Responses API 无此参数，注入会污染请求）。
//   - chat_completions（默认）：ChatCompletionsURL；公共改写 + 流式 include_usage 注入。
//
// 公共改写顺序：model 重写 → param_override（set/remove）。
// param_override 语义：value 为 null（nil）删除字段，否则覆盖写入。
func (Adaptor) BuildRequest(ctx context.Context, info *adaptor.RelayInfo, req *dto.ChatRequest) (*http.Request, error) {
	var (
		body []byte
		url  string
		err  error
	)
	if info.Endpoint == adaptor.EndpointResponses {
		body, err = rewriteResponsesBody(info, req)
		url = ResponsesURL(info.Channel.BaseURL)
	} else {
		body, err = rewriteChatBody(info, req)
		url = ChatCompletionsURL(info.Channel.BaseURL)
	}
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+info.APIKey)
	for k, v := range info.Channel.HeaderOverride {
		httpReq.Header.Set(k, v)
	}
	return httpReq, nil
}

// rewriteCommon 公共改写：在 Clone 上做 model 重写 + param_override
// （原 req 不动，failover 各 attempt 互不污染）。
func rewriteCommon(info *adaptor.RelayInfo, req *dto.ChatRequest) (*dto.ChatRequest, error) {
	r := req.Clone()

	if info.UpstreamModel != "" && info.UpstreamModel != req.Model {
		if err := r.Set("model", info.UpstreamModel); err != nil {
			return nil, err
		}
	}
	for k, v := range info.Channel.ParamOverride {
		if v == nil {
			r.Remove(k)
			continue
		}
		if err := r.Set(k, v); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// rewriteChatBody chat completions 请求改写：公共改写 + 流式 include_usage 注入。
//
// 流式请求无条件强制 stream_options.include_usage=true（保留客户端 stream_options
// 的其他字段）：计费依赖尾部 usage chunk，客户端显式关闭会造成零计费流式请求。
// 客户端未请求 include_usage 时，透传层会吞掉 usage-only chunk（见 relaySSE）。
func rewriteChatBody(info *adaptor.RelayInfo, req *dto.ChatRequest) ([]byte, error) {
	r, err := rewriteCommon(info, req)
	if err != nil {
		return nil, err
	}
	if info.Stream {
		opts := map[string]json.RawMessage{}
		if raw, ok := r.Get("stream_options"); ok {
			// 非对象（null/非法）时按空对象重建；解析失败忽略原值。
			_ = json.Unmarshal(raw, &opts)
			if opts == nil {
				opts = map[string]json.RawMessage{}
			}
		}
		opts["include_usage"] = json.RawMessage("true")
		if err := r.Set("stream_options", opts); err != nil {
			return nil, err
		}
	}
	return r.Marshal()
}

// rewriteResponsesBody Responses 请求改写：仅公共改写（model 重写 + param_override）。
// 绝不注入 stream_options——Responses API 无此参数，流式 usage 来自 completed 事件，
// 注入会污染上游请求。
func rewriteResponsesBody(info *adaptor.RelayInfo, req *dto.ChatRequest) ([]byte, error) {
	r, err := rewriteCommon(info, req)
	if err != nil {
		return nil, err
	}
	return r.Marshal()
}

// normalizeBaseV1 归一 base_url 到 /v1 前缀：末尾 "/" 去除；
// 已以 /v1 结尾时不重复拼接（new-api 惯例）。
func normalizeBaseV1(baseURL string) string {
	base := strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(base, "/v1") {
		return base
	}
	return base + "/v1"
}

// ChatCompletionsURL 拼接上游 chat completions 端点。
func ChatCompletionsURL(baseURL string) string {
	return normalizeBaseV1(baseURL) + "/chat/completions"
}

// ResponsesURL 拼接上游 Responses API 端点（{base}/v1/responses）。
func ResponsesURL(baseURL string) string {
	return normalizeBaseV1(baseURL) + "/responses"
}

// ParseNonStreamResponse 解析非流式 2xx 响应：提取顶层 usage，
// 并把响应 model 字段回写为对外模型名（隐藏渠道 model_mapping）。
func (Adaptor) ParseNonStreamResponse(info *adaptor.RelayInfo, body []byte) ([]byte, *dto.Usage) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return body, nil
	}

	var usage *dto.Usage
	if raw, ok := fields["usage"]; ok && string(raw) != "null" {
		if u, parsed := dto.ParseUsage(raw); parsed {
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
