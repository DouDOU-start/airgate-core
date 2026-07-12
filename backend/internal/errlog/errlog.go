// Package errlog 提供上游请求日志（upstream_request_logs）的异步落库与错误计数。
//
// 与 billing.Recorder 的关键差异（有意保持两套实现，勿抽公共批处理器）：
// 计费记录不可丢、与扣费同事务、失败降级重试；本包是诊断留痕——纯 INSERT、
// 无事务负担、缓冲满即丢（丢弃走聚合告警），错误风暴下宁可丢明细不可拖垮主链路。
// 错误率的事实源是 Redis 分钟桶计数器（CountFailure，恒计数不采样），
// 落库明细允许有损。
package errlog

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/DouDOU-start/airgate-core/ent"
	entupstreamrequestlog "github.com/DouDOU-start/airgate-core/ent/upstreamrequestlog"
)

const (
	defaultBufferSize = 4096
	batchSize         = 200
	flushInterval     = 2 * time.Second

	// maxMessageLen / maxReasonLen 摘要长度上限（rune）。
	maxMessageLen = 500
	maxReasonLen  = 500

	// dropWarnInterval 丢弃告警聚合间隔：风暴下逐条 WARN 本身就是放大器。
	dropWarnInterval = 10 * time.Second

	// counterKeyTTL 分钟桶计数器过期时间。
	counterKeyTTL = 90 * time.Minute

	// retentionDays 日志保留天数；cleanupInterval 清理周期；cleanupBatch 单批删除行数。
	retentionDays   = 30
	cleanupInterval = 6 * time.Hour
	cleanupBatch    = 5000

	// foldWindow / foldCacheMax 重复失败折叠：同签名失败在窗口内不重复插行，
	// 改为对首行 repeat_count 自增——错误风暴下行数从 O(请求数) 压到 O(签名数)。
	foldWindow   = time.Minute
	foldCacheMax = 4096
)

// 发起方常量（与 ent 枚举一致）。表内只有失败行：
// 成功一律走 usage_logs（渠道测试成功按 0 费用落账本）。
const (
	SourceRelay       = "relay"
	SourceChannelTest = "channel_test"
)

// relay 失败阶段常量（与 forward.go 终止路径一一对应）。
const (
	PhasePrecheckBalance     = "precheck_balance"
	PhasePrecheckPrice       = "precheck_price"
	PhasePrecheckRate        = "precheck_rate"
	PhaseLocalLimit          = "local_limit"
	PhaseQueueTimeout        = "queue_timeout"
	PhaseUpstreamExhausted   = "upstream_exhausted"
	PhaseUpstreamClientError = "upstream_client_error"
	PhaseCanceled            = "canceled"
	PhaseStreamAborted       = "stream_aborted"
	// PhaseBadRequest 构建上游请求即失败（非法请求体 / 不支持的端点 / 参数翻译失败）；
	// 属客户端/配置问题，一次性 400 终止，不 failover、不计渠道健康。
	PhaseBadRequest = "bad_request"
	// PhaseTaskFailed 异步任务上游报失败（轮询终态，已退款）。
	PhaseTaskFailed = "task_failed"
	// PhaseTaskTimeout 异步任务超时清扫置失败（已退款）。
	PhaseTaskTimeout = "task_timeout"
)

// AttemptHop 重试链中的一跳（序列化进 attempt_chain JSON）。
type AttemptHop struct {
	Seq          int    `json:"seq"`
	ChannelID    int    `json:"channel_id"`
	ChannelName  string `json:"channel_name"`
	KeyID        int    `json:"channel_key_id,omitempty"` // 本跳选中的密钥端点 ID
	KeyHint      string `json:"key_hint,omitempty"`       // 渠道密钥尾 4 位提示
	UpstreamStat int    `json:"upstream_status,omitempty"`
	Verdict      string `json:"verdict"` // rateLimited / authFailed / transient / clientError / networkError / streamAborted
	Reason       string `json:"reason,omitempty"`
	RetryAfterMs int64  `json:"retry_after_ms,omitempty"`
	LatencyMs    int64  `json:"latency_ms,omitempty"`
	AutoDisabled bool   `json:"auto_disabled,omitempty"`
}

