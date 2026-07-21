package moderation

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// CheckRequest 一次审核判定的入参（调用方在触网前构造）。
type CheckRequest struct {
	RequestID string
	UserID    int
	UserEmail string
	APIKeyID  int
	GroupID   int
	GroupName string
	Endpoint  string
	Protocol  string
	Model     string
	// ContentType 仅 openai_video 的 multipart 提交体需要，JSON 协议留空。
	ContentType string
	Body        []byte
}

// Decision 判定结果。Allowed=false 时调用方应以 StatusCode/Message 拒绝请求。
type Decision struct {
	Allowed         bool
	Blocked         bool
	Flagged         bool
	StatusCode      int
	Message         string
	Action          string
	HighestCategory string
	HighestScore    float64
	CategoryScores  map[string]float64
	InputHash       string
}

// LogEntry 审核日志条目（与 ent moderation_logs 列一一对应，零 FK 快照）。
type LogEntry struct {
	RequestID         string
	UserID            int
	UserEmail         string
	APIKeyID          int
	GroupID           int
	GroupName         string
	Endpoint          string
	Protocol          string
	Model             string
	Mode              string
	Action            string
	Flagged           bool
	HighestCategory   string
	HighestScore      float64
	MatchedKeyword    string
	CategoryScores    map[string]float64
	ThresholdSnapshot map[string]float64
	InputExcerpt      string
	InputHash         string
	UpstreamLatencyMS int64
	QueueDelayMS      int64
	Error             string
	ViolationCount    int
	AutoBanned        bool
	EmailSent         bool
}

// CleanupResult 一次日志 TTL 清理的结果。
type CleanupResult struct {
	DeletedHit    int64
	DeletedNonHit int64
	FinishedAt    time.Time
}

// LogStore 审核日志落库窄接口（infra/store 实现，本包不 import ent）。
type LogStore interface {
	Create(ctx context.Context, e LogEntry) error
	// CountFlaggedSince 统计滑窗内计入封号的违规数：flagged 且 action 非
	// hash_block（重复内容不重复计数），且晚于该用户最近一次 auto_banned 行
	//（每次封禁后计数从零重算）。
	CountFlaggedSince(ctx context.Context, userID int, since time.Time) (int, error)
	Cleanup(ctx context.Context, hitBefore, nonHitBefore time.Time) (CleanupResult, error)
}

// UserBanner 自动封禁窄接口（app 层实现）。返回是否真正执行了禁用——
// 管理员、已禁用用户返回 false 且不报错。
type UserBanner interface {
	DisableUser(ctx context.Context, userID int) (bool, error)
}

// ConfigSource 运行时配置源（app 层实现：读 settings 表并解密 api_keys）。
type ConfigSource interface {
	Runtime(ctx context.Context) (enabled bool, cfg *Config, err error)
}

// Notifier 命中通知窄接口（可选注入；bootstrap 用 mailer 实现）。
// 返回是否成功发出过邮件（回填 LogEntry.EmailSent）。
type Notifier interface {
	OnFlagged(ctx context.Context, e LogEntry, banned bool) (sent bool)
}

type runtimeSnapshot struct {
	enabled        bool
	config         *Config
	keywordMatcher *keywordMatcher
	configDigest   [sha256.Size]byte
	loadedAt       time.Time
}

// Engine 审核判定引擎。nil Engine 与未注入依赖时 Check 恒放行（fail-open）。
type Engine struct {
	src      ConfigSource
	logs     LogStore
	hashes   HashCache
	banner   UserBanner
	notifier Notifier
	client   *apiClient

	asyncQueue chan asyncTask

	asyncActive    atomic.Int64
	asyncEnqueued  atomic.Int64
	asyncDropped   atomic.Int64
	asyncProcessed atomic.Int64
	asyncErrors    atomic.Int64

	preBlockActive         atomic.Int64
	preBlockChecked        atomic.Int64
	preBlockAllowed        atomic.Int64
	preBlockBlocked        atomic.Int64
	preBlockErrors         atomic.Int64
	preBlockLatencyTotalMS atomic.Int64

	lastCleanupUnix          atomic.Int64
	lastCleanupDeletedHit    atomic.Int64
	lastCleanupDeletedNonHit atomic.Int64

	snapshot       atomic.Pointer[runtimeSnapshot]
	refreshMu      sync.Mutex
	refreshRetryAt atomic.Int64
	snapshotTTL    time.Duration
}

// NewEngine 构造引擎（不启动后台 goroutine，worker/清理由 StartBackground 拉起）。
func NewEngine(src ConfigSource, logs LogStore, hashes HashCache, banner UserBanner) *Engine {
	return &Engine{
		src:        src,
		logs:       logs,
		hashes:     hashes,
		banner:     banner,
		client:     newAPIClient(),
		asyncQueue: make(chan asyncTask, maxQueueSize),
	}
}

