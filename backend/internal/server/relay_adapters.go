package server

import (
	"context"
	"errors"

	appchannel "github.com/DouDOU-start/airgate-core/internal/app/channel"
	appsettings "github.com/DouDOU-start/airgate-core/internal/app/settings"
	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/relay/pipeline"
	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
)

// channelTester 实现 appchannel.Tester：把领域对象转成解密后的运行时快照，
// 交 relay 管线走完整 adaptor 链路发起测试请求。
// 测试结果落库（response_time_ms/tested_at）与 disabled_auto 恢复由 channel service 编排。
type channelTester struct {
	pipe   *pipeline.Pipeline
	secret string
}

// Test 实现 appchannel.Tester。endpoint 仅对 openai 协议 key 生效（空值默认 chat completions）。
func (t *channelTester) Test(ctx context.Context, key appchannel.ChannelKey, model, endpoint string) (int, error) {
	plain, err := auth.DecryptAPIKey(key.APIKey, t.secret)
	if err != nil {
		return 0, errors.New("密钥端点无可解密的 API Key")
	}

	snap := channelTestSnapshot(key, plain)
	return t.pipe.TestChannel(ctx, snap, model, endpoint)
}

// channelTestSnapshot 将数据库中的渠道密钥转换为测试请求使用的运行时快照。
// 渠道名称必须随 ID 一并传递，否则失败留痕只能在前端回退显示渠道 ID。
func channelTestSnapshot(key appchannel.ChannelKey, plain string) *registry.ChannelKeySnapshot {
	return &registry.ChannelKeySnapshot{
		KeyID:          key.ID,
		KeyName:        key.Name,
		ChannelID:      key.ChannelID,
		ChannelName:    key.ChannelName,
		BaseURL:        key.BaseURL,
		Type:           key.Type,
		APIKey:         plain,
		Models:         channelTestModelSet(key.Models),
		ModelMapping:   key.ModelMapping,
		ParamOverride:  key.ParamOverride,
		HeaderOverride: key.HeaderOverride,
		CostRatio:      key.CostRatio,
		UpstreamRate:   key.UpstreamRate,
		Status:         key.Status,
		TestModel:      key.TestModel,
	}
}

func channelTestModelSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

// gatewaySettingsSource 把 appsettings.Service 适配为 pipeline.SettingsLister。
type gatewaySettingsSource struct {
	svc *appsettings.Service
}

// List 实现 pipeline.SettingsLister。
func (g gatewaySettingsSource) List(ctx context.Context, group string) ([]pipeline.Setting, error) {
	items, err := g.svc.List(ctx, group)
	if err != nil {
		return nil, err
	}
	result := make([]pipeline.Setting, len(items))
	for i, item := range items {
		result[i] = pipeline.Setting{Key: item.Key, Value: item.Value}
	}
	return result, nil
}
