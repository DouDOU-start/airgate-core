package plugin

import (
	"net/url"
	"os"
	"strings"
)

// schedulingModelsForRequest 返回调度层使用的模型候选列表。
//
// 优先查询插件模型目录的 Metadata["scheduling_model"]：插件可在 Models() 声明中
// 标注"本模型调度时应映射为哪个模型"，Core 直接采纳，无需硬编码。
//
// 若目录中未找到（例如 Claude 模型不在 OpenAI 插件的 Models() 中——它们是 Anthropic
// 协议翻译入口的模型，由客户端传入而非插件声明），则回退到硬编码映射以保持向后兼容。
// TODO(tech-debt): 当所有跨协议模型均通过 Metadata["scheduling_model"] 声明后，
// 可移除 openAIAnthropicSchedulingModels 硬编码回退。
func schedulingModelsForRequest(mgr *Manager, platform, path, requestedModel string) []string {
	// 优先查询插件模型目录的调度模型元数据
	if mgr != nil {
		if sm := mgr.SchedulingModel(requestedModel); sm != "" {
			return compactUniqueModels(sm)
		}
	}

	// 回退：硬编码映射（仅 OpenAI 插件 /v1/messages Anthropic 协议翻译入口）
	if !strings.EqualFold(strings.TrimSpace(platform), "openai") || !isAnthropicMessagesForwardPath(path) {
		return compactUniqueModels(requestedModel)
	}
	return openAIAnthropicSchedulingModels(requestedModel)
}

func isAnthropicMessagesForwardPath(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	if u, err := url.Parse(path); err == nil && u != nil {
		path = u.Path
	} else if idx := strings.IndexByte(path, '?'); idx >= 0 {
		path = path[:idx]
	}
	return pathHasAPIPrefix(path, "/v1/messages") || pathHasAPIPrefix(path, "/messages")
}

func pathHasAPIPrefix(path, prefix string) bool {
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	rest := path[len(prefix):]
	return rest == "" || rest[0] == '/'
}

func openAIAnthropicSchedulingModels(requestedModel string) []string {
	model := strings.ToLower(strings.TrimSpace(requestedModel))
	if model == "" {
		return compactUniqueModels(requestedModel)
	}

	defaultTarget := normalizedEnvModel("gpt-5.5", "AIRGATE_DEFAULT_CLAUDE_MODEL")
	switch {
	case strings.HasPrefix(model, "claude-haiku-"):
		return compactUniqueModels(
			normalizedEnvModel("gpt-5.3-codex-spark", "AIRGATE_MODEL_HAIKU", "ANTHROPIC_DEFAULT_HAIKU_MODEL"),
			normalizedEnvModel("gpt-5.4-mini", "AIRGATE_MODEL_HAIKU_FALLBACK"),
		)
	case strings.HasPrefix(model, "claude-sonnet-"):
		return compactUniqueModels(
			normalizedEnvModel(defaultTarget, "AIRGATE_MODEL_SONNET", "ANTHROPIC_DEFAULT_SONNET_MODEL"),
			normalizedEnvModel("gpt-5.4", "AIRGATE_MODEL_SONNET_FALLBACK"),
		)
	case strings.HasPrefix(model, "claude-opus-"):
		return compactUniqueModels(
			normalizedEnvModel(defaultTarget, "AIRGATE_MODEL_OPUS", "ANTHROPIC_DEFAULT_OPUS_MODEL"),
			normalizedEnvModel("gpt-5.4", "AIRGATE_MODEL_OPUS_FALLBACK"),
		)
	case strings.HasPrefix(model, "claude-3") || strings.HasPrefix(model, "claude-"):
		return compactUniqueModels(
			defaultTarget,
			normalizedEnvModel("gpt-5.4", "AIRGATE_MODEL_DEFAULT_FALLBACK"),
		)
	default:
		return compactUniqueModels(requestedModel)
	}
}

func normalizedEnvModel(fallback string, keys ...string) string {
	for _, key := range keys {
		if value := normalizeMappedModelID(os.Getenv(key), ""); value != "" {
			return value
		}
	}
	return normalizeMappedModelID(fallback, fallback)
}

func normalizeMappedModelID(raw, fallback string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return fallback
	}
	if idx := strings.LastIndex(value, "@"); idx >= 0 && idx+1 < len(value) {
		value = strings.TrimSpace(value[idx+1:])
	}
	value = strings.TrimPrefix(value, "openai/")
	value = strings.TrimPrefix(value, "oai/")
	if value == "" {
		return fallback
	}
	return value
}

func compactUniqueModels(models ...string) []string {
	out := make([]string, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		key := strings.ToLower(model)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, model)
	}
	return out
}
