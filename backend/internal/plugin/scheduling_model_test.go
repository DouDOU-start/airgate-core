package plugin

import (
	"reflect"
	"testing"

	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"
)

func TestSchedulingModelsForOpenAIAnthropicMessages(t *testing.T) {
	clearSchedulingModelEnv(t)

	tests := []struct {
		name  string
		path  string
		model string
		want  []string
	}{
		{
			name:  "opus 使用主模型和降级模型",
			path:  "/v1/messages",
			model: "claude-opus-4-7",
			want:  []string{"gpt-5.5", "gpt-5.4"},
		},
		{
			name:  "sonnet 使用主模型和降级模型",
			path:  "/messages",
			model: "claude-sonnet-4-6",
			want:  []string{"gpt-5.5", "gpt-5.4"},
		},
		{
			name:  "haiku 使用快速模型和降级模型",
			path:  "/v1/messages/count_tokens",
			model: "claude-haiku-4-5",
			want:  []string{"gpt-5.3-codex-spark", "gpt-5.4-mini"},
		},
		{
			name:  "非 Claude 模型保持原样",
			path:  "/v1/messages",
			model: "gpt-5.4",
			want:  []string{"gpt-5.4"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			// mgr=nil 走硬编码回退路径
			got := schedulingModelsForRequest(nil, "openai", "", tt.path, tt.model)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("schedulingModelsForRequest() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestSchedulingModelsFromCatalogMetadata(t *testing.T) {
	clearSchedulingModelEnv(t)

	// 构造含 scheduling_model 元数据的 Manager
	mgr := &Manager{
		modelCache: map[string][]sdk.ModelInfo{
			"test-platform": {
				{
					ID:       "my-model-v1",
					Name:     "My Model V1",
					Metadata: map[string]string{"scheduling_model": "actual-model-v2"},
				},
				{
					ID:   "plain-model",
					Name: "Plain Model",
				},
			},
		},
	}

	// 目录中有 scheduling_model → 直接采纳，不走硬编码
	got := schedulingModelsForRequest(mgr, "any-platform", "test-plugin", "/any/path", "my-model-v1")
	want := []string{"actual-model-v2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("schedulingModelsForRequest(catalog hit) = %#v, want %#v", got, want)
	}

	// 目录中无 scheduling_model → 回退（非 openai 平台直接返回原模型）
	got = schedulingModelsForRequest(mgr, "other", "test-plugin", "/v1/chat", "plain-model")
	want = []string{"plain-model"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("schedulingModelsForRequest(no metadata) = %#v, want %#v", got, want)
	}
}

func TestSchedulingModelsFromDeclaredRouteMap(t *testing.T) {
	clearSchedulingModelEnv(t)

	// 插件在路由 Metadata["scheduling_model_map"] 声明前缀映射表（tech-debt #5 治理路径）
	mgr := &Manager{
		modelCache: map[string][]sdk.ModelInfo{},
		routeCache: map[string][]sdk.RouteDefinition{
			"gateway-openai": {
				{
					Method: "POST",
					Path:   "/v1/messages",
					Metadata: map[string]string{
						"scheduling_model_map": `{"claude-haiku-":["my-fast","my-fast-fallback"],"claude-":["my-main","openai/my-main-fallback"]}`,
					},
				},
			},
		},
	}

	tests := []struct {
		name   string
		plugin string
		path   string
		model  string
		want   []string
	}{
		{
			name:   "最长前缀优先：haiku 命中专属映射",
			plugin: "gateway-openai",
			path:   "/v1/messages",
			model:  "claude-haiku-4-5",
			want:   []string{"my-fast", "my-fast-fallback"},
		},
		{
			name:   "通用前缀兜底：sonnet 命中 claude- 并做 ID 规范化",
			plugin: "gateway-openai",
			path:   "/v1/messages",
			model:  "claude-sonnet-4-6",
			want:   []string{"my-main", "my-main-fallback"},
		},
		{
			name:   "子路径经路由前缀命中声明",
			plugin: "gateway-openai",
			path:   "/v1/messages/count_tokens",
			model:  "claude-opus-4-7",
			want:   []string{"my-main", "my-main-fallback"},
		},
		{
			name:   "声明未命中的模型保持原样",
			plugin: "gateway-openai",
			path:   "/v1/messages",
			model:  "gemini-pro",
			want:   []string{"gemini-pro"},
		},
		{
			name:   "未声明映射的插件走硬编码兜底",
			plugin: "gateway-other",
			path:   "/v1/messages",
			model:  "claude-opus-4-7",
			want:   []string{"gpt-5.5", "gpt-5.4"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got := schedulingModelsForRequest(mgr, "openai", tt.plugin, tt.path, tt.model)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("schedulingModelsForRequest() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestParseSchedulingModelMapInvalidJSON(t *testing.T) {
	if got := parseSchedulingModelMap("{not-json"); got != nil {
		t.Fatalf("parseSchedulingModelMap(invalid) = %#v, want nil", got)
	}
	if got := parseSchedulingModelMap(""); got != nil {
		t.Fatalf("parseSchedulingModelMap(empty) = %#v, want nil", got)
	}
}

func TestSchedulingModelsForOpenAIAnthropicMessagesUsesEnvOverride(t *testing.T) {
	clearSchedulingModelEnv(t)
	t.Setenv("AIRGATE_MODEL_OPUS", "openai/gpt-5.4")
	t.Setenv("AIRGATE_MODEL_OPUS_FALLBACK", "oai/gpt-5.4")

	got := schedulingModelsForRequest(nil, "openai", "", "/v1/messages", "claude-opus-4-7")
	want := []string{"gpt-5.4"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("schedulingModelsForRequest() = %#v, want %#v", got, want)
	}
}

func TestSchedulingModelsIgnoreNonAnthropicRoutes(t *testing.T) {
	clearSchedulingModelEnv(t)

	got := schedulingModelsForRequest(nil, "openai", "", "/v1/chat/completions", "claude-opus-4-7")
	want := []string{"claude-opus-4-7"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("schedulingModelsForRequest() = %#v, want %#v", got, want)
	}
}

func clearSchedulingModelEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"AIRGATE_DEFAULT_CLAUDE_MODEL",
		"AIRGATE_MODEL_OPUS",
		"ANTHROPIC_DEFAULT_OPUS_MODEL",
		"AIRGATE_MODEL_SONNET",
		"ANTHROPIC_DEFAULT_SONNET_MODEL",
		"AIRGATE_MODEL_HAIKU",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL",
		"AIRGATE_MODEL_HAIKU_FALLBACK",
		"AIRGATE_MODEL_OPUS_FALLBACK",
		"AIRGATE_MODEL_SONNET_FALLBACK",
		"AIRGATE_MODEL_DEFAULT_FALLBACK",
	} {
		t.Setenv(key, "")
	}
}
