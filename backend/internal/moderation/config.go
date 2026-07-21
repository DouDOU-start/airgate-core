// Package moderation 是风控中心的判定核心：输入抽取、关键词拦截、外部审核 API
// 调用与熔断、observe 异步 worker 池、命中哈希缓存、审核日志与封禁副作用。
//
// 分层约束：本包与 billing/errlog 同级，不 import ent 与 app 包——配置读取、
// 日志落库、封禁动账均经窄接口（ConfigSource/LogStore/UserBanner/Notifier）
// 由装配方注入；relay 子系统经 pipeline/task 的窄接口调用 Engine.Check。
package moderation

import (
	"encoding/binary"
	"encoding/hex"
	"net/http"
	"strings"
	"time"
)

// 运行模式 / 处置动作 / 关键词模式 / 模型过滤常量（与前端配置页字面量一致）。
const (
	ModeOff      = "off"
	ModeObserve  = "observe"
	ModePreBlock = "pre_block"

	ActionAllow        = "allow"
	ActionBlock        = "block"
	ActionHashBlock    = "hash_block"
	ActionKeywordBlock = "keyword_block"
	ActionError        = "error"

	keywordCategory = "keyword"
	hashCategory    = "hash"

	KeywordModeKeywordOnly   = "keyword_only"
	KeywordModeKeywordAndAPI = "keyword_and_api"
	KeywordModeAPIOnly       = "api_only"

	ModelFilterAll     = "all"
	ModelFilterInclude = "include"
	ModelFilterExclude = "exclude"
)

// 输入抽取协议（Protocol* 对应 airgate 各入站端点树）。
const (
	ProtocolOpenAIChat        = "openai_chat"
	ProtocolOpenAIResponses   = "openai_responses"
	ProtocolOpenAIImages      = "openai_images"
	ProtocolAnthropicMessages = "anthropic_messages"
	ProtocolGemini            = "gemini"
	ProtocolOpenAIVideo       = "openai_video"
	ProtocolSuno              = "suno"
)

const (
	defaultBaseURL   = "https://api.openai.com"
	defaultModel     = "omni-moderation-latest"
	defaultTimeoutMS = 3000
	maxTimeoutMS     = 30000
	maxInputRunes    = 12000
	maxExcerptRunes  = 240

	defaultWorkerCount          = 4
	maxWorkerCount              = 32
	defaultQueueSize            = 32768
	maxQueueSize                = 100000
	defaultBanThreshold         = 10
	defaultViolationWindowHours = 720
	defaultBlockStatus          = http.StatusForbidden
	defaultBlockMessage         = "内容审计命中风险规则，请调整输入后重试"
	defaultRetryCount           = 2
	maxRetryCount               = 5
	defaultHitRetentionDays     = 180
	defaultNonHitRetentionDays  = 3
	maxHitRetentionDays         = 3650
	maxNonHitRetentionDays      = 3
	maxInputImages              = 1
	maxBlockedKeywords          = 10000
	maxBlockedKeywordRunes      = 200
	maxModelFilterModels        = 1000
	maxModelFilterRunes         = 200

	// key 熔断冻结时长：401/403 认证错冻长、429/529 限流冻短、其余 5xx/网络错最短。
	keyAuthFreezeDuration      = 10 * time.Minute
	keyRateLimitFreezeDuration = time.Minute
	keyHTTPErrorFreezeDuration = 10 * time.Second

	cleanupInterval = 24 * time.Hour
	cleanupTimeout  = 30 * time.Minute
	cleanupDelay    = 5 * time.Minute

	runtimeCacheTTL       = time.Second
	runtimeRefreshTimeout = 5 * time.Second
)

// categoryOrder 阈值判定遍历的类别序（与 OpenAI moderation API 的类别对齐）。
var categoryOrder = []string{
	"harassment",
	"harassment/threatening",
	"hate",
	"hate/threatening",
	"illicit",
	"illicit/violent",
	"self-harm",
	"self-harm/intent",
	"self-harm/instructions",
	"sexual",
	"sexual/minors",
	"violence",
	"violence/graphic",
}

