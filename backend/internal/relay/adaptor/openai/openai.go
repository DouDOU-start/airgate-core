// Package openai 实现 openai_compatible / custom 渠道适配器：
// OpenAI chat completions / responses / images 协议直发
// （URL 拼接、Bearer 认证、定点改写、usage/张数提取），本就纯透传。
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
	"github.com/DouDOU-start/airgate-core/internal/relay/dto"
)

func init() {
	adaptor.Register("openai_compatible", func() adaptor.Adaptor { return Adaptor{} })
	// custom = OpenAI 兼容自定义渠道，与 openai_compatible 同协议同适配器
	//（归 openai 协议组，见 registry 协议过滤表）。
	adaptor.Register("custom", func() adaptor.Adaptor { return Adaptor{} })
}

// Adaptor openai_compatible 协议适配器（无状态）。
type Adaptor struct{}

// BuildRequest 构建上游请求。
//
// 按 info.Endpoint 分支选 URL 与请求改写策略：
//   - responses：ResponsesURL；仅公共改写（model 重写 + param_override），
//     绝不注入 stream_options（Responses API 无此参数，注入会污染请求）。
//   - images generations：ImagesGenerationsURL；仅公共改写（Images API 同样无
//     stream_options 概念，绝不注入）。
//   - images edits：ImagesEditsURL；multipart 透传——渠道 model_mapping 未生效时
//     RawBody 原始字节 + 原 Content-Type（含 boundary）直发上游、零重组；
//     映射生效时仅定点重写 model 普通字段值（模型重写属 adaptor 职责清单），
//     其余 part 逐字节复制、boundary 沿用（见 RewriteMultipartModel）；
//     param_override 对 multipart 不生效（JSON 语义的覆盖值无法映射到表单字段）。
//   - chat_completions（默认）：ChatCompletionsURL；公共改写 + 流式 include_usage 注入。
//
// 公共改写顺序：model 重写 → param_override（set/remove）。
// param_override 语义：value 为 null（nil）删除字段，否则覆盖写入。
func (Adaptor) BuildRequest(ctx context.Context, info *adaptor.RelayInfo, req *dto.ChatRequest) (*http.Request, error) {
	var (
		body        []byte
		url         string
		contentType = "application/json"
		err         error
	)
	switch info.Endpoint {
	case adaptor.EndpointResponses:
		body, err = rewritePlainBody(info, req)
		url = ResponsesURL(info.ChannelKey.BaseURL)
	case adaptor.EndpointImagesGenerations:
		body, err = rewritePlainBody(info, req)
		url = ImagesGenerationsURL(info.ChannelKey.BaseURL)
	case adaptor.EndpointImagesEdits:
		if len(info.RawBody) == 0 {
			return nil, errors.New("images edits 缺少原始 multipart 请求体")
		}
		body = info.RawBody
		contentType = info.RawContentType
		// model_mapping 生效时定点重写 multipart 的 model 字段（沿用原 boundary，
		// Content-Type 不变）；未生效时原样直发、零重组。
		if info.UpstreamModel != "" && info.UpstreamModel != info.RequestModel {
			body, err = RewriteMultipartModel(info.RawBody, info.RawContentType, info.UpstreamModel)
		}
		url = ImagesEditsURL(info.ChannelKey.BaseURL)
	case adaptor.EndpointChatCompletions:
		body, err = rewriteChatBody(info, req)
		url = ChatCompletionsURL(info.ChannelKey.BaseURL)
	case adaptor.EndpointAlphaSearch:
		// codex 联网搜索：请求体原样透传（含 model 重写），上游 {base}/v1/alpha/search。
		body, err = rewritePlainBody(info, req)
		url = AlphaSearchURL(info.ChannelKey.BaseURL)
	default:
		// 纯透传：openai 协议渠道只可从 chat/responses/images 入口路由到（Pick 协议过滤保证）。
		return nil, errors.New("openai 兼容渠道仅支持 chat completions / responses / images 端点")
	}
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("Authorization", "Bearer "+info.APIKey)
	for k, v := range info.ChannelKey.HeaderOverride {
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
	for k, v := range info.ChannelKey.ParamOverride {
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

// rewritePlainBody 仅做公共改写（model 重写 + param_override）的 JSON 端点共用
// （responses / images generations）：绝不注入 stream_options——这些端点无此参数
// （Responses 的流式 usage 来自 completed 事件），注入会污染上游请求。
func rewritePlainBody(info *adaptor.RelayInfo, req *dto.ChatRequest) ([]byte, error) {
	r, err := rewriteCommon(info, req)
	if err != nil {
		return nil, err
	}
	return r.Marshal()
}

// RewriteMultipartModel 定点重写 multipart 体中 model 普通字段的值
// （仅渠道 model_mapping 生效时走此路径）。零翻译边界内的最小改写：
//   - 沿用原 boundary（Content-Type 头无需变更）；
//   - 各 part 头原样复制、内容经 NextRawPart 逐字节复制（不解码传输编码，
//     文件字节零改动）；仅 model 普通字段（非文件 part）的值替换为上游模型名。
func RewriteMultipartModel(body []byte, contentType, upstreamModel string) ([]byte, error) {
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, fmt.Errorf("解析 multipart Content-Type 失败: %w", err)
	}
	boundary := params["boundary"]
	if boundary == "" {
		return nil, errors.New("multipart 缺少 boundary")
	}

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if err := w.SetBoundary(boundary); err != nil {
		return nil, err
	}
	mr := multipart.NewReader(bytes.NewReader(body), boundary)
	for {
		part, err := mr.NextRawPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("解析 multipart 失败: %w", err)
		}
		dst, err := w.CreatePart(part.Header)
		if err != nil {
			return nil, err
		}
		if part.FormName() == "model" && part.FileName() == "" {
			if _, err := io.WriteString(dst, upstreamModel); err != nil {
				return nil, err
			}
			continue
		}
		if _, err := io.Copy(dst, part); err != nil {
			return nil, fmt.Errorf("复制 multipart part 失败: %w", err)
		}
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
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

// ImagesGenerationsURL 拼接上游生图端点（{base}/v1/images/generations）。
func ImagesGenerationsURL(baseURL string) string {
	return normalizeBaseV1(baseURL) + "/images/generations"
}

// ImagesEditsURL 拼接上游图像编辑端点（{base}/v1/images/edits）。
func ImagesEditsURL(baseURL string) string {
	return normalizeBaseV1(baseURL) + "/images/edits"
}

// AlphaSearchURL 拼接上游 codex 联网搜索端点（{base}/v1/alpha/search）。
func AlphaSearchURL(baseURL string) string {
	return normalizeBaseV1(baseURL) + "/alpha/search"
}

// ParseNonStreamResponse 解析非流式 2xx 响应：提取顶层 usage，
// 并把响应 model 字段回写为对外模型名（隐藏渠道 model_mapping）。
// 图像端点另提取 data 数组长度写入 usage.Calls（产出张数，按次×张数计费用）。
func (Adaptor) ParseNonStreamResponse(info *adaptor.RelayInfo, body []byte) ([]byte, *dto.Usage) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return body, nil
	}

	var usage *dto.Usage
	if raw, ok := fields["usage"]; ok && string(raw) != "null" {
		// gpt-image 系图像响应的 usage 形态（input_tokens/output_tokens/total_tokens，
		// input_tokens_details 里可能有 image_tokens/text_tokens）与 Responses 命名同构，
		// ParseUsage 的回退候选已覆盖并归一化到 dto.Usage（无需分支）。
		if u, parsed := dto.ParseUsage(raw); parsed {
			usage = &u
		}
	}

	// 图像端点：data 数组长度即产出张数（以响应为准，非请求 n），写入 usage.Calls
	// 供 PerRequest 按次×张数计费；token usage（gpt-image 系）若存在照常提取，两者并存
	//（PerRequest==0 时按 token 计费、Calls 不参与）。
	if info.Endpoint == adaptor.EndpointImagesGenerations || info.Endpoint == adaptor.EndpointImagesEdits {
		if raw, ok := fields["data"]; ok && string(raw) != "null" {
			var items []json.RawMessage
			if json.Unmarshal(raw, &items) == nil && len(items) > 0 {
				if usage == nil {
					usage = &dto.Usage{}
				}
				usage.Calls = len(items)
			}
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
