package gemini

import "github.com/DouDOU-start/airgate-core/internal/relay/dto"

// geminiUsage Gemini usageMetadata。
// 计数语义：promptTokenCount 为主提示 token 数（已含 cachedContentTokenCount），
// toolUsePromptTokenCount 是额外的工具调用提示，两者相加才是完整输入。
// thoughtsTokenCount 为思考 token（Gemini 2.5 默认开启），不含在 candidatesTokenCount，
// 但上游按输出价计费——必须并入 CompletionTokens，否则思考 token 系统性漏计费。
type geminiUsage struct {
	PromptTokenCount        int `json:"promptTokenCount"`
	CandidatesTokenCount    int `json:"candidatesTokenCount"`
	ThoughtsTokenCount      int `json:"thoughtsTokenCount"`
	CachedContentTokenCount int `json:"cachedContentTokenCount"`
	ToolUsePromptTokenCount int `json:"toolUsePromptTokenCount"`
	TotalTokenCount         int `json:"totalTokenCount"`
}

// outputTokens 输出 token = 正文 + 思考（均按输出价计费）。
func (u geminiUsage) outputTokens() int {
	tokens := addTokenCounts(u.CandidatesTokenCount, u.ThoughtsTokenCount)
	fromTotal := nonNegativeTokenCount(u.TotalTokenCount) -
		addTokenCounts(u.PromptTokenCount, u.ToolUsePromptTokenCount)
	if fromTotal > tokens {
		tokens = fromTotal
	}
	return tokens
}

// merge 合并流式分片中的累计用量。个别兼容上游会把输入、输出和缓存明细
// 分散到不同帧；逐字段取最大值可保留已出现的数据，并避免累计值重复相加。
func (u geminiUsage) merge(other geminiUsage) geminiUsage {
	max := func(a, b int) int {
		if b > a {
			return b
		}
		return a
	}
	return geminiUsage{
		PromptTokenCount:        max(u.PromptTokenCount, other.PromptTokenCount),
		CandidatesTokenCount:    max(u.CandidatesTokenCount, other.CandidatesTokenCount),
		ThoughtsTokenCount:      max(u.ThoughtsTokenCount, other.ThoughtsTokenCount),
		CachedContentTokenCount: max(u.CachedContentTokenCount, other.CachedContentTokenCount),
		ToolUsePromptTokenCount: max(u.ToolUsePromptTokenCount, other.ToolUsePromptTokenCount),
		TotalTokenCount:         max(u.TotalTokenCount, other.TotalTokenCount),
	}
}

// dtoUsage 转 billing 用的 dto.Usage。Gemini 无缓存写入分档，双档缓存写恒 0。
func (u geminiUsage) dtoUsage() dto.Usage {
	return dto.Usage{
		PromptTokens:     addTokenCounts(u.PromptTokenCount, u.ToolUsePromptTokenCount),
		CompletionTokens: u.outputTokens(),
		CachedTokens:     nonNegativeTokenCount(u.CachedContentTokenCount),
	}
}

func nonNegativeTokenCount(value int) int {
	if value < 0 {
		return 0
	}
	return value
}

func addTokenCounts(values ...int) int {
	maxInt := int(^uint(0) >> 1)
	total := 0
	for _, value := range values {
		value = nonNegativeTokenCount(value)
		if value > maxInt-total {
			return maxInt
		}
		total += value
	}
	return total
}

// empty 判断 usageMetadata 是否为空（流式中间 chunk 常无 usage）。
func (u geminiUsage) empty() bool {
	return u.PromptTokenCount == 0 && u.CandidatesTokenCount == 0 &&
		u.ThoughtsTokenCount == 0 && u.CachedContentTokenCount == 0 &&
		u.ToolUsePromptTokenCount == 0 && u.TotalTokenCount == 0
}
