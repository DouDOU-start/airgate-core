package healthmon

import "math"

// BuildSample 由 S/E 构造样本摘要。
func BuildSample(s, e int64, minSample int) Sample {
	if minSample <= 0 {
		minSample = DefaultMinSample
	}
	n := s + e
	return Sample{
		N:         n,
		S:         s,
		E:         e,
		Idle:      n == 0,
		LowSample: n > 0 && n < int64(minSample),
	}
}

// Rates 计算成功率与错误率；idle 时均为 0。
func Rates(sample Sample) (successRate, errorRate float64) {
	if sample.N <= 0 {
		return 0, 0
	}
	successRate = float64(sample.S) / float64(sample.N)
	errorRate = float64(sample.E) / float64(sample.N)
	return successRate, errorRate
}

// ComputeHealthScore 计算 0–100 健康分；idle 返回 nil。
//
// business = 0.6*errorScore + 0.4*ttftScore
//   - error_rate: 1%→100, 10%→0 线性
//   - ttft 窗口峰值: 1s→100, 3s→0 线性；无 TTFT 样本时仅用 errorScore
func ComputeHealthScore(sample Sample, errorRate float64, ttftMaxMs int64, hasTTFT bool) *int {
	if sample.Idle {
		return nil
	}
	errorScore := scoreErrorRate(errorRate)
	business := errorScore
	if hasTTFT && ttftMaxMs > 0 {
		ttftScore := scoreTTFT(float64(ttftMaxMs))
		business = errorScore*0.6 + ttftScore*0.4
	}
	score := int(math.Round(clamp(business, 0, 100)))
	return &score
}

func scoreErrorRate(rate float64) float64 {
	pct := rate * 100
	if pct <= 1 {
		return 100
	}
	if pct >= 10 {
		return 0
	}
	return (10 - pct) / 9 * 100
}

func scoreTTFT(maxMs float64) float64 {
	if maxMs <= 1000 {
		return 100
	}
	if maxMs >= 3000 {
		return 0
	}
	return (3000 - maxMs) / 2000 * 100
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// MergeCounts 将分类计数累加到 Counts。
func MergeCounts(dst *Counts, class ErrorClass, n int64) {
	if n <= 0 {
		return
	}
	switch class {
	case ClassAuth:
		dst.Auth += n
	case ClassRateLimit:
		dst.RateLimit += n
	case ClassUpstream5xx:
		dst.Upstream5xx += n
	case ClassClient:
		dst.Client += n
	case ClassCanceled:
		dst.Canceled += n
	case ClassPrecheck:
		dst.Precheck += n
	default:
		dst.Other += n
	}
}

// SLAErrorCount 从 Counts 取计入 error_rate 的失败数。
func SLAErrorCount(c Counts) int64 {
	return c.Auth + c.RateLimit + c.Upstream5xx
}