// SetNotifier 注入命中通知实现（可选，装配期调用）。
func (e *Engine) SetNotifier(n Notifier) {
	if e != nil {
		e.notifier = n
	}
}

// Check 审核判定主链。短路顺序：总开关 → enabled/mode → 分组 → 模型 → 输入抽取
// （空放行）→ pre_block 关键词 → 命中哈希缓存 → 采样 → 无 key 放行 →
// observe 入队放行 / pre_block 同步调外部 API。任何内部故障 fail-open 放行。
func (e *Engine) Check(ctx context.Context, in CheckRequest) Decision {
	allow := Decision{Allowed: true, Action: ActionAllow}
	if e == nil || e.src == nil || e.logs == nil {
		return allow
	}
	snap, err := e.loadSnapshot(ctx)
	if err != nil {
		slog.Warn("moderation.skip_config_load_failed", "user_id", in.UserID, "endpoint", in.Endpoint, "error", err)
		return allow
	}
	if !snap.enabled {
		return allow
	}
	cfg := snap.config
	if !cfg.Enabled || cfg.Mode == ModeOff {
		return allow
	}
	if !cfg.includesGroup(in.GroupID) || !cfg.includesModel(in.Model) {
		return allow
	}
	content := ExtractInput(in.Protocol, in.ContentType, in.Body)
	if content.IsEmpty() {
		return allow
	}
	hashText := content.Hash()
	if cfg.Mode == ModePreBlock {
		if cfg.KeywordBlockingMode != KeywordModeAPIOnly && len(cfg.BlockedKeywords) > 0 {
			if keyword, hit := snap.matchBlockedKeyword(content.Text); hit {
				e.recordPreBlockMetric(0, ActionKeywordBlock)
				scores := map[string]float64{keywordCategory: 1.0}
				entry := e.buildLog(in, cfg, ActionKeywordBlock, true, keywordCategory, 1.0, scores, content.Text, hashText, 0, "")
				entry.MatchedKeyword = keyword
				e.enqueueRecord(cfg, entry, false, true)
				return Decision{
					Blocked: true, Flagged: true,
					Message: cfg.BlockMessage, StatusCode: cfg.BlockStatus,
					HighestCategory: keywordCategory, HighestScore: 1.0,
					CategoryScores: scores, Action: ActionKeywordBlock,
				}
			}
		}
		if cfg.KeywordBlockingMode == KeywordModeKeywordOnly {
			e.recordPreBlockMetric(0, ActionAllow)
			return allow
		}
	}
	if cfg.PreHashCheckEnabled && e.hashes != nil {
		matched, err := e.hashes.Has(ctx, hashText)
		if err != nil {
			slog.Warn("moderation.hash_check_failed", "user_id", in.UserID, "endpoint", in.Endpoint, "error", err)
		}
		if matched {
			if cfg.Mode == ModePreBlock {
				e.recordPreBlockMetric(0, ActionHashBlock)
			}
			message := cfg.BlockMessage
			if message != "" {
				message = fmt.Sprintf("%s（hash: %s）", message, hashText)
			}
			scores := map[string]float64{hashCategory: 1.0}
			entry := e.buildLog(in, cfg, ActionHashBlock, true, hashCategory, 1.0, scores, content.Text, hashText, 0, "")
			// hash_block 不重复记 hash、不触发封号副作用（同内容重复提交不加罪）。
			e.enqueueRecord(cfg, entry, false, false)
			return Decision{
				Blocked: true, Flagged: true,
				Message: message, StatusCode: cfg.BlockStatus,
				InputHash: hashText, Action: ActionHashBlock,
			}
		}
	}
	if !cfg.shouldSample(hashText) {
		if cfg.Mode == ModePreBlock {
			e.recordPreBlockMetric(0, ActionAllow)
		}
		return allow
	}
	if len(cfg.APIKeys) == 0 {
		if cfg.Mode == ModePreBlock {
			e.recordPreBlockMetric(0, ActionError)
		}
		slog.Warn("moderation.skip_no_audit_api_keys", "user_id", in.UserID, "endpoint", in.Endpoint)
		return allow
	}
	if cfg.Mode == ModeObserve {
		e.enqueueAudit(in, content, hashText)
		return allow
	}
	return e.checkSync(ctx, in, cfg, content, hashText, 0, true)
}