// DefaultThresholds 各类别默认命中阈值（score >= threshold 即 flagged）。
func DefaultThresholds() map[string]float64 {
	return map[string]float64{
		"harassment":             0.98,
		"harassment/threatening": 0.90,
		"hate":                   0.65,
		"hate/threatening":       0.65,
		"illicit":                0.95,
		"illicit/violent":        0.95,
		"self-harm":              0.65,
		"self-harm/intent":       0.85,
		"self-harm/instructions": 0.65,
		"sexual":                 0.65,
		"sexual/minors":          0.65,
		"violence":               0.95,
		"violence/graphic":       0.95,
	}
}

// Categories 返回类别序副本（供前端阈值编辑器渲染）。
func Categories() []string {
	out := make([]string, len(categoryOrder))
	copy(out, categoryOrder)
	return out
}

// ModelFilter 审核模型过滤：all 全量 / include 仅列表内 / exclude 列表外。
type ModelFilter struct {
	Type   string   `json:"type"`
	Models []string `json:"models"`
}

// Config 内容审核配置。整体以单 JSON 存 settings 表（group=risk_control），
// APIKeys 在本包内始终为明文——持久化时的加密/解密由 app 层（ConfigSource 实现方）完成。
type Config struct {
	Enabled              bool               `json:"enabled"`
	Mode                 string             `json:"mode"`
	BaseURL              string             `json:"base_url"`
	Model                string             `json:"model"`
	APIKeys              []string           `json:"api_keys,omitempty"`
	TimeoutMS            int                `json:"timeout_ms"`
	SampleRate           int                `json:"sample_rate"`
	AllGroups            bool               `json:"all_groups"`
	GroupIDs             []int              `json:"group_ids"`
	RecordNonHits        bool               `json:"record_non_hits"`
	Thresholds           map[string]float64 `json:"thresholds"`
	WorkerCount          int                `json:"worker_count"`
	QueueSize            int                `json:"queue_size"`
	BlockStatus          int                `json:"block_status"`
	BlockMessage         string             `json:"block_message"`
	EmailOnHit           bool               `json:"email_on_hit"`
	AutoBanEnabled       bool               `json:"auto_ban_enabled"`
	BanThreshold         int                `json:"ban_threshold"`
	ViolationWindowHours int                `json:"violation_window_hours"`
	RetryCount           int                `json:"retry_count"`
	HitRetentionDays     int                `json:"hit_retention_days"`
	NonHitRetentionDays  int                `json:"non_hit_retention_days"`
	PreHashCheckEnabled  bool               `json:"pre_hash_check_enabled"`
	BlockedKeywords      []string           `json:"blocked_keywords"`
	KeywordBlockingMode  string             `json:"keyword_blocking_mode"`
	ModelFilter          ModelFilter        `json:"model_filter"`
}

// DefaultConfig 出厂默认配置（enabled=false，需管理员显式开启）。
func DefaultConfig() *Config {
	return &Config{
		Enabled:              false,
		Mode:                 ModePreBlock,
		BaseURL:              defaultBaseURL,
		Model:                defaultModel,
		TimeoutMS:            defaultTimeoutMS,
		SampleRate:           100,
		AllGroups:            true,
		GroupIDs:             []int{},
		RecordNonHits:        false,
		Thresholds:           DefaultThresholds(),
		WorkerCount:          defaultWorkerCount,
		QueueSize:            defaultQueueSize,
		BlockStatus:          defaultBlockStatus,
		BlockMessage:         defaultBlockMessage,
		EmailOnHit:           true,
		AutoBanEnabled:       true,
		BanThreshold:         defaultBanThreshold,
		ViolationWindowHours: defaultViolationWindowHours,
		RetryCount:           defaultRetryCount,
		HitRetentionDays:     defaultHitRetentionDays,
		NonHitRetentionDays:  defaultNonHitRetentionDays,
		PreHashCheckEnabled:  false,
		BlockedKeywords:      []string{},
		KeywordBlockingMode:  KeywordModeKeywordAndAPI,
		ModelFilter:          ModelFilter{Type: ModelFilterAll, Models: []string{}},
	}
}

