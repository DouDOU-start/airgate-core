package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/DouDOU-start/airgate-core/internal/billing"
	"github.com/DouDOU-start/airgate-core/internal/errlog"
	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
	"github.com/DouDOU-start/airgate-core/internal/relay/dto"
	"github.com/DouDOU-start/airgate-core/internal/relay/outcome"
	"github.com/DouDOU-start/airgate-core/internal/relay/pricing"
	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
)

// channelTestTimeout 渠道测试请求总超时。
const channelTestTimeout = 30 * time.Second

// channelTestPlan 按渠道协议返回测试请求的端点/原生请求体
// （纯透传架构：adaptor 不做翻译，测试体须是渠道协议的原生最小请求，max_tokens=1 控成本）。
// testEndpoint 仅对 openai 协议渠道生效（chat_completions / responses 二选一，
// 空值默认 chat_completions）；anthropic/gemini 各只有一个端点，忽略该参数。
func channelTestPlan(channelType, model, testEndpoint string) (endpoint, body string) {
	switch channelType {
	case "anthropic":
		return adaptor.EndpointMessages,
			fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}],"max_tokens":1}`, model)
	case "gemini":
		// Gemini 请求体不携带 model（在 URL 层，由 RelayInfo.UpstreamModel 拼接）。
		return adaptor.EndpointGenerateContent,
			`{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"maxOutputTokens":1}}`
	default:
		if testEndpoint == adaptor.EndpointResponses {
			// Responses API 的 max_output_tokens 下限为 16。
			return adaptor.EndpointResponses,
				fmt.Sprintf(`{"model":%q,"input":"hi","max_output_tokens":16,"stream":false}`, model)
		}
		return adaptor.EndpointChatCompletions,
			fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}],"max_tokens":1,"stream":false}`, model)
	}
}

// TestChannel 走完整 adaptor 链路对渠道发一次非流式测试请求，返回延迟毫秒。
// snap 须携带解密后的 APIKeys（由调用方构造，见 server 层 Tester 适配器）；
// 测试结果落库与 disabled_auto 恢复由 channel service 编排，此处只做请求。
// 测试是真实的上游消耗：成功按 0 费用落消费记录（渠道成本口径照记），
// 失败与转发失败同表留痕。testEndpoint 语义见 channelTestPlan。
func (p *Pipeline) TestChannel(ctx context.Context, snap *registry.ChannelKeySnapshot, model, testEndpoint string) (latencyMs int, err error) {
	// 任务类 key（视频/音乐）测试会真实产生付费任务，不支持一键测试；
	// 直接拒绝，不留失败痕（配置性限制而非渠道故障）。
	if snap.Type == registry.ProtocolOpenAIVideo || snap.Type == registry.ProtocolSuno {
		return 0, errors.New("任务类渠道（视频/音乐）暂不支持一键测试")
	}
	start := time.Now()
	latencyMs, endpoint, usage, err := p.testChannel(ctx, snap, model, testEndpoint)
	if err != nil {
		// 管理员主动取消（停止/关窗断开请求）不留失败痕：
		// ctx 取消是操作行为而非渠道故障；30s 超时属内层 deadline，外层 ctx 无损，照常留痕。
		if p.errSink != nil && ctx.Err() == nil {
			p.errSink.Record(errlog.Entry{
				RequestID:   uuid.NewString(),
				Source:      errlog.SourceChannelTest,
				Model:       model,
				ChannelID:   snap.ChannelID,
				ChannelName: snap.ChannelName,
				DurationMs:  time.Since(start).Milliseconds(),
				Message:     err.Error(),
			})
		}
		return 0, err
	}
	p.recordTestUsage(snap, model, endpoint, usage, time.Since(start).Milliseconds())
	return latencyMs, nil
}

// testEndpointPath 测试请求的代表性对外端点路径（仅记录用；测试不经对外路由，
// 入参为实际使用的 adaptor 端点常量）。
func testEndpointPath(endpoint string) string {
	switch endpoint {
	case adaptor.EndpointMessages:
		return "/v1/messages"
	case adaptor.EndpointGenerateContent:
		return "/v1beta/generateContent"
	case adaptor.EndpointResponses:
		return "/v1/responses"
	default:
		return "/v1/chat/completions"
	}
}

