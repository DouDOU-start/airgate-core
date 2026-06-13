package plugin

import (
	"net/url"
	"os"
	"strings"
)

// schedulingModelsForRequest 返回调度层使用的模型候选列表。
//
// 查询顺序（声明优先，兜底保持向后兼容）：
//  1. 插件模型目录的 Metadata["scheduling_model"]：目录内模型的精确 ID 映射。
//  2. 插件路由声明的 Metadata["scheduling_model_map"]：前缀映射表，覆盖不在目录中的
//     协议翻译入口模型（如 openai 插件 /v1/messages 收到的 claude-*，由客户端传入）。
//  3. 历史硬编码映射：仅当插件未声明时生效（老版本插件兼容），勿再扩展。
func schedulingModelsForRequest(mgr *Manager, platform, pluginName, path, requestedModel string) []string {
	// 1. 插件模型目录的调度模型元数据（精确 ID）
	if mgr != nil {
		if sm := mgr.SchedulingModel(requestedModel); sm != "" {
			return compactUniqueModels(sm)
		}
		// 2. 插件路由声明的前缀映射表
		if pluginName != "" {
			if mapped := schedulingModelsFromDeclaredMap(mgr.SchedulingModelMap(pluginName, path), requestedModel); len(mapped) > 0 {
				return mapped
			}
		}
	}

	// 3. 兜底：硬编码映射（仅 OpenAI 插件 /v1/messages Anthropic 协议翻译入口）
	if !strings.EqualFold(strings.TrimSpace(platform), "openai") || !isAnthropicMessagesForwardPath(path) {
		return compactUniqueModels(requestedModel)
	}
	return openAIAnthropicSchedulingModels(requestedModel)
}

// schedulingModelsFromDeclaredMap 在插件声明的前缀映射表中查找请求模型。
// 键为模型 ID 前缀（允许尾部 "*"，匹配时忽略），大小写不敏感，最长前缀优先。
func schedulingModelsFromDeclaredMap(prefixMap map[string][]string, requestedModel string) []string {
	model := strings.ToLower(strings.TrimSpace(requestedModel))
	if model == "" || len(prefixMap) == 0 {
		return nil
	}
	bestLen := -1
	var bestModels []string
	for key, models := range prefixMap {
		prefix := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(key), "*"))
		if prefix == "" || !strings.HasPrefix(model, prefix) {
			continue
		}
		if len(prefix) > bestLen {
			bestLen = len(prefix)
			bestModels = models
		}
	}
	if bestLen < 0 {
		return nil
	}
	normalized := make([]string, 0, len(bestModels))
	for _, m := range bestModels {
		normalized = append(normalized, normalizeMappedModelID(m, ""))
	}
	return compactUniqueModels(normalized...)
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
