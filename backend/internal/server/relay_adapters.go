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

	models := make(map[string]struct{}, len(key.Models))
	for _, m := range key.Models {
		models[m] = struct{}{}
	}
	snap := &registry.ChannelKeySnapshot{
		KeyID:          key.ID,
		ChannelID:      key.ChannelID,
		BaseURL:        key.BaseURL,
		Type:           key.Type,
		APIKey:         plain,
		Models:         models,
		ModelMapping:   key.ModelMapping,
		ParamOverride:  key.ParamOverride,
		HeaderOverride: key.HeaderOverride,
		CostRatio:      key.CostRatio,
		UpstreamRate:   key.UpstreamRate,
		Status:         key.Status,
		TestModel:      key.TestModel,
	}
	return t.pipe.TestChannel(ctx, snap, model, endpoint)
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
