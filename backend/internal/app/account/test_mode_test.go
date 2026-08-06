package account

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/DouDOU-start/airgate-core/internal/relay/accounttesthook"
)

type fakeAccountTestTransformer struct {
	request accounttesthook.Request
	result  accounttesthook.Decision
	err     error
}

func (f *fakeAccountTestTransformer) TransformAccountTest(_ context.Context, request accounttesthook.Request) (accounttesthook.Decision, error) {
	f.request = request
	return f.result, f.err
}

func TestNormalizeTestModeUsesNormalByDefault(t *testing.T) {
	for _, input := range []TestMode{"", "normal", " Normal "} {
		mode, err := normalizeTestMode(input)
		if err != nil || mode != TestModeNormal {
			t.Fatalf("模式 %q 规范化结果 = %q, %v", input, mode, err)
		}
	}
	if _, err := normalizeTestMode("unknown"); err == nil {
		t.Fatal("未知测试模式不应被接受")
	}
}

func TestTransformAccountTestRequestRequiresPlugin(t *testing.T) {
	service := &Service{}
	_, err := service.transformAccountTestRequest(context.Background(), TestModeOverage, "gpt-test", json.RawMessage(`{"model":"gpt-test"}`))
	if !errors.Is(err, accounttesthook.ErrUnavailable) {
		t.Fatalf("未注入插件时错误 = %v，期望 ErrUnavailable", err)
	}
}

func TestTransformAccountTestRequestPassesOverageContext(t *testing.T) {
	transformer := &fakeAccountTestTransformer{result: accounttesthook.Decision{
		Version:     accounttesthook.VersionV1,
		RequestBody: json.RawMessage(`{"model":"gpt-test","input":[{"type":"function_call"}]}`),
	}}
	service := &Service{testTransformer: transformer}
	result, err := service.transformAccountTestRequest(
		context.Background(),
		TestModeOverage,
		"gpt-test",
		json.RawMessage(`{"model":"gpt-test","input":"你好"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if transformer.request.Mode != "overage" || transformer.request.Platform != "codex" || transformer.request.Endpoint != "responses" {
		t.Fatalf("插件请求上下文异常: %+v", transformer.request)
	}
	if len(result) == 0 {
		t.Fatal("插件变换结果为空")
	}
}
