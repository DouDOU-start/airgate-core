package cursor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
)

// ProviderKey 是 cursor 在 CPA manager 中的 executor 键。
const ProviderKey = "cursor"

// Executor 实现 CPA 的 ProviderExecutor，把 OpenAI/Anthropic 请求翻译为
// Cursor Agent 协议。协议翻译不经 CPA translator，executor 直接产出
// 入口格式（ResponseFormat）的响应。
type Executor struct {
	client *Client
}

// NewExecutor 创建 cursor executor。
func NewExecutor() *Executor {
	return &Executor{client: NewClient("", "")}
}

// Identifier 返回 provider 键。
func (e *Executor) Identifier() string { return ProviderKey }

func protocolFromFormat(f sdktranslator.Format) Protocol {
	if f == sdktranslator.FormatClaude {
		return ProtoAnthropic
	}
	return ProtoOpenAI
}

func metaString(auth *coreauth.Auth, key string) string {
	if auth == nil || auth.Metadata == nil {
		return ""
	}
	if v, ok := auth.Metadata[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

// buildAuditBody 生成审计展示用的请求体摘要。真实上行是 Connect-RPC protobuf
// 双向流，无法原样展示；摘要保留排障关键信息（实际上行模型、消息/工具规模）。
func buildAuditBody(model, wireModel string, parsed *ParsedRequest, runRequestBytes int) []byte {
	toolNames := make([]string, 0, len(parsed.Tools))
	for _, t := range parsed.Tools {
		toolNames = append(toolNames, t.Name)
	}
	body, err := json.Marshal(map[string]any{
		"_transport":        "connect-rpc over http/2（protobuf 双向流，此处为摘要）",
		"model":             model,
		"wire_model":        wireModel,
		"reasoning_effort":  parsed.ReasoningEffort,
		"stream":            parsed.Stream,
		"messages":          len(parsed.Messages),
		"system_prompts":    len(parsed.SystemPrompts),
		"tools":             toolNames,
		"run_request_bytes": runRequestBytes,
	})
	if err != nil {
		return []byte("{}")
	}
	return body
}

// prepared 是一次执行前的公共准备产物。
type prepared struct {
	parsed       *ParsedRequest
	events       <-chan Event
	promptTokens int
	protocol     Protocol
}

func (e *Executor) prepare(ctx context.Context, auth *coreauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (*prepared, error) {
	accessToken := metaString(auth, "access_token")
	if accessToken == "" {
		return nil, &ConnectError{Code: "unauthenticated", Message: "cursor 账号缺少 access_token", HTTPStatus: http.StatusUnauthorized}
	}
	protocol := protocolFromFormat(opts.SourceFormat)
	parsed, err := ParseRequest(protocol, req.Payload)
	if err != nil {
		return nil, err
	}
	store := NewBlobStore()
	// 下游可经标准协议传思考档位（reasoning_effort / thinking.budget_tokens），
	// Cursor 侧档位编码在模型 id 后缀，这里改写实际上行的模型；响应回显仍用原 model。
	// 裸基础别名（如 claude-fable-5）在未指定档位时落到 medium 就近变体。
	wireModel := ResolveWireModel(req.Model, parsed.ReasoningEffort)
	reqBytes, tools, err := BuildRunRequest(parsed, wireModel, uuid.NewString(), store)
	if err != nil {
		return nil, err
	}
	proxyURL := ""
	if auth != nil {
		proxyURL = auth.ProxyURL
	}
	run := RunOptions{AccessToken: accessToken, ProxyURL: proxyURL}
	// 审计链路（CPA 固定上下文键下发 RoundTripper）：真实 body 是 protobuf
	// 双向流，给审计层一份可读的 JSON 摘要作为 GetBody 替身。
	//nolint:staticcheck
	if rt, ok := ctx.Value("cliproxy.roundtripper").(http.RoundTripper); ok && rt != nil {
		run.Transport = rt
		run.AuditBody = buildAuditBody(req.Model, wireModel, parsed, len(reqBytes))
	}
	events := RunSession(ctx, SessionOptions{
		Client:       e.client,
		Run:          run,
		RequestBytes: reqBytes,
		Tools:        tools,
		Store:        store,
	})
	return &prepared{
		parsed:       parsed,
		events:       events,
		promptTokens: EstimatePromptTokens(parsed),
		protocol:     protocolFromFormat(cliproxyexecutor.ResponseFormatOrSource(opts)),
	}, nil
}

// Execute 非流式执行：聚合事件后渲染完整响应。
func (e *Executor) Execute(ctx context.Context, auth *coreauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	p, err := e.prepare(ctx, auth, req, opts)
	if err != nil {
		return cliproxyexecutor.Response{}, err
	}
	agg := CollectEvents(p.events)
	if agg.Err != nil {
		return cliproxyexecutor.Response{}, agg.Err
	}
	var body []byte
	if p.protocol == ProtoAnthropic {
		body = BuildAnthropicResponse(agg, "msg_"+uuid.NewString(), req.Model, p.promptTokens)
	} else {
		body = BuildOpenAIResponse(agg, "chatcmpl-"+uuid.NewString(), req.Model, time.Now().Unix(), p.promptTokens)
	}
	return cliproxyexecutor.Response{
		Payload: body,
		Headers: http.Header{"Content-Type": []string{"application/json"}},
	}, nil
}

// ExecuteStream 流式执行：把中立事件渲染成入口协议 SSE。
// OpenAI 输出裸 chunk JSON（转发层负责补 data: 帧），Anthropic 输出完整 SSE 文本。
func (e *Executor) ExecuteStream(ctx context.Context, auth *coreauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
	p, err := e.prepare(ctx, auth, req, opts)
	if err != nil {
		return nil, err
	}
	out := make(chan cliproxyexecutor.StreamChunk, 16)
	go func() {
		defer close(out)
		if p.protocol == ProtoAnthropic {
			r := NewAnthropicStreamRenderer("msg_"+uuid.NewString(), req.Model, p.promptTokens)
			for ev := range p.events {
				if errEv, ok := ev.(ErrEvent); ok {
					out <- cliproxyexecutor.StreamChunk{Err: errEv.Err}
					return
				}
				for _, frame := range r.Render(ev) {
					out <- cliproxyexecutor.StreamChunk{Payload: EncodeSSE(frame)}
				}
			}
			return
		}
		r := NewOpenAIStreamRenderer("chatcmpl-"+uuid.NewString(), req.Model, time.Now().Unix(), p.promptTokens)
		for ev := range p.events {
			if errEv, ok := ev.(ErrEvent); ok {
				out <- cliproxyexecutor.StreamChunk{Err: errEv.Err}
				return
			}
			for _, frame := range r.Render(ev) {
				out <- cliproxyexecutor.StreamChunk{Payload: frame.Data}
			}
		}
		out <- cliproxyexecutor.StreamChunk{Payload: []byte("[DONE]")}
	}()
	return &cliproxyexecutor.StreamResult{
		Headers: http.Header{"Content-Type": []string{"text/event-stream"}},
		Chunks:  out,
	}, nil
}

// Refresh 用 refresh token 换新 access token 并回写 auth。
func (e *Executor) Refresh(ctx context.Context, auth *coreauth.Auth) (*coreauth.Auth, error) {
	if auth == nil {
		return nil, fmt.Errorf("auth 为空")
	}
	refresh := metaString(auth, "refresh_token")
	if refresh == "" {
		return nil, fmt.Errorf("cursor 账号缺少 refresh_token")
	}
	hc, err := NewHTTPClient(auth.ProxyURL)
	if err != nil {
		return nil, err
	}
	tokens, err := RefreshToken(ctx, hc, refresh)
	if err != nil {
		return nil, err
	}
	if auth.Metadata == nil {
		auth.Metadata = make(map[string]any)
	}
	auth.Metadata["access_token"] = tokens.AccessToken
	auth.Metadata["refresh_token"] = tokens.RefreshToken
	if !tokens.ExpiresAt.IsZero() {
		auth.Metadata["expired"] = tokens.ExpiresAt.UTC().Format(time.RFC3339)
	}
	auth.Metadata["last_refresh"] = time.Now().UTC().Format(time.RFC3339)
	auth.UpdatedAt = time.Now().UTC()
	return auth, nil
}

// CountTokens 本地估算 token 数（Cursor 无计数端点）。
func (e *Executor) CountTokens(_ context.Context, _ *coreauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	protocol := protocolFromFormat(opts.SourceFormat)
	parsed, err := ParseRequest(protocol, req.Payload)
	if err != nil {
		return cliproxyexecutor.Response{}, err
	}
	tokens := EstimatePromptTokens(parsed)
	return cliproxyexecutor.Response{
		Payload: []byte(fmt.Sprintf(`{"input_tokens":%d}`, tokens)),
		Headers: http.Header{"Content-Type": []string{"application/json"}},
	}, nil
}

// HttpRequest 注入 Bearer 凭证并按账号代理执行任意 HTTP 请求。
func (e *Executor) HttpRequest(ctx context.Context, auth *coreauth.Auth, req *http.Request) (*http.Response, error) {
	accessToken := metaString(auth, "access_token")
	if accessToken == "" {
		return nil, fmt.Errorf("cursor 账号缺少 access_token")
	}
	proxyURL := ""
	if auth != nil {
		proxyURL = auth.ProxyURL
	}
	hc, err := NewHTTPClient(proxyURL)
	if err != nil {
		return nil, err
	}
	req = req.WithContext(ctx)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	return hc.Do(req)
}
