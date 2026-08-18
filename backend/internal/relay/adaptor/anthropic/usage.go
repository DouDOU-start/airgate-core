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
	inputTokens := nonNegativeTokenCount(u.InputTokens)
	cacheReadTokens := nonNegativeTokenCount(u.CacheReadInputTokens)
	n := normUsage{
		PromptTokens:     addTokenCounts(inputTokens, cacheReadTokens),
		CompletionTokens: nonNegativeTokenCount(u.OutputTokens),
		CachedTokens:     cacheReadTokens,
		CacheCreation:    nonNegativeTokenCount(u.CacheCreationInputTokens),
	}
	if u.CacheCreation != nil {
		n.CacheCreation5m = nonNegativeTokenCount(u.CacheCreation.Ephemeral5mInputTokens)
		n.CacheCreation1h = nonNegativeTokenCount(u.CacheCreation.Ephemeral1hInputTokens)
		if n.CacheCreation == 0 {
			n.CacheCreation = addTokenCounts(n.CacheCreation5m, n.CacheCreation1h)
		}
	}
	return n
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

// merge 合并另一段 usage（流式：input 侧来自 message_start，output 侧来自 message_delta）。
// 各字段都是累计值，逐字段取最大值既能覆盖 message_start 的 output_tokens 占位值，
// 也不会因兼容上游的后帧缺字段或较小值丢失已经观察到的用量。
func (n normUsage) merge(other normUsage) normUsage {
	max := func(a, b int) int {
		if b > a {
			return b
		}
		return a
	}
	return normUsage{
		PromptTokens:     max(n.PromptTokens, other.PromptTokens),
		CompletionTokens: max(n.CompletionTokens, other.CompletionTokens),
		CachedTokens:     max(n.CachedTokens, other.CachedTokens),
		CacheCreation:    max(n.CacheCreation, other.CacheCreation),
		CacheCreation5m:  max(n.CacheCreation5m, other.CacheCreation5m),
		CacheCreation1h:  max(n.CacheCreation1h, other.CacheCreation1h),
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