// checkSync 调外部审核 API 并按阈值判定。queueDelayMS>0 表示来自 observe
// 异步链路；allowBlock=false（observe）恒放行只记录。审核 API 失败 fail-open。
func (e *Engine) checkSync(ctx context.Context, in CheckRequest, cfg *Config, content Input, hashText string, queueDelayMS int64, allowBlock bool) Decision {
	allow := Decision{Allowed: true, Action: ActionAllow}
	trackPreBlock := queueDelayMS == 0 && allowBlock && cfg.Mode == ModePreBlock
	if trackPreBlock {
		e.preBlockActive.Add(1)
		defer e.preBlockActive.Add(-1)
	}
	start := time.Now()
	result, err := e.client.call(ctx, cfg, content.ModerationInput(), trackPreBlock)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		if trackPreBlock {
			e.recordPreBlockMetric(latency, ActionError)
		}
		if queueDelayMS > 0 {
			e.asyncErrors.Add(1)
		}
		slog.Warn("moderation.audit_api_failed", "user_id", in.UserID, "endpoint", in.Endpoint, "latency_ms", latency, "error", err)
		if cfg.RecordNonHits {
			entry := e.buildLog(in, cfg, ActionError, false, "", 0, nil, content.Text, hashText, queueDelayMS, err.Error())
			entry.UpstreamLatencyMS = latency
			e.persistLog(ctx, cfg, entry, false, false)
		}
		return allow
	}

	flagged, highestCategory, highestScore := evaluateScores(result.CategoryScores, cfg.Thresholds)
	action := ActionAllow
	blocked := false
	if allowBlock && flagged && cfg.Mode == ModePreBlock {
		action = ActionBlock
		blocked = true
	}
	if trackPreBlock {
		e.recordPreBlockMetric(latency, action)
	}
	if flagged || cfg.RecordNonHits {
		entry := e.buildLog(in, cfg, action, flagged, highestCategory, highestScore, result.CategoryScores, content.Text, hashText, queueDelayMS, "")
		entry.UpstreamLatencyMS = latency
		if queueDelayMS == 0 && cfg.Mode == ModePreBlock {
			// 同步链路不让落库拖慢响应，转异步记录。
			e.enqueueRecord(cfg, entry, flagged, flagged)
		} else {
			e.persistLog(ctx, cfg, entry, flagged, flagged)
		}
	}
	if blocked {
		return Decision{
			Blocked: true, Flagged: true,
			Message: cfg.BlockMessage, StatusCode: cfg.BlockStatus,
			HighestCategory: highestCategory, HighestScore: highestScore,
			CategoryScores: result.CategoryScores, Action: action,
		}
	}
	return Decision{
		Allowed: true, Flagged: flagged,
		HighestCategory: highestCategory, HighestScore: highestScore,
		CategoryScores: result.CategoryScores, Action: action,
	}
}

func (e *Engine) buildLog(in CheckRequest, cfg *Config, action string, flagged bool, highestCategory string, highestScore float64, scores map[string]float64, text, hashText string, queueDelayMS int64, errText string) LogEntry {
	return LogEntry{
		RequestID:         in.RequestID,
		UserID:            in.UserID,
		UserEmail:         in.UserEmail,
		APIKeyID:          in.APIKeyID,
		GroupID:           in.GroupID,
		GroupName:         in.GroupName,
		Endpoint:          in.Endpoint,
		Protocol:          in.Protocol,
		Model:             in.Model,
		Mode:              cfg.Mode,
		Action:            action,
		Flagged:           flagged,
		HighestCategory:   highestCategory,
		HighestScore:      highestScore,
		CategoryScores:    cloneFloatMap(scores),
		ThresholdSnapshot: cloneFloatMap(cfg.Thresholds),
		InputExcerpt:      Excerpt(text),
		InputHash:         hashText,
		QueueDelayMS:      queueDelayMS,
		Error:             errText,
	}
}

// persistLog 落库与副作用：记 flagged hash → 滑窗计数/自动封禁 → 邮件通知 → 落库。
func (e *Engine) persistLog(ctx context.Context, cfg *Config, entry LogEntry, recordHash, applySideEffects bool) {
	if recordHash && e.hashes != nil {
		if err := e.hashes.Record(ctx, entry.InputHash); err != nil {
			slog.Warn("moderation.record_hash_failed", "user_id", entry.UserID, "error", err)
		}
	}
	if applySideEffects {
		banned := e.applyBanSideEffects(ctx, cfg, &entry)
		if e.notifier != nil && entry.Flagged && entry.UserEmail != "" {
			entry.EmailSent = e.notifier.OnFlagged(ctx, entry, banned)
		}
	}
	if err := e.logs.Create(ctx, entry); err != nil {
		slog.Warn("moderation.create_log_failed", "user_id", entry.UserID, "action", entry.Action, "error", err)
	}
}

