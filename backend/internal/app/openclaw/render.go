package openclaw

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ModelPreset 对应 openclaw.models_preset 数组中的单个元素。
//
// 字段语义：
//   - ID / API：写入 openclaw.json 的核心键；label 则同时当作展示名和 alias。
//   - Reasoning / Input：可选能力标记，用于前端/脚本展示，并原样写回 provider.models。
type ModelPreset struct {
	ID        string   `json:"id"`
	Label     string   `json:"label,omitempty"`
	API       string   `json:"api"`
	Reasoning bool     `json:"reasoning,omitempty"`
	Input     []string `json:"input,omitempty"`
}

// ParsedModels 解析 Config 里嵌入的 models_preset JSON 文本。
// 返回错误时意味着管理员配置的 JSON 无效，调用方（handler）应回 500 并提示去管理面板修复。
func (c Config) ParsedModels() ([]ModelPreset, error) {
	var models []ModelPreset
	if err := json.Unmarshal([]byte(c.ModelsPresetJSON), &models); err != nil {
		return nil, err
	}
	return models, nil
}

// BuildModelsText 把 models_preset 压成 pipe 分隔的纯文本，供 install.sh 里的 bash
// 直接 `while IFS='|' read` 解析，避免在客户端依赖 python3/jq。
//
// 每行格式：`<idx>|<id>|<label>|<api>|<caps>`
//   - idx   ：1-based 序号，与用户在交互提示里看到的编号一致
//   - label ：为空时用 id 兜底，确保第三列非空
//   - caps  ：空格分隔的能力标记（当前支持 reasoning/image），可为空
//
// label 中若包含 `|` 会破坏 shell 的 IFS 切分 —— 这里直接替换成空格，不做转义，
// 因为 label 本身是给人类看的展示名，不影响后续逻辑（id 才是唯一键）。
func (s *Service) BuildModelsText(cfg Config) (string, error) {
	models, err := cfg.ParsedModels()
	if err != nil {
		return "", fmt.Errorf("parse models_preset: %w", err)
	}
	var b strings.Builder
	for i, m := range models {
		label := m.Label
		if label == "" {
			label = m.ID
		}
		label = strings.ReplaceAll(label, "|", " ")

		caps := make([]string, 0, 2)
		if m.Reasoning {
			caps = append(caps, "reasoning")
		}
		for _, in := range m.Input {
			if in == "image" {
				caps = append(caps, "image")
				break
			}
		}

		fmt.Fprintf(&b, "%d|%s|%s|%s|%s\n", i+1, m.ID, label, m.API, strings.Join(caps, " "))
	}
	return b.String(), nil
}

// RenderConfigRequest 是 POST /openclaw/render-config 的入参结构。
//
// 设计要点：
//   - 只接受模型 ID 列表，不接受 1-based 编号 —— 编号的语义随 models_preset 顺序变化，
//     如果客户端缓存了旧编号就会张冠李戴；用 ID 更稳健。
//   - APIKey 由客户端明文传入，服务端只做 JSON 序列化回写 openclaw.json，不落库。
type RenderConfigRequest struct {
	APIKey      string   `json:"api_key"`
	SelectedIDs []string `json:"selected_ids"`
}

// BuildOpenClawConfig 根据 preset 元数据 + 用户选择 + API Key 渲染完整的 openclaw.json。
//
// 这段逻辑之前是在 install.sh 里用内嵌 python3 脚本做的，现在搬到服务端：
//   - 消除 install.sh 对 python3 的依赖（macOS 默认没有 python3）
//   - 让 JSON 转义由 encoding/json 保证，避免 shell 拼字符串时的注入风险
//   - 以后 openclaw.json schema 变化时只需升级 core，无需客户端重新拉脚本
//
// 返回的字节流直接就是可写进 ~/.openclaw/openclaw.json 的内容（2 空格缩进 + 末尾换行）。
func (s *Service) BuildOpenClawConfig(cfg Config, req RenderConfigRequest) ([]byte, error) {
	apiKey := strings.TrimSpace(req.APIKey)
	if apiKey == "" {
		return nil, fmt.Errorf("api_key is required")
	}
	if len(req.SelectedIDs) == 0 {
		return nil, fmt.Errorf("selected_ids is required")
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, fmt.Errorf("base_url is not configured")
	}

	allModels, err := cfg.ParsedModels()
	if err != nil {
		return nil, fmt.Errorf("parse models_preset: %w", err)
	}

	byID := make(map[string]ModelPreset, len(allModels))
	for _, m := range allModels {
		byID[m.ID] = m
	}

	// 按输入顺序保留用户选择；去重但不乱序，因为第一项会被当作 primary 模型。
	picked := make([]ModelPreset, 0, len(req.SelectedIDs))
	seen := make(map[string]bool, len(req.SelectedIDs))
	for _, id := range req.SelectedIDs {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		m, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("unknown model id: %s", id)
		}
		picked = append(picked, m)
		seen[id] = true
	}
	if len(picked) == 0 {
		return nil, fmt.Errorf("no valid model selected")
	}

	provider := strings.TrimSpace(cfg.ProviderName)
	if provider == "" {
		provider = DefaultProviderName
	}
	gatewayMode := strings.TrimSpace(cfg.GatewayMode)
	if gatewayMode == "" {
		gatewayMode = DefaultGatewayMode
	}
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	v1BaseURL := fmt.Sprintf("%s/v1", baseURL)

	providerModels := make([]map[string]any, 0, len(picked))
	aliasMap := make(map[string]map[string]string, len(picked))
	for _, m := range picked {
		name := m.Label
		if name == "" {
			name = m.ID
		}
		item := map[string]any{
			"id":   m.ID,
			"name": name,
			"api":  m.API,
		}
		if m.Reasoning {
			item["reasoning"] = true
		}
		if len(m.Input) > 0 {
			item["input"] = m.Input
		}
		providerModels = append(providerModels, item)
		aliasMap[fmt.Sprintf("%s/%s", provider, m.ID)] = map[string]string{"alias": name}
	}

	primaryRef := fmt.Sprintf("%s/%s", provider, picked[0].ID)

	defaults := map[string]any{
		"model":      map[string]any{"primary": primaryRef},
		"models":     aliasMap,
		"imageModel": map[string]any{"primary": primaryRef},
	}
	if cfg.MemorySearchEnabled {
		memModel := strings.TrimSpace(cfg.MemorySearchModel)
		if memModel == "" {
			memModel = DefaultMemorySearchModel
		}
		defaults["memorySearch"] = map[string]any{
			"enabled":  true,
			"provider": "openai",
			"model":    memModel,
			"remote": map[string]any{
				"baseUrl": v1BaseURL,
				"apiKey":  apiKey,
			},
		}
	}

	out := map[string]any{
		"gateway": map[string]any{"mode": gatewayMode},
		"models": map[string]any{
			"mode": "merge",
			"providers": map[string]any{
				provider: map[string]any{
					"baseUrl": v1BaseURL,
					"apiKey":  apiKey,
					"models":  providerModels,
				},
			},
		},
		"agents": map[string]any{
			"defaults": defaults,
		},
	}

	buf, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal openclaw.json: %w", err)
	}
	buf = append(buf, '\n')
	return buf, nil
}