// Entry 一条打上游失败的留痕。
type Entry struct {
	RequestID   string
	Source      string // SourceRelay / SourceChannelTest
	Phase       string // relay 失败阶段；渠道测试留空
	StatusCode  int
	ErrorType   string // 与 errfmt 错误体同源
	ErrorCode   string
	Message     string
	Attempts    int
	Chain       []AttemptHop
	Billed      bool
	Model       string
	Endpoint    string
	Stream      bool
	UserID      int
	UserEmail   string
	APIKeyID    int
	GroupID     int
	ChannelID   int
	ChannelName string
	IPAddress   string
	UserAgent   string
	DurationMs  int64
}

// secretPatterns sink 入口的通用凭证兜底脱敏（sanitizeKeyLeak 只能精确匹配
// 本渠道密钥，这里再兜一层常见凭证形态，防上游错误体回显他类密钥）。
var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{8,}`),
	regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]{8,}`),
}

// Sanitize 通用凭证脱敏 + rune 安全截断。
func Sanitize(s string, limit int) string {
	for _, p := range secretPatterns {
		s = p.ReplaceAllString(s, "***")
	}
	if limit > 0 {
		runes := []rune(s)
		if len(runes) > limit {
			s = string(runes[:limit])
		}
	}
	return s
}

// Recorder 上游请求日志异步记录器 + Redis 错误计数器。
type Recorder struct {
	db  *ent.Client
	rdb *redis.Client

	ch      chan Entry
	stopCh  chan struct{}
	stopped chan struct{}
	once    sync.Once

	// dropped 自上次告警以来的丢弃计数（聚合告警）。
	dropped      atomic.Int64
	lastDropWarn atomic.Int64 // unix 秒

	// folds 折叠缓存：失败签名 → 已落库首行。仅 flush 调用方（run goroutine）
	// 访问，无需加锁。
	folds map[string]foldRef
}

// foldRef 折叠缓存项：窗口截止前，同签名失败对该行 repeat_count 自增。
type foldRef struct {
	id    int
	until time.Time
}

// NewRecorder 创建记录器；rdb 可为 nil（计数器降级为 no-op）。
func NewRecorder(db *ent.Client, rdb *redis.Client) *Recorder {
	return &Recorder{
		db:      db,
		rdb:     rdb,
		ch:      make(chan Entry, defaultBufferSize),
		stopCh:  make(chan struct{}),
		stopped: make(chan struct{}),
		folds:   make(map[string]foldRef),
	}
}

// Record 非阻塞投递；缓冲满即丢（聚合告警）。入口统一二次脱敏。
func (r *Recorder) Record(e Entry) {
	e.Message = Sanitize(e.Message, maxMessageLen)
	for i := range e.Chain {
		e.Chain[i].Reason = Sanitize(e.Chain[i].Reason, maxReasonLen)
	}
	select {
	case r.ch <- e:
	default:
		n := r.dropped.Add(1)
		now := time.Now().Unix()
		last := r.lastDropWarn.Load()
		if now-last >= int64(dropWarnInterval/time.Second) && r.lastDropWarn.CompareAndSwap(last, now) {
			slog.Warn("errlog_entries_dropped", "count", n)
			r.dropped.Add(-n)
		}
	}
}

// CountFailure 渠道×verdict 分钟桶错误计数（错误率事实源，恒计数不受采样影响）。
// phase 维度另计一桶。Redis 不可用时静默降级。
func (r *Recorder) CountFailure(ctx context.Context, channelID int, verdict, phase string) {
	if r.rdb == nil {
		return
	}
	minute := time.Now().Unix() / 60
	pipe := r.rdb.Pipeline()
	if channelID > 0 && verdict != "" {
		key := fmt.Sprintf("agw:err:ch:%d:%s:%d", channelID, verdict, minute)
		pipe.Incr(ctx, key)
		pipe.Expire(ctx, key, counterKeyTTL)
	}
	if phase != "" {
		key := fmt.Sprintf("agw:err:phase:%s:%d", phase, minute)
		pipe.Incr(ctx, key)
		pipe.Expire(ctx, key, counterKeyTTL)
	}
	_, _ = pipe.Exec(ctx)
}

// FailureVerdicts 渠道失败计数的 verdict 全集（与 forward.go 的 CountFailure 调用一致；
// clientError 属调用方参数问题，有意不计入渠道健康信号）。
var FailureVerdicts = []string{"rateLimited", "authFailed", "transient", "networkError", "streamAborted"}

// maxCountWindowMinutes 计数读取窗口上限：桶 TTL 90min，超窗数据本就不完整。
const maxCountWindowMinutes = 60

