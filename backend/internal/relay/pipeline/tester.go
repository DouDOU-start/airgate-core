package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
	"github.com/DouDOU-start/airgate-core/internal/relay/dto"
	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
)

// channelTestTimeout 渠道测试请求总超时。
const channelTestTimeout = 30 * time.Second

// channelTestBody 渠道测试请求体模板（最小成本：max_tokens=1，非流式）。
const channelTestBody = `{"model":%q,"messages":[{"role":"user","content":"hi"}],"max_tokens":1,"stream":false}`

// TestChannel 走完整 adaptor 链路对渠道发一次非流式测试请求，返回延迟毫秒。
// snap 须携带解密后的 APIKeys（由调用方构造，见 server 层 Tester 适配器）；
// 测试结果落库与 disabled_auto 恢复由 channel service 编排，此处只做请求。
func (p *Pipeline) TestChannel(ctx context.Context, snap *registry.ChannelSnapshot, model string) (int, error) {
	if model == "" {
		return 0, errors.New("缺少测试模型")
	}
	if len(snap.APIKeys) == 0 {
		return 0, errors.New("渠道未配置 API Key")
	}

	ad, err := adaptor.GetAdaptor(snap.Type)
	if err != nil {
		return 0, err
	}
	client, err := p.clients.Get(snap.ProxyURL)
	if err != nil {
		return 0, err
	}

	req, err := dto.ParseChatRequest([]byte(fmt.Sprintf(channelTestBody, model)))
	if err != nil {
		return 0, err
	}

	info := &adaptor.RelayInfo{
		Channel:       snap,
		APIKey:        snap.APIKeys[0],
		RequestModel:  model,
		UpstreamModel: upstreamModel(snap, model),
		Stream:        false,
		EntryProtocol: entryProtocolOpenAI,
		Client:        client,
	}

	ctx, cancel := context.WithTimeout(ctx, channelTestTimeout)
	defer cancel()
	httpReq, err := ad.BuildRequest(ctx, info, req)
	if err != nil {
		return 0, err
	}

	start := time.Now()
	resp, err := client.Do(httpReq)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
	latency := int(time.Since(start).Milliseconds())
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// 错误体片段会进入管理端 502 响应与日志：先对渠道全部 key 脱敏。
		return 0, fmt.Errorf("上游返回 HTTP %d: %s", resp.StatusCode,
			sanitizeKeyLeak(bodySnippet(body), snap.APIKeys))
	}
	return latency, nil
}
