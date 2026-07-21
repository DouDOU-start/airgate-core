package moderation

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrBadInput 测试入参不合法（app 层映射为 400）。
var ErrBadInput = errors.New("invalid moderation input")

// RuntimeStatus 引擎运行时状态快照（admin 状态页）。
type RuntimeStatus struct {
	Enabled              bool        `json:"enabled"`
	RiskControlEnabled   bool        `json:"risk_control_enabled"`
	Mode                 string      `json:"mode"`
	WorkerCount          int         `json:"worker_count"`
	MaxWorkers           int         `json:"max_workers"`
	ActiveWorkers        int64       `json:"active_workers"`
	QueueSize            int         `json:"queue_size"`
	QueueLength          int         `json:"queue_length"`
	QueueUsagePercent    float64     `json:"queue_usage_percent"`
	Enqueued             int64       `json:"enqueued"`
	Dropped              int64       `json:"dropped"`
	Processed            int64       `json:"processed"`
	Errors               int64       `json:"errors"`
	PreBlockActive       int64       `json:"pre_block_active"`
	PreBlockChecked      int64       `json:"pre_block_checked"`
	PreBlockAllowed      int64       `json:"pre_block_allowed"`
	PreBlockBlocked      int64       `json:"pre_block_blocked"`
	PreBlockErrors       int64       `json:"pre_block_errors"`
	PreBlockAvgLatencyMS int64       `json:"pre_block_avg_latency_ms"`
	APIKeyAvailable      int64       `json:"api_key_available_count"`
	APIKeyLoads          []KeyLoad   `json:"api_key_loads"`
	APIKeyStatuses       []KeyStatus `json:"api_key_statuses"`
	FlaggedHashCount     int64       `json:"flagged_hash_count"`
	LastCleanupAt        *time.Time  `json:"last_cleanup_at,omitempty"`
	LastCleanupHit       int64       `json:"last_cleanup_deleted_hit"`
	LastCleanupNonHit    int64       `json:"last_cleanup_deleted_non_hit"`
}

// Status 汇总运行时指标（配置读缓存快照，失败返回零值状态）。
func (e *Engine) Status(ctx context.Context) RuntimeStatus {
	out := RuntimeStatus{MaxWorkers: maxWorkerCount}
	if e == nil {
		return out
	}
	snap, err := e.loadSnapshot(ctx)
	if err == nil {
		out.RiskControlEnabled = snap.enabled
		out.Enabled = snap.config.Enabled
		out.Mode = snap.config.Mode
		out.WorkerCount = snap.config.WorkerCount
		out.QueueSize = snap.config.QueueSize
		out.APIKeyAvailable = e.client.availableKeyCount(snap.config.APIKeys)
		out.APIKeyLoads = e.client.KeyLoads(snap.config.APIKeys)
		out.APIKeyStatuses = e.client.KeyStatuses(snap.config.APIKeys)
	}
	out.ActiveWorkers = e.asyncActive.Load()
	out.QueueLength = len(e.asyncQueue)
	if out.QueueSize > 0 {
		out.QueueUsagePercent = float64(out.QueueLength) / float64(out.QueueSize) * 100
	}
	out.Enqueued = e.asyncEnqueued.Load()
	out.Dropped = e.asyncDropped.Load()
	out.Processed = e.asyncProcessed.Load()
	out.Errors = e.asyncErrors.Load()
	out.PreBlockActive = e.preBlockActive.Load()
	out.PreBlockChecked = e.preBlockChecked.Load()
	out.PreBlockAllowed = e.preBlockAllowed.Load()
	out.PreBlockBlocked = e.preBlockBlocked.Load()
	out.PreBlockErrors = e.preBlockErrors.Load()
	if checked := out.PreBlockChecked; checked > 0 {
		out.PreBlockAvgLatencyMS = e.preBlockLatencyTotalMS.Load() / checked
	}
	if e.hashes != nil {
		if n, err := e.hashes.Count(ctx); err == nil {
			out.FlaggedHashCount = n
		}
	}
	if unix := e.lastCleanupUnix.Load(); unix > 0 {
		t := time.Unix(unix, 0)
		out.LastCleanupAt = &t
		out.LastCleanupHit = e.lastCleanupDeletedHit.Load()
		out.LastCleanupNonHit = e.lastCleanupDeletedNonHit.Load()
	}
	return out
}

// KeyStatuses 按给定明文 key 列表导出健康状态快照（配置页回显）。
func (e *Engine) KeyStatuses(keys []string) []KeyStatus {
	if e == nil {
		return []KeyStatus{}
	}
	return e.client.KeyStatuses(keys)
}

// TestKeysInput 探活/试审入参：api_keys 为空时测当前已配置的 key；
// prompt/images 非空时同时返回真实审核判定结果。
type TestKeysInput struct {
	APIKeys   []string
	BaseURL   string
	Model     string
	TimeoutMS int
	Prompt    string
	Images    []string
}

// TestAuditResult 试审的判定结果（按当前配置阈值评估）。
type TestAuditResult struct {
	Flagged         bool               `json:"flagged"`
	HighestCategory string             `json:"highest_category"`
	HighestScore    float64            `json:"highest_score"`
	CategoryScores  map[string]float64 `json:"category_scores"`
	Thresholds      map[string]float64 `json:"thresholds"`
}

