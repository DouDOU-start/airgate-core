package cpa

import (
	_ "embed"
	"encoding/json"
	"strings"
	"sync"
)

//go:embed embed/models.json
var cpaModelsJSON []byte

// ModelInfo 静态模型元数据（对齐 CPA registry.ModelInfo 常用字段）。
type ModelInfo struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name,omitempty"`
	OwnedBy     string `json:"owned_by,omitempty"`
	Type        string `json:"type,omitempty"`
}

// cpaCatalog 对应 CLIProxyAPI internal/registry/models/models.json。
// 来源见 embed/SOURCE.txt；升级 CPA 依赖后请同步该文件。
type cpaCatalog struct {
	Claude      []ModelInfo `json:"claude"`
	Gemini      []ModelInfo `json:"gemini"`
	Vertex      []ModelInfo `json:"vertex"`
	AIStudio    []ModelInfo `json:"aistudio"`
	GeminiCLI   []ModelInfo `json:"gemini-cli"`
	CodexFree   []ModelInfo `json:"codex-free"`
	CodexTeam   []ModelInfo `json:"codex-team"`
	CodexPlus   []ModelInfo `json:"codex-plus"`
	CodexPro    []ModelInfo `json:"codex-pro"`
	Kimi        []ModelInfo `json:"kimi"`
	Antigravity []ModelInfo `json:"antigravity"`
	XAI         []ModelInfo `json:"xai"`
}

var (
	catalogOnce sync.Once
	catalogData cpaCatalog
	catalogErr  error
)

func loadCatalog() cpaCatalog {
	catalogOnce.Do(func() {
		if err := json.Unmarshal(cpaModelsJSON, &catalogData); err != nil {
			catalogErr = err
			catalogData = cpaCatalog{}
		}
	})
	return catalogData
}

// CatalogLoadError 返回 models.json 解析错误（正常应 nil）。
func CatalogLoadError() error {
	loadCatalog()
	return catalogErr
}

// DefaultModelInfos 返回 platform 的 CPA 静态模型目录（含 display_name）。
// planType 仅对 codex 生效（free/plus/team|business|go/pro，默认 pro）。
func DefaultModelInfos(platform, planType string) []ModelInfo {
	cat := loadCatalog()
	provider := ResolveProvider(platform)
	var list []ModelInfo
	switch provider {
	case "codex":
		list = codexModelsByPlan(cat, planType)
	case "claude":
		list = cat.Claude
	case "gemini":
		list = cat.Gemini
	case "vertex":
		list = cat.Vertex
	case "aistudio":
		// aistudio 优先；空则回落 gemini-cli
		if len(cat.AIStudio) > 0 {
			list = cat.AIStudio
		} else {
			list = cat.GeminiCLI
		}
	case "antigravity":
		list = cat.Antigravity
	case "kimi":
		list = cat.Kimi
	case "xai":
		list = cat.XAI
	default:
		return nil
	}
	return cloneModelInfos(list)
}

// codexModelsByPlan 对齐 CPA sdk/cliproxy/service_models.go OAuth 分档。
func codexModelsByPlan(cat cpaCatalog, planType string) []ModelInfo {
	switch strings.ToLower(strings.TrimSpace(planType)) {
	case "free":
		return cat.CodexFree
	case "plus":
		return cat.CodexPlus
	case "team", "business", "go":
		return cat.CodexTeam
	case "pro", "prolite", "pro_lite", "pro-lite", "max", "":
		// 默认 pro：与 CPA default 分支一致
		if len(cat.CodexPro) > 0 {
			return cat.CodexPro
		}
		return cat.CodexPlus
	default:
		if len(cat.CodexPro) > 0 {
			return cat.CodexPro
		}
		return cat.CodexPlus
	}
}

func cloneModelInfos(in []ModelInfo) []ModelInfo {
	if len(in) == 0 {
		return nil
	}
	out := make([]ModelInfo, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, m := range in {
		id := strings.TrimSpace(m.ID)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		cp := m
		cp.ID = id
		if strings.TrimSpace(cp.DisplayName) == "" {
			cp.DisplayName = id
		}
		out = append(out, cp)
	}
	return out
}

// ModelIDs 从 ModelInfo 列表提取 ID。
func ModelIDs(models []ModelInfo) []string {
	if len(models) == 0 {
		return nil
	}
	out := make([]string, 0, len(models))
	for _, m := range models {
		if m.ID != "" {
			out = append(out, m.ID)
		}
	}
	return out
}
