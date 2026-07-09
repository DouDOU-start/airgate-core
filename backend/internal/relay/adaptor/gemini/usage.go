package gemini

import "github.com/DouDOU-start/airgate-core/internal/relay/dto"

// geminiUsage Gemini usageMetadata。
// 计数语义：promptTokenCount 为完整输入 token 数（已含 cachedContentTokenCount），
// 与 dto.Usage 的 PromptTokens 口径一致，无需归一化。
// thoughtsTokenCount 为思考 token（Gemini 2.5 默认开启），不含在 candidatesTokenCount，
// 但上游按输出价计费——必须并入 CompletionTokens，否则思考 token 系统性漏计费。
type geminiUsage struct {
	PromptTokenCount        int `json:"promptTokenCount"`
	CandidatesTokenCount    int `json:"candidatesTokenCount"`
	ThoughtsTokenCount      int `json:"thoughtsTokenCount"`
	CachedContentTokenCount int `json:"cachedContentTokenCount"`
	TotalTokenCount         int `json:"totalTokenCount"`
}

// outputTokens 输出 token = 正文 + 思考（均按输出价计费）。
func (u geminiUsage) outputTokens() int {
	return u.CandidatesTokenCount + u.ThoughtsTokenCount
}

// dtoUsage 转 billing 用的 dto.Usage。Gemini 无缓存写入分档，双档缓存写恒 0。
func (u geminiUsage) dtoUsage() dto.Usage {
	return dto.Usage{
		PromptTokens:     u.PromptTokenCount,
		CompletionTokens: u.outputTokens(),
		CachedTokens:     u.CachedContentTokenCount,
	}
}

// empty 判断 usageMetadata 是否为空（流式中间 chunk 常无 usage）。
func (u geminiUsage) empty() bool {
	return u.PromptTokenCount == 0 && u.CandidatesTokenCount == 0 &&
		u.ThoughtsTokenCount == 0 && u.TotalTokenCount == 0
}
