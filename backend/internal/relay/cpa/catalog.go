package cpa

import (
	_ "embed"
	"encoding/json"
	"strings"
	"sync"

	"github.com/DouDOU-start/airgate-core/internal/relay/cursor"
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
		// CLIProxyAPI 会在静态 models.json 之外注入 xAI 媒体模型；这里同步
		// 该行为，确保账号注册表能按 Grok Build 实际发送的模型名选中账号。
		list = withXAIMediaBuiltins(cat.XAI)
	case "cursor":
		list = cursorModelInfos()
	default:
		return nil
	}
	return cloneModelInfos(list)
}

// withXAIMediaBuiltins 注入 CLIProxyAPI 的 xAI 内建媒体模型。
// 这些模型不在上游静态 models.json 中，不能依赖目录文件更新。
func withXAIMediaBuiltins(models []ModelInfo) []ModelInfo {
	out := append([]ModelInfo(nil), models...)
	out = append(out,
		ModelInfo{ID: "grok-imagine-image", DisplayName: "Grok Imagine 生图", OwnedBy: "xai", Type: "xai"},
		ModelInfo{ID: "grok-imagine-image-quality", DisplayName: "Grok Imagine 高质量生图", OwnedBy: "xai", Type: "xai"},
		ModelInfo{ID: "grok-imagine-image-2.0", DisplayName: "Grok Imagine 生图 2.0", OwnedBy: "xai", Type: "xai"},
		ModelInfo{ID: "grok-imagine-video", DisplayName: "Grok Imagine 视频", OwnedBy: "xai", Type: "xai"},
		ModelInfo{ID: "grok-imagine-video-1.5", DisplayName: "Grok Imagine 视频 1.5", OwnedBy: "xai", Type: "xai"},
		ModelInfo{ID: "grok-imagine-video-1.5-preview", DisplayName: "Grok Imagine 视频 1.5 预览版", OwnedBy: "xai", Type: "xai"},
	)
	return out
}

// cursorModelInfos 把 cursor 包内置目录转成通用 ModelInfo，并追加裸基础
// 别名（如 claude-fable-5）：Cursor 档位编码在 id 后缀，下游常按基础名请求，
// 别名保证账号注册表可按基础名选中账号，实际档位由 executor 侧解析。
func cursorModelInfos() []ModelInfo {
	models := cursor.Models()
	aliases := cursor.BaseAliases()
	out := make([]ModelInfo, 0, len(models)+len(aliases))
	for _, m := range models {
		out = append(out, ModelInfo{
			ID:          m.ID,
			DisplayName: m.Name,
			OwnedBy:     "cursor",
			Type:        "cursor",
		})
	}
	for _, base := range aliases {
		out = append(out, ModelInfo{
			ID:          base,
			DisplayName: base,
			OwnedBy:     "cursor",
			Type:        "cursor",
		})
	}
	return out
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