// Normalize 归一化并 clamp 全部字段（空值回填默认、越界收敛到上限）。
func (cfg *Config) Normalize() {
	cfg.APIKeys = normalizeAPIKeys(cfg.APIKeys)
	switch cfg.Mode {
	case ModeOff, ModeObserve, ModePreBlock:
	default:
		cfg.Mode = ModePreBlock
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		cfg.BaseURL = defaultBaseURL
	}
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if strings.TrimSpace(cfg.Model) == "" {
		cfg.Model = defaultModel
	}
	cfg.Model = strings.TrimSpace(cfg.Model)
	cfg.TimeoutMS = clampInt(cfg.TimeoutMS, defaultTimeoutMS, maxTimeoutMS)
	if cfg.SampleRate < 0 {
		cfg.SampleRate = 0
	}
	if cfg.SampleRate > 100 {
		cfg.SampleRate = 100
	}
	cfg.WorkerCount = clampInt(cfg.WorkerCount, defaultWorkerCount, maxWorkerCount)
	cfg.QueueSize = clampInt(cfg.QueueSize, defaultQueueSize, maxQueueSize)
	if strings.TrimSpace(cfg.BlockMessage) == "" {
		cfg.BlockMessage = defaultBlockMessage
	}
	cfg.BlockMessage = strings.TrimSpace(cfg.BlockMessage)
	if cfg.BlockStatus <= 0 {
		cfg.BlockStatus = defaultBlockStatus
	}
	if cfg.BanThreshold <= 0 {
		cfg.BanThreshold = defaultBanThreshold
	}
	if cfg.ViolationWindowHours <= 0 {
		cfg.ViolationWindowHours = defaultViolationWindowHours
	}
	if cfg.RetryCount < 0 {
		cfg.RetryCount = 0
	}
	if cfg.RetryCount > maxRetryCount {
		cfg.RetryCount = maxRetryCount
	}
	cfg.HitRetentionDays = clampInt(cfg.HitRetentionDays, defaultHitRetentionDays, maxHitRetentionDays)
	cfg.NonHitRetentionDays = clampInt(cfg.NonHitRetentionDays, defaultNonHitRetentionDays, maxNonHitRetentionDays)
	cfg.GroupIDs = normalizeIntIDs(cfg.GroupIDs)
	cfg.Thresholds = MergeThresholds(DefaultThresholds(), cfg.Thresholds)
	cfg.BlockedKeywords = NormalizeBlockedKeywords(cfg.BlockedKeywords)
	cfg.KeywordBlockingMode = normalizeKeywordBlockingMode(cfg.KeywordBlockingMode)
	cfg.ModelFilter = normalizeModelFilter(cfg.ModelFilter)
}

// Clone 深拷贝配置（异步任务持有快照防并发修改）。
func (cfg *Config) Clone() *Config {
	if cfg == nil {
		return nil
	}
	clone := *cfg
	clone.APIKeys = append([]string(nil), cfg.APIKeys...)
	clone.GroupIDs = append([]int(nil), cfg.GroupIDs...)
	clone.BlockedKeywords = append([]string(nil), cfg.BlockedKeywords...)
	clone.Thresholds = cloneFloatMap(cfg.Thresholds)
	clone.ModelFilter = ModelFilter{
		Type:   cfg.ModelFilter.Type,
		Models: append([]string(nil), cfg.ModelFilter.Models...),
	}
	return &clone
}

func (cfg *Config) includesGroup(groupID int) bool {
	if cfg.AllGroups {
		return true
	}
	if groupID <= 0 {
		return false
	}
	for _, id := range cfg.GroupIDs {
		if id == groupID {
			return true
		}
	}
	return false
}