// FailureCounts 汇总近 minutes 分钟渠道×verdict 失败计数（读分钟桶）。
// Redis 未配置时返回 nil, nil（调用方按无数据处理）。
func (r *Recorder) FailureCounts(ctx context.Context, channelIDs []int, minutes int) (map[int]map[string]int64, error) {
	if r.rdb == nil || len(channelIDs) == 0 {
		return nil, nil
	}
	if minutes <= 0 || minutes > maxCountWindowMinutes {
		minutes = maxCountWindowMinutes
	}
	nowMin := time.Now().Unix() / 60
	type keyRef struct {
		channelID int
		verdict   string
	}
	keys := make([]string, 0, len(channelIDs)*len(FailureVerdicts)*minutes)
	refs := make([]keyRef, 0, len(channelIDs)*len(FailureVerdicts)*minutes)
	for _, id := range channelIDs {
		for _, v := range FailureVerdicts {
			for m := int64(0); m < int64(minutes); m++ {
				keys = append(keys, fmt.Sprintf("agw:err:ch:%d:%s:%d", id, v, nowMin-m))
				refs = append(refs, keyRef{channelID: id, verdict: v})
			}
		}
	}

	result := make(map[int]map[string]int64)
	// MGET 分块：渠道多时 key 总数可上万，限制单次请求体量。
	const chunk = 2000
	for start := 0; start < len(keys); start += chunk {
		end := min(start+chunk, len(keys))
		vals, err := r.rdb.MGet(ctx, keys[start:end]...).Result()
		if err != nil {
			return nil, err
		}
		for i, raw := range vals {
			s, ok := raw.(string)
			if !ok {
				continue
			}
			n, err := strconv.ParseInt(s, 10, 64)
			if err != nil || n == 0 {
				continue
			}
			ref := refs[start+i]
			if result[ref.channelID] == nil {
				result[ref.channelID] = make(map[string]int64)
			}
			result[ref.channelID][ref.verdict] += n
		}
	}
	return result, nil
}

// Start 启动后台落库与 TTL 清理。
func (r *Recorder) Start() {
	go r.run()
	go r.cleanupLoop()
}

// Stop 停止并排空缓冲。
func (r *Recorder) Stop() {
	r.once.Do(func() {
		close(r.stopCh)
		<-r.stopped
	})
}

func (r *Recorder) run() {
	defer close(r.stopped)
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()

	batch := make([]Entry, 0, batchSize)
	ctx := context.Background()
	for {
		select {
		case e := <-r.ch:
			batch = append(batch, e)
			if len(batch) >= batchSize {
				r.flush(ctx, batch)
				batch = batch[:0]
			}
		case <-ticker.C:
			if len(batch) > 0 {
				r.flush(ctx, batch)
				batch = batch[:0]
			}
		case <-r.stopCh:
			// 排空缓冲后退出。不 close(r.ch)：关停窗口内在途请求仍可能调 Record，
			// close 会令其 send-on-closed-channel panic；排空按 batchSize 分片，
			// 一次性 CreateBulk 数千行会超 PostgreSQL 65535 绑定参数上限整批失败。
			for {
				select {
				case e := <-r.ch:
					batch = append(batch, e)
					if len(batch) >= batchSize {
						r.flush(ctx, batch)
						batch = batch[:0]
					}
				default:
					if len(batch) > 0 {
						r.flush(ctx, batch)
					}
					return
				}
			}
		}
	}
}

// foldKey 折叠签名；不可折叠时返回 ""。已计费的失败（流中断等）不折叠：
// 要保留 request_id 与消费记录互查。
// message 不参与签名——同类错误报文常含时间戳/序号等噪声。
func foldKey(e Entry) string {
	if e.Billed {
		return ""
	}
	return strings.Join([]string{
		e.Source, e.Phase, strconv.Itoa(e.StatusCode), e.ErrorType, e.ErrorCode,
		e.Model, e.Endpoint,
		strconv.Itoa(e.ChannelID), strconv.Itoa(e.UserID), strconv.Itoa(e.APIKeyID),
	}, "\x1f")
}

