package cursor

import (
	_ "embed"
	"encoding/json"
	"sort"
	"strings"
	"sync"
)

//go:embed embed/models.json
var modelsJSON []byte

// CatalogModel 是 Cursor 暴露的一个可用模型（静态回退目录）。
// Cursor 为订阅制，成本按订阅额度结算而非 token，故此处不含单价。
type CatalogModel struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ContextWindow int    `json:"context_window"`
	MaxTokens     int    `json:"max_tokens"`
	Reasoning     bool   `json:"reasoning"`
	Vision        bool   `json:"vision"`
}

var (
	modelsOnce sync.Once
	modelsData []CatalogModel
	modelsErr  error
)

func loadModels() ([]CatalogModel, error) {
	modelsOnce.Do(func() {
		modelsErr = json.Unmarshal(modelsJSON, &modelsData)
	})
	return modelsData, modelsErr
}

// Models 返回内置的 Cursor 模型静态目录。这是账号创建 / 模型广场的回退来源；
// 真实可用模型可另经 GetUsableModels 动态发现（需账号 token）。
func Models() []CatalogModel {
	models, err := loadModels()
	if err != nil {
		return nil
	}
	out := make([]CatalogModel, len(models))
	copy(out, models)
	return out
}

// thinkingLevels 是 Cursor 模型 id 后缀可表达的思考档位，从弱到强排序。
// Cursor 协议本身没有思考层级字段（ThinkingDetails 为空消息），档位编码在
// 模型 id 后缀里；"-max" 后缀是 Max Mode（额度/上下文维度），不参与档位映射。
var thinkingLevels = []string{"minimal", "low", "medium", "high", "xhigh"}

func levelIndex(level string) int {
	for i, lv := range thinkingLevels {
		if lv == level {
			return i
		}
	}
	return -1
}

var (
	catalogIDsOnce sync.Once
	catalogIDs     map[string]bool
)

func catalogIDSet() map[string]bool {
	catalogIDsOnce.Do(func() {
		catalogIDs = make(map[string]bool)
		for _, m := range Models() {
			catalogIDs[m.ID] = true
		}
	})
	return catalogIDs
}

// stripLevelSuffix 剥离 id 尾部的思考档位后缀，返回基础 id 与是否剥离。
func stripLevelSuffix(id string) (string, bool) {
	for _, lv := range thinkingLevels {
		if strings.HasSuffix(id, "-"+lv) {
			return strings.TrimSuffix(id, "-"+lv), true
		}
	}
	return id, false
}

var (
	baseAliasOnce sync.Once
	baseAliases   []string
)

// BaseAliases 返回目录中带档位变体的基础模型 id 列表（去重、字典序），
// 排除本身就是真实目录条目的 id。用于对外暴露"裸基础名"路由别名，
// 让下游（如 Claude Code）可直接传 claude-fable-5 而不必带档位后缀。
func BaseAliases() []string {
	baseAliasOnce.Do(func() {
		ids := catalogIDSet()
		seen := map[string]bool{}
		for id := range ids {
			base, stripped := stripLevelSuffix(id)
			if !stripped || ids[base] || seen[base] {
				continue
			}
			seen[base] = true
			baseAliases = append(baseAliases, base)
		}
		sort.Strings(baseAliases)
	})
	out := make([]string, len(baseAliases))
	copy(out, baseAliases)
	return out
}

// ResolveWireModel 解析实际上行 Cursor 的模型 id：显式档位优先按档位改写；
// 未指定档位时，目录内 id 原样透传，目录外（如裸基础别名 claude-fable-5）
// 落到 medium 档就近变体，避免把上游不认识的 id 直接发出去。
func ResolveWireModel(model, effort string) string {
	if effort != "" {
		return ApplyThinkingLevel(model, effort)
	}
	if catalogIDSet()[model] {
		return model
	}
	return ApplyThinkingLevel(model, "medium")
}

// ApplyThinkingLevel 把标准协议归一出的思考档位映射为 Cursor 模型 id 变体：
// 剥离 model 已带的档位后缀得到基础 id，优先取 base-effort 精确档位；目录中
// 不存在时在同一基础 id 的可用档位里就近取档（距离相同偏向低档）。effort 为空
// 或找不到任何变体时原样返回 model。
func ApplyThinkingLevel(model, effort string) string {
	target := levelIndex(effort)
	if model == "" || target < 0 {
		return model
	}
	ids := catalogIDSet()
	base, _ := stripLevelSuffix(model)
	if exact := base + "-" + effort; ids[exact] {
		return exact
	}
	best := ""
	bestDist := len(thinkingLevels) + 1
	for i, lv := range thinkingLevels {
		cand := base + "-" + lv
		if !ids[cand] {
			continue
		}
		// 升序遍历下严格小于即保留，天然实现"距离相同偏向低档"。
		if d := absInt(i - target); d < bestDist {
			best, bestDist = cand, d
		}
	}
	if best != "" {
		return best
	}
	if ids[base] {
		return base
	}
	return model
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