// recordTestUsage 渠道测试成功落消费记录：无用户/Key 归属、三管道费用为 0
// （不扣任何人余额），total 照价目表实算（缺价记 0），渠道成本口径照常成立
// （total × cost_ratio——测试确实消耗了渠道额度）。source 固定 channel_test，
// 供前端把发起方标为「渠道测试」。
func (p *Pipeline) recordTestUsage(snap *registry.ChannelKeySnapshot, model, endpoint string, usage *dto.Usage, durationMs int64) {
	if p.sink == nil {
		return
	}
	var u dto.Usage
	if usage != nil {
		u = *usage
	}
	price, _ := p.pricing.Get(model)
	costs := pricing.ComputeCosts(price, pricing.Usage{
		PromptTokens:          u.PromptTokens,
		CompletionTokens:      u.CompletionTokens,
		CachedTokens:          u.CachedTokens,
		CacheCreationTokens:   u.CacheCreationTokens,
		CacheCreation5mTokens: u.CacheCreation5mTokens,
		CacheCreation1hTokens: u.CacheCreation1hTokens,
		Calls:                 u.Calls,
	}, "")
	calc := p.calculator.Calculate(billing.CalculateInput{
		InputCost:         costs.Input,
		OutputCost:        costs.Output,
		CachedInputCost:   costs.Cached,
		CacheCreationCost: costs.CacheCreation5m + costs.CacheCreation1h,
		BillingRate:       0, // 三管道归零：测试不向任何用户/Key 计费
		SellRate:          0,
		AccountRate:       snap.EffectiveCostRatio(),
	})
	inputTokens := u.PromptTokens - u.CachedTokens
	if inputTokens < 0 {
		inputTokens = 0
	}
	p.sink.Record(billing.UsageRecord{
		ChannelID:             snap.ChannelID,
		ChannelKeyID:          snap.KeyID,
		Model:                 model,
		InputTokens:           inputTokens,
		OutputTokens:          u.CompletionTokens,
		CachedInputTokens:     u.CachedTokens,
		CacheCreationTokens:   u.CacheCreationTokens,
		CacheCreation5mTokens: u.CacheCreation5mTokens,
		CacheCreation1hTokens: u.CacheCreation1hTokens,
		Calls:                 u.Calls,
		InputPrice:            price.Input,
		OutputPrice:           price.Output,
		CachedInputPrice:      price.CachedInput,
		CacheCreationPrice:    price.CacheCreation5m,
		CacheCreation1hPrice:  price.CacheCreation1h,
		InputCost:             calc.InputCost,
		OutputCost:            calc.OutputCost,
		CachedInputCost:       calc.CachedInputCost,
		CacheCreationCost:     calc.CacheCreationCost,
		TotalCost:             calc.TotalCost,
		AccountRateMultiplier: calc.AccountRateMultiplier,
		Endpoint:              testEndpointPath(endpoint),
		Source:                billing.SourceChannelTest,
		DurationMs:            durationMs,
		RequestID:             uuid.NewString(),
	})
}

// testChannel 渠道测试主体；成功时回传实际使用的端点常量与上游 usage（可能为 nil）供落账。
func (p *Pipeline) testChannel(ctx context.Context, snap *registry.ChannelKeySnapshot, model, testEndpoint string) (int, string, *dto.Usage, error) {
	if model == "" {
		return 0, "", nil, errors.New("缺少测试模型")
	}
	if snap.APIKey == "" {
		return 0, "", nil, errors.New("密钥端点未配置 API Key")
	}

	ad, err := adaptor.GetAdaptor(snap.Type)
	if err != nil {
		return 0, "", nil, err
	}
	client := p.client

	endpoint, testBody := channelTestPlan(snap.Type, model, testEndpoint)
	req, err := dto.ParseChatRequest([]byte(testBody))
	if err != nil {
		return 0, "", nil, err
	}
	req.Model = model

	info := &adaptor.RelayInfo{
		ChannelKey:    snap,
		APIKey:        snap.APIKey,
		RequestModel:  model,
		UpstreamModel: upstreamModel(snap, model),
		Stream:        false,
		Endpoint:      endpoint,
		Client:        client,
	}

	ctx, cancel := context.WithTimeout(ctx, channelTestTimeout)
	defer cancel()
	httpReq, err := ad.BuildRequest(ctx, info, req)
	if err != nil {
		return 0, "", nil, err
	}

	start := time.Now()
	resp, err := client.Do(httpReq)
	if err != nil {
		return 0, "", nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxTestBodyBytes))
	latency := int(time.Since(start).Milliseconds())
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// 错误体片段会进入管理端 502 响应与日志：先对本 key 脱敏。
		return 0, "", nil, fmt.Errorf("上游返回 HTTP %d: %s", resp.StatusCode,
			outcome.SanitizeKeyLeak(outcome.BodySnippet(body), []string{snap.APIKey}))
	}
	// 解析上游 usage 供落账；解析失败不影响测试结果（usage 记 0）。
	_, usage := ad.ParseNonStreamResponse(info, body)
	if usage == nil {
		// 部分上游（多见于代理商 /v1/responses 实现）无视 stream:false 恒回 SSE：
		// JSON 整体解析必然失败，改从 SSE 帧提取 usage（completed 事件在流末尾）。
		usage = extractUsageFromSSE(body)
	}
	if usage == nil {
		// 2xx 却提不出 usage：上游返回了非预期形态（缺 usage 字段等）。
		// 留脱敏片段定位，勿静默记零。
		slog.Warn("channel_test_usage_missing",
			"channel_key_id", snap.KeyID, "model", model, "endpoint", endpoint,
			"content_type", resp.Header.Get("Content-Type"),
			"body_snippet", outcome.SanitizeKeyLeak(outcome.BodySnippet(body), []string{snap.APIKey}))
	}
	return latency, endpoint, usage, nil
}

// maxTestBodyBytes 测试响应体读取上限：上游可能对测试请求回整条 SSE 流
// （含多个 response 快照事件），上限取宽于错误体，保证读到流尾的 usage。
const maxTestBodyBytes = 256 << 10

// extractUsageFromSSE 从 SSE 文本逐帧提取 usage，取最后一个可解析帧
// （Responses 的 response.completed / chat-completions 的末帧 usage chunk 都在流尾）。
// ExtractResponsesUsage 兼容 response.usage 嵌套与顶层 usage 两种形态。
func extractUsageFromSSE(body []byte) *dto.Usage {
	var last *dto.Usage
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		if u, ok := dto.ExtractResponsesUsage([]byte(payload)); ok {
			v := u
			last = &v
		}
	}
	return last
}