func (cfg *Config) includesModel(model string) bool {
	if cfg == nil {
		return true
	}
	filter := normalizeModelFilter(cfg.ModelFilter)
	switch filter.Type {
	case ModelFilterInclude:
		return modelListContains(filter.Models, model)
	case ModelFilterExclude:
		return !modelListContains(filter.Models, model)
	default:
		return true
	}
}

// shouldSample 稳定采样：取输入哈希前 2 字节模 100 与采样率比较，
// 同一输入恒定同判（避免同内容反复请求时抽样结果抖动）。
func (cfg *Config) shouldSample(hashText string) bool {
	if cfg.SampleRate >= 100 {
		return true
	}
	if cfg.SampleRate <= 0 {
		return false
	}
	raw, err := hex.DecodeString(hashText)
	if err != nil || len(raw) < 2 {
		return true
	}
	return int(binary.BigEndian.Uint16(raw[:2])%100) < cfg.SampleRate
}

func clampInt(v, def, max int) int {
	if v <= 0 {
		return def
	}
	if v > max {
		return max
	}
	return v
}

// MergeThresholds 以 base 为底、override 覆盖已知类别（值 clamp 到 [0,1]）。
func MergeThresholds(base map[string]float64, override map[string]float64) map[string]float64 {
	out := cloneFloatMap(base)
	for _, category := range categoryOrder {
		if v, ok := override[category]; ok {
			if v < 0 {
				v = 0
			}
			if v > 1 {
				v = 1
			}
			out[category] = v
		}
	}
	return out
}

func normalizeIntIDs(ids []int) []int {
	if len(ids) == 0 {
		return []int{}
	}
	seen := make(map[int]struct{}, len(ids))
	out := make([]int, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// NormalizeBlockedKeywords 去空白、截断、按小写去重，上限 10000 词。
func NormalizeBlockedKeywords(in []string) []string {
	if len(in) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, raw := range in {
		kw := strings.TrimSpace(raw)
		if kw == "" {
			continue
		}
		kw = trimRunes(kw, maxBlockedKeywordRunes)
		key := strings.ToLower(kw)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, kw)
		if len(out) >= maxBlockedKeywords {
			break
		}
	}
	return out
}

func normalizeKeywordBlockingMode(mode string) string {
	switch strings.TrimSpace(mode) {
	case KeywordModeKeywordOnly, KeywordModeAPIOnly:
		return strings.TrimSpace(mode)
	default:
		return KeywordModeKeywordAndAPI
	}
}

func normalizeModelFilter(filter ModelFilter) ModelFilter {
	out := ModelFilter{
		Type:   normalizeModelFilterType(filter.Type),
		Models: normalizeModelNames(filter.Models),
	}
	if out.Type == ModelFilterAll {
		out.Models = []string{}
	}
	return out
}

func normalizeModelFilterType(filterType string) string {
	switch strings.ToLower(strings.TrimSpace(filterType)) {
	case ModelFilterInclude:
		return ModelFilterInclude
	case ModelFilterExclude:
		return ModelFilterExclude
	default:
		return ModelFilterAll
	}
}

func normalizeModelNames(models []string) []string {
	if len(models) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	for _, raw := range models {
		model := trimRunes(strings.TrimSpace(raw), maxModelFilterRunes)
		if model == "" {
			continue
		}
		key := strings.ToLower(model)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, model)
		if len(out) >= maxModelFilterModels {
			break
		}
	}
	return out
}

func modelListContains(models []string, model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" {
		return false
	}
	for _, candidate := range models {
		if strings.ToLower(strings.TrimSpace(candidate)) == model {
			return true
		}
	}
	return false
}

func normalizeAPIKeys(keys []string) []string {
	if len(keys) == 0 {
		return []string{}
	}
	seen := make(map[string]struct{}, len(keys))
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	return out
}

func cloneFloatMap(in map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func trimRunes(text string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	return string(runes[:max])
}

// MaskSecretTail 密钥掩码展示：8 个 * + 尾 4 位。
func MaskSecretTail(secret string) string {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return ""
	}
	if len(secret) <= 4 {
		return "****"
	}
	return strings.Repeat("*", 8) + secret[len(secret)-4:]
}
