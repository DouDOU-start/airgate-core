package anthropic

import "github.com/DouDOU-start/airgate-core/internal/relay/dto"

// anthropicUsage Anthropic usage 对象（message 响应 / message_start / message_delta）。
// 计数语义：input_tokens 为「新鲜」输入，不含缓存读/写；缓存读写另计。
type anthropicUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheCreation            *struct {
		Ephemeral5mInputTokens int `json:"ephemeral_5m_input_tokens"`
		Ephemeral1hInputTokens int `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
}

// normUsage 归一化后的 usage（OpenAI 口径：prompt_tokens 含缓存读）。
type normUsage struct {
	PromptTokens     int // = input_tokens + cache_read（含缓存读，符合 billing 口径）
	CompletionTokens int
	CachedTokens     int // = cache_read_input_tokens
	CacheCreation    int // = cache_creation_input_tokens（泛化总量）
	CacheCreation5m  int
	CacheCreation1h  int
}

// normalizeUsage 把 Anthropic usage 归一为 OpenAI 口径。
// 关键：Anthropic input_tokens 不含缓存读，而 billing 的 ComputeCosts 按
// promptTokens = PromptTokens - CachedTokens 拆「纯输入」，故 PromptTokens 必须含缓存读，
// 否则纯输入被低估、缓存读被漏计。
func normalizeUsage(u anthropicUsage) normUsage {
	n := normUsage{
		PromptTokens:     u.InputTokens + u.CacheReadInputTokens,
		CompletionTokens: u.OutputTokens,
		CachedTokens:     u.CacheReadInputTokens,
		CacheCreation:    u.CacheCreationInputTokens,
	}
	if u.CacheCreation != nil {
		n.CacheCreation5m = u.CacheCreation.Ephemeral5mInputTokens
		n.CacheCreation1h = u.CacheCreation.Ephemeral1hInputTokens
	}
	return n
}

// merge 合并另一段 usage（流式：input 侧来自 message_start，output 侧来自 message_delta）。
// 各字段取非零者（后到的 output_tokens 覆盖 message_start 的占位 1）。
func (n normUsage) merge(other normUsage) normUsage {
	pick := func(a, b int) int {
		if b != 0 {
			return b
		}
		return a
	}
	return normUsage{
		PromptTokens:     pick(n.PromptTokens, other.PromptTokens),
		CompletionTokens: pick(n.CompletionTokens, other.CompletionTokens),
		CachedTokens:     pick(n.CachedTokens, other.CachedTokens),
		CacheCreation:    pick(n.CacheCreation, other.CacheCreation),
		CacheCreation5m:  pick(n.CacheCreation5m, other.CacheCreation5m),
		CacheCreation1h:  pick(n.CacheCreation1h, other.CacheCreation1h),
	}
}

// dtoUsage 转 billing 用的 dto.Usage。
func (n normUsage) dtoUsage() dto.Usage {
	return dto.Usage{
		PromptTokens:          n.PromptTokens,
		CompletionTokens:      n.CompletionTokens,
		CachedTokens:          n.CachedTokens,
		CacheCreationTokens:   n.CacheCreation,
		CacheCreation5mTokens: n.CacheCreation5m,
		CacheCreation1hTokens: n.CacheCreation1h,
	}
}