// TestKeysResult 探活结果：逐 key 状态 + 可选试审判定。
type TestKeysResult struct {
	Items       []KeyStatus      `json:"items"`
	AuditResult *TestAuditResult `json:"audit_result,omitempty"`
	ImageCount  int              `json:"image_count"`
}

const (
	maxTestImageBytes        = 8 * 1024 * 1024
	maxTestImageDataURLBytes = 12 * 1024 * 1024
)

// TestKeys 逐 key 探活（可覆盖 base_url/model/timeout；带 prompt/images 时做真实试审）。
func (e *Engine) TestKeys(ctx context.Context, in TestKeysInput) (*TestKeysResult, error) {
	snap, err := e.loadSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	cfg := snap.config.Clone()
	keys := normalizeAPIKeys(in.APIKeys)
	configured := false
	if len(keys) == 0 {
		keys = cfg.APIKeys
		configured = true
	}
	if strings.TrimSpace(in.BaseURL) != "" {
		cfg.BaseURL = in.BaseURL
	}
	if strings.TrimSpace(in.Model) != "" {
		cfg.Model = in.Model
	}
	if in.TimeoutMS > 0 {
		cfg.TimeoutMS = in.TimeoutMS
	}
	cfg.Normalize()
	testInput, imageCount, err := buildTestInput(in.Prompt, in.Images)
	if err != nil {
		return nil, err
	}
	// 已配置 key + 带试审内容时只打一个可用 key（省成本），纯探活才逐 key 打。
	if configured && hasAuditInput(in.Prompt, in.Images) {
		key, ok := e.client.nextUsableKey(cfg)
		if !ok {
			return &TestKeysResult{Items: e.client.KeyStatuses(keys), ImageCount: imageCount}, nil
		}
		keys = []string{key}
	}
	if len(keys) == 0 {
		return &TestKeysResult{Items: []KeyStatus{}, ImageCount: imageCount}, nil
	}
	items := make([]KeyStatus, 0, len(keys))
	var auditResult *TestAuditResult
	for idx, key := range keys {
		start := time.Now()
		httpStatus := 0
		result, err := e.client.callOnce(ctx, cfg, key, testInput, &httpStatus)
		latency := int(time.Since(start).Milliseconds())
		if err != nil {
			e.client.markKeyError(key, err.Error(), latency, httpStatus)
		} else {
			e.client.markKeySuccess(key, latency, httpStatus)
			if auditResult == nil {
				thresholds := MergeThresholds(DefaultThresholds(), cfg.Thresholds)
				flagged, highestCategory, highestScore := evaluateScores(result.CategoryScores, thresholds)
				auditResult = &TestAuditResult{
					Flagged:         flagged,
					HighestCategory: highestCategory,
					HighestScore:    highestScore,
					CategoryScores:  cloneFloatMap(result.CategoryScores),
					Thresholds:      thresholds,
				}
			}
		}
		status := e.client.keyStatusForHash(idx, KeyHash(key), MaskSecretTail(key), configured)
		status.LastTested = true
		items = append(items, status)
	}
	return &TestKeysResult{Items: items, AuditResult: auditResult, ImageCount: imageCount}, nil
}

func hasAuditInput(prompt string, images []string) bool {
	if normalizeText(prompt) != "" {
		return true
	}
	for _, image := range images {
		if strings.TrimSpace(image) != "" {
			return true
		}
	}
	return false
}

func buildTestInput(prompt string, images []string) (any, int, error) {
	prompt = trimRunes(normalizeText(prompt), maxInputRunes)
	normalized := make([]string, 0, len(images))
	for _, image := range images {
		image = strings.TrimSpace(image)
		if image == "" {
			continue
		}
		if len(normalized) >= maxInputImages {
			return nil, 0, fmt.Errorf("%w: 最多上传 %d 张测试图片", ErrBadInput, maxInputImages)
		}
		if err := validateTestImageDataURL(image); err != nil {
			return nil, 0, err
		}
		normalized = append(normalized, image)
	}
	if prompt == "" && len(normalized) == 0 {
		return "hello", 0, nil
	}
	if len(normalized) == 0 {
		return prompt, 0, nil
	}
	parts := make([]apiInputPart, 0, len(normalized)+1)
	if prompt != "" {
		parts = append(parts, apiInputPart{Type: "text", Text: prompt})
	}
	for _, image := range normalized {
		parts = append(parts, apiInputPart{Type: "image_url", ImageURL: &apiImageURLRef{URL: image}})
	}
	return parts, len(normalized), nil
}

func validateTestImageDataURL(value string) error {
	if len(value) > maxTestImageDataURLBytes {
		return fmt.Errorf("%w: 测试图片不能超过 8MB", ErrBadInput)
	}
	if !strings.HasPrefix(value, "data:image/") {
		return fmt.Errorf("%w: 测试图片必须是 data:image/* base64", ErrBadInput)
	}
	parts := strings.SplitN(value, ",", 2)
	if len(parts) != 2 || !strings.Contains(parts[0], ";base64") {
		return fmt.Errorf("%w: 测试图片必须是 base64 data URL", ErrBadInput)
	}
	raw, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return fmt.Errorf("%w: 测试图片 base64 无效", ErrBadInput)
	}
	if len(raw) > maxTestImageBytes {
		return fmt.Errorf("%w: 测试图片不能超过 8MB", ErrBadInput)
	}
	return nil
}