// applyBanSideEffects 滑窗违规计数并按阈值自动封禁；返回本次是否真正执行了禁用。
func (e *Engine) applyBanSideEffects(ctx context.Context, cfg *Config, entry *LogEntry) bool {
	if !entry.Flagged || entry.UserID <= 0 {
		return false
	}
	count := 1
	if cfg.ViolationWindowHours > 0 {
		since := time.Now().Add(-time.Duration(cfg.ViolationWindowHours) * time.Hour)
		if n, err := e.logs.CountFlaggedSince(ctx, entry.UserID, since); err == nil {
			count = n + 1
		}
	}
	entry.ViolationCount = count
	if !cfg.AutoBanEnabled || cfg.BanThreshold <= 0 || count < cfg.BanThreshold || e.banner == nil {
		return false
	}
	banned, err := e.banner.DisableUser(ctx, entry.UserID)
	if err != nil {
		slog.Warn("moderation.auto_ban_failed", "user_id", entry.UserID, "error", err)
		return false
	}
	entry.AutoBanned = true
	return banned
}

func (e *Engine) recordPreBlockMetric(latencyMS int64, action string) {
	e.preBlockChecked.Add(1)
	if latencyMS < 0 {
		latencyMS = 0
	}
	e.preBlockLatencyTotalMS.Add(latencyMS)
	switch action {
	case ActionBlock, ActionHashBlock, ActionKeywordBlock:
		e.preBlockBlocked.Add(1)
	case ActionError:
		e.preBlockErrors.Add(1)
	default:
		e.preBlockAllowed.Add(1)
	}
}

// ---- 运行时配置快照（1s TTL，过期异步单飞刷新、失败 serve-stale）----

func (e *Engine) snapshotCacheTTL() time.Duration {
	if e.snapshotTTL > 0 {
		return e.snapshotTTL
	}
	return runtimeCacheTTL
}

func (e *Engine) loadSnapshot(ctx context.Context) (*runtimeSnapshot, error) {
	now := time.Now()
	if snap := e.snapshot.Load(); snap != nil {
		if now.Sub(snap.loadedAt) < e.snapshotCacheTTL() {
			return snap, nil
		}
		e.triggerSnapshotRefresh()
		return snap, nil
	}
	e.refreshMu.Lock()
	defer e.refreshMu.Unlock()
	if snap := e.snapshot.Load(); snap != nil {
		return snap, nil
	}
	return e.refreshSnapshot(ctx)
}

func (e *Engine) triggerSnapshotRefresh() {
	if time.Now().UnixNano() < e.refreshRetryAt.Load() || !e.refreshMu.TryLock() {
		return
	}
	go func() {
		defer e.refreshMu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), runtimeRefreshTimeout)
		defer cancel()
		if _, err := e.refreshSnapshot(ctx); err != nil {
			e.refreshRetryAt.Store(time.Now().Add(e.snapshotCacheTTL()).UnixNano())
			slog.Warn("moderation.snapshot_refresh_failed", "error", err)
		}
	}()
}

func (e *Engine) refreshSnapshot(ctx context.Context) (*runtimeSnapshot, error) {
	enabled, cfg, err := e.src.Runtime(ctx)
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		cfg = DefaultConfig()
	}
	cfg.Normalize()
	raw, _ := json.Marshal(cfg)
	digest := sha256.Sum256(raw)
	// 配置未变时复用已构建的关键词自动机（构建成本随词表增长）。
	if current := e.snapshot.Load(); current != nil && current.configDigest == digest {
		snap := &runtimeSnapshot{
			enabled:        enabled,
			config:         current.config,
			keywordMatcher: current.keywordMatcher,
			configDigest:   digest,
			loadedAt:       time.Now(),
		}
		e.snapshot.Store(snap)
		e.refreshRetryAt.Store(0)
		return snap, nil
	}
	snap := &runtimeSnapshot{
		enabled:        enabled,
		config:         cfg,
		keywordMatcher: newKeywordMatcher(cfg.BlockedKeywords),
		configDigest:   digest,
		loadedAt:       time.Now(),
	}
	e.snapshot.Store(snap)
	e.refreshRetryAt.Store(0)
	return snap, nil
}

// InvalidateSnapshot 配置更新后立即失效缓存（下一次 Check 重新加载）。
func (e *Engine) InvalidateSnapshot() {
	if e != nil {
		e.snapshot.Store(nil)
	}
}

func (s *runtimeSnapshot) matchBlockedKeyword(text string) (string, bool) {
	if s.keywordMatcher != nil {
		return s.keywordMatcher.Match(text)
	}
	return "", false
}