// flush 批量落库；失败即丢（诊断数据允许有损，绝不重试拖垮 DB）。
// 同签名失败折叠：命中窗口内缓存 → 对首行 repeat_count 自增；批内重复 → 合并成一行。
// 代价是被折叠行丢失各自的 request_id / 重试链（保留首行的），风暴排障够用。
func (r *Recorder) flush(ctx context.Context, batch []Entry) {
	now := time.Now()
	r.pruneFolds(now)

	type pendingRow struct {
		entry  Entry
		key    string
		repeat int
	}
	increments := make(map[int]int)
	pending := make([]pendingRow, 0, len(batch))
	batchIdx := make(map[string]int)
	for _, e := range batch {
		key := foldKey(e)
		if key != "" {
			if ref, ok := r.folds[key]; ok && now.Before(ref.until) {
				increments[ref.id]++
				continue
			}
			if idx, ok := batchIdx[key]; ok {
				pending[idx].repeat++
				continue
			}
			batchIdx[key] = len(pending)
		}
		pending = append(pending, pendingRow{entry: e, key: key, repeat: 1})
	}

	if len(pending) > 0 {
		builders := make([]*ent.UpstreamRequestLogCreate, 0, len(pending))
		for _, p := range pending {
			builders = append(builders, r.entryCreate(p.entry, p.repeat))
		}
		rows, err := r.db.UpstreamRequestLog.CreateBulk(builders...).Save(ctx)
		if err != nil {
			slog.Warn("errlog_flush_failed", "count", len(pending), "error", err)
		} else {
			for i, row := range rows {
				if key := pending[i].key; key != "" && len(r.folds) < foldCacheMax {
					r.folds[key] = foldRef{id: row.ID, until: now.Add(foldWindow)}
				}
			}
		}
	}

	for id, n := range increments {
		if err := r.db.UpstreamRequestLog.UpdateOneID(id).AddRepeatCount(n).Exec(ctx); err != nil {
			// 首行可能已被 TTL 清理；折叠计数属诊断数据，丢弃即可。
			slog.Warn("errlog_fold_increment_failed", "id", id, "count", n, "error", err)
		}
	}
}

// pruneFolds 清理过期折叠缓存。
func (r *Recorder) pruneFolds(now time.Time) {
	for k, ref := range r.folds {
		if !now.Before(ref.until) {
			delete(r.folds, k)
		}
	}
}

func (r *Recorder) entryCreate(e Entry, repeat int) *ent.UpstreamRequestLogCreate {
	var chain json.RawMessage
	if len(e.Chain) > 0 {
		if data, err := json.Marshal(e.Chain); err == nil {
			chain = data
		}
	}
	b := r.db.UpstreamRequestLog.Create().
		SetRequestID(e.RequestID).
		SetSource(entupstreamrequestlog.Source(e.Source)).
		SetPhase(e.Phase).
		SetStatusCode(e.StatusCode).
		SetErrorType(e.ErrorType).
		SetErrorCode(e.ErrorCode).
		SetMessage(e.Message).
		SetAttempts(e.Attempts).
		SetBilled(e.Billed).
		SetModel(e.Model).
		SetEndpoint(e.Endpoint).
		SetStream(e.Stream).
		SetUserID(e.UserID).
		SetUserEmailSnapshot(e.UserEmail).
		SetAPIKeyID(e.APIKeyID).
		SetGroupID(e.GroupID).
		SetChannelID(e.ChannelID).
		SetChannelName(e.ChannelName).
		SetIPAddress(e.IPAddress).
		SetUserAgent(Sanitize(e.UserAgent, 512)).
		SetDurationMs(e.DurationMs).
		SetRepeatCount(repeat)
	if chain != nil {
		b.SetAttemptChain(chain)
	}
	return b
}

// cleanupLoop TTL 清理：每 6h 分批删除 30 天前的记录。
func (r *Recorder) cleanupLoop() {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			r.cleanup(context.Background())
		case <-r.stopCh:
			return
		}
	}
}

func (r *Recorder) cleanup(ctx context.Context) {
	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	total := 0
	for {
		ids, err := r.db.UpstreamRequestLog.Query().
			Where(entupstreamrequestlog.CreatedAtLT(cutoff)).
			Limit(cleanupBatch).
			IDs(ctx)
		if err != nil || len(ids) == 0 {
			break
		}
		n, err := r.db.UpstreamRequestLog.Delete().
			Where(entupstreamrequestlog.IDIn(ids...)).
			Exec(ctx)
		if err != nil {
			slog.Warn("errlog_cleanup_failed", "error", err)
			break
		}
		total += n
		if n < cleanupBatch {
			break
		}
		time.Sleep(200 * time.Millisecond) // 批间歇，避免持续占用
	}
	if total > 0 {
		slog.Info("errlog_cleanup_done", "deleted", total, "retention_days", retentionDays)
	}
}
