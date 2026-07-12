package billing

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/DouDOU-start/airgate-core/ent"
)

const (
	// defaultBufferSize 内存 channel 缓冲大小。计费记录不可丢：flush 整批失败时
	// 重试退避（最长 1s+2s）会阻塞唯一的消费 goroutine，缓冲须扛住
	// 「DB 抖动窗口 × 峰值请求速率」——按 1000 req/s × 100s 余量取 10 万条
	// （每条几百字节，满载约几十 MB，可接受）。
	defaultBufferSize = 100_000
	// batchSize 批量写入阈值。usage_logs 约 40 列，500 条 ≈ 2 万绑定参数，
	// 远低于 PostgreSQL 65535 上限；批越大高峰期事务次数越少。
	batchSize     = 500
	flushInterval = 5 * time.Second // 定时刷新间隔
	maxRetries    = 3               // 写入失败最大重试次数
)

// UsageRecord 使用记录
type UsageRecord struct {
	UserID                int
	UserEmail             string
	APIKeyID              int
	ChannelID             int
	ChannelKeyID          int
	GroupID               int
	Model                 string
	InputTokens           int
	OutputTokens          int
	CachedInputTokens     int
	CacheCreationTokens   int
	CacheCreation5mTokens int
	CacheCreation1hTokens int
	Calls                 int // 按次计费计次数（图像端点=响应产出张数）；token 计费端点恒 0
	InputPrice            float64
	OutputPrice           float64
	CachedInputPrice      float64
	CacheCreationPrice    float64
	CacheCreation1hPrice  float64
	InputCost             float64
	OutputCost            float64
	CachedInputCost       float64
	CacheCreationCost     float64
	TotalCost             float64
	ActualCost            float64 // 平台真实成本（扣 reseller 余额）
	BilledCost            float64 // 客户账面消耗（累加到 APIKey.used_quota）
	RateMultiplier        float64 // 快照：本次生效的平台计费倍率
	SellRate              float64 // 快照：本次生效的销售倍率（0 表示未启用 markup）
	AccountRateMultiplier float64 // 快照：本次生效的渠道成本倍率（渠道成本查询期现算）
	ServiceTier           string
	Stream                bool
	DurationMs            int64
	FirstTokenMs          int64
	UserAgent             string
	IPAddress             string
	Endpoint              string
	Source                string // 记账来源：relay（默认）/ channel_test / task；空值落库归一为 relay
	RequestID             string // X-Request-ID：与 upstream_request_logs 互查
	// SkipBalanceCharge 跳过 user.balance 扣减：异步任务的余额动账已在
	// 预扣/结算阶段同步完成，落账只记 usage_log 与 key 用量累加，
	// 再扣一次会双重计费。
	SkipBalanceCharge bool
}

// 记账来源常量（usage_logs.source）。
const (
	SourceRelay       = "relay"        // 用户转发流量
	SourceChannelTest = "channel_test" // 渠道测试（管理员操作，无用户归属）
	SourceTask        = "task"         // 异步任务（视频/音乐）终态结算
)

// normalizedSource 空来源归一为 relay（防御性缺省：调用方漏填来源时不落空值）。
func normalizedSource(source string) string {
	if source == "" {
		return SourceRelay
	}
	return source
}

// Recorder 异步记录器
// 使用 channel 缓冲，goroutine 批量写入
// 每 100 条或每 5 秒 flush 一次
type Recorder struct {
	db      *ent.Client
	ch      chan UsageRecord
	stopCh  chan struct{}
	stopped chan struct{}
	once    sync.Once

	// insertBatch / insertOne 落库函数，可注入以便测试降级逻辑（默认真实实现）。
	insertBatch func(ctx context.Context, batch []UsageRecord) error
	insertOne   func(ctx context.Context, rec UsageRecord, withChannel bool) error
	// sleep 重试间隔等待，可注入以便测试（默认 time.Sleep）。
	sleep func(d time.Duration)
	// onBalanceCharged 扣费事务提交后触发，传入本次扣减了余额的用户 ID（用于余额预警检查）。
	// 经 func 注入避免 billing 依赖 app 包；由装配层接到 user 服务。
	onBalanceCharged func(userIDs []int)
}

// SetBalanceChargedHook 设置扣费后回调（事务提交成功后异步触发余额预警检查）。
func (r *Recorder) SetBalanceChargedHook(fn func(userIDs []int)) {
	r.onBalanceCharged = fn
}

// fireBalanceCharged 通知扣费回调（提交成功后调用；回调内部自行异步，不阻塞记账）。
func (r *Recorder) fireBalanceCharged(userIDs []int) {
	if r.onBalanceCharged == nil || len(userIDs) == 0 {
		return
	}
	r.onBalanceCharged(userIDs)
}

// NewRecorder 创建使用量记录器
func NewRecorder(db *ent.Client, bufferSize int) *Recorder {
	if bufferSize <= 0 {
		bufferSize = defaultBufferSize
	}
	r := &Recorder{
		db:      db,
		ch:      make(chan UsageRecord, bufferSize),
		stopCh:  make(chan struct{}),
		stopped: make(chan struct{}),
		sleep:   time.Sleep,
	}
	r.insertBatch = r.batchInsert
	r.insertOne = r.insertSingle
	return r
}

// Record 提交使用记录（非阻塞）。缓冲满时丢弃该条并记 ERROR 级日志
// （带 user_id/model/cost 供对账补录）——保持非阻塞语义，不反压转发热路径。
func (r *Recorder) Record(record UsageRecord) {
	select {
	case r.ch <- record:
	default:
		slog.Error("billing_record_buffer_full_dropped",
			"user_id", record.UserID,
			"api_key_id", record.APIKeyID,
			"model", record.Model,
			"total_cost", record.TotalCost,
			"actual_cost", record.ActualCost,
			"billed_cost", record.BilledCost,
			"request_id", record.RequestID,
		)
	}
}

// Start 启动后台写入 goroutine
func (r *Recorder) Start() {
	go r.run()
}

// Stop 停止写入，等待缓冲区清空
func (r *Recorder) Stop() {
	r.once.Do(func() {
		close(r.stopCh)
		<-r.stopped
	})
}

// run 后台运行循环
func (r *Recorder) run() {
	defer close(r.stopped)

	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()

	batch := make([]UsageRecord, 0, batchSize)
	ctx := context.Background()

	for {
		select {
		case rec := <-r.ch:
			batch = append(batch, rec)
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
			// close 会令其 send-on-closed-channel panic（计费记录直接丢失）；
			// 排空按 batchSize 分片，避免单事务巨批。
			for {
				select {
				case rec := <-r.ch:
					batch = append(batch, rec)
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

// flush 批量写入数据库：整批失败重试 maxRetries 次后降级为逐条写入，
// 单条坏记录（如渠道已删除触发 FK 违约）不再放大为整批计费丢失。
func (r *Recorder) flush(ctx context.Context, batch []UsageRecord) {
	for attempt := 0; attempt < maxRetries; attempt++ {
		if err := r.insertBatch(ctx, batch); err != nil {
			slog.Error("billing_batch_flush_failed",
				"attempt", attempt+1,
				"count", len(batch),
				"error", err,
			)
			if attempt < maxRetries-1 {
				r.sleep(time.Duration(attempt+1) * time.Second)
			}
			continue
		}
		slog.Debug("billing_batch_flush_succeeded", "count", len(batch))
		return
	}
	r.flushOneByOne(ctx, batch)
}

// flushOneByOne 整批重试耗尽后的降级路径：逐条插入；单条因约束冲突失败
// （渠道硬删除后新插入引用悬空 id 触发 FK 违约）再降级为去掉 channel 边重插，
// 保证计费金额与余额扣款绝不因单条坏记录整批丢失。
func (r *Recorder) flushOneByOne(ctx context.Context, batch []UsageRecord) {
	dropped := 0
	for _, rec := range batch {
		err := r.insertOne(ctx, rec, true)
		if err == nil {
			continue
		}
		if ent.IsConstraintError(err) {
			retryErr := r.insertOne(ctx, rec, false)
			if retryErr == nil {
				slog.Warn("billing_record_channel_edge_cleared",
					"user_id", rec.UserID,
					"channel_id", rec.ChannelID,
					"model", rec.Model,
				)
				continue
			}
			err = retryErr
		}
		dropped++
		slog.Error("billing_record_dropped",
			"user_id", rec.UserID,
			"channel_id", rec.ChannelID,
			"model", rec.Model,
			"error", err,
		)
	}
	if dropped > 0 {
		slog.Error("billing_batch_flush_dropped", "count", dropped)
	}
}

// insertSingle 单条写入（UsageLog + 扣费同事务）；withChannel=false 时不设 channel 边。
func (r *Recorder) insertSingle(ctx context.Context, rec UsageRecord, withChannel bool) error {
	tx, err := r.db.Tx(ctx)
	if err != nil {
		return fmt.Errorf("开启事务失败: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if _, err := usageLogCreate(tx, rec, withChannel).Save(ctx); err != nil {
		return fmt.Errorf("插入 UsageLog 失败: %w", err)
	}
	chargedIDs, err := applyUsageCharges(ctx, tx, []UsageRecord{rec})
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交事务失败: %w", err)
	}
	r.fireBalanceCharged(chargedIDs)
	return nil
}

// batchInsert 在同一事务中批量写入使用记录并扣费
// 保证 UsageLog 插入与余额扣减的原子性，避免记录成功但扣费失败
func (r *Recorder) batchInsert(ctx context.Context, batch []UsageRecord) error {
	tx, err := r.db.Tx(ctx)
	if err != nil {
		return fmt.Errorf("开启事务失败: %w", err)
	}
	defer func() {
		// 若事务未提交则回滚（Commit 后 Rollback 是 no-op）
		_ = tx.Rollback()
	}()

	// 1. 批量写入 UsageLog（同时记录 actual_cost 和 billed_cost 双轨数据）
	builders := make([]*ent.UsageLogCreate, 0, len(batch))
	for _, rec := range batch {
		builders = append(builders, usageLogCreate(tx, rec, true))
	}

	if _, err := tx.UsageLog.CreateBulk(builders...).Save(ctx); err != nil {
		return fmt.Errorf("批量插入 UsageLog 失败: %w", err)
	}

	chargedIDs, err := applyUsageCharges(ctx, tx, batch)
	if err != nil {
		return err
	}

	// 3. 提交事务
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交事务失败: %w", err)
	}
	r.fireBalanceCharged(chargedIDs)
	return nil
}

// usageLogCreate 构建 UsageLog 插入语句；withChannel=false 时跳过 channel 边
// （渠道已删除时 FK 违约的降级重插路径）。
func usageLogCreate(tx *ent.Tx, rec UsageRecord, withChannel bool) *ent.UsageLogCreate {
	b := tx.UsageLog.Create().
		SetModel(rec.Model).
		SetInputTokens(rec.InputTokens).
		SetOutputTokens(rec.OutputTokens).
		SetCachedInputTokens(rec.CachedInputTokens).
		SetCacheCreationTokens(rec.CacheCreationTokens).
		SetCacheCreation5mTokens(rec.CacheCreation5mTokens).
		SetCacheCreation1hTokens(rec.CacheCreation1hTokens).
		SetCalls(rec.Calls).
		SetInputPrice(rec.InputPrice).
		SetOutputPrice(rec.OutputPrice).
		SetCachedInputPrice(rec.CachedInputPrice).
		SetCacheCreationPrice(rec.CacheCreationPrice).
		SetCacheCreation1hPrice(rec.CacheCreation1hPrice).
		SetInputCost(rec.InputCost).
		SetOutputCost(rec.OutputCost).
		SetCachedInputCost(rec.CachedInputCost).
		SetCacheCreationCost(rec.CacheCreationCost).
		SetTotalCost(rec.TotalCost).
		SetActualCost(rec.ActualCost).
		SetBilledCost(rec.BilledCost).
		SetRateMultiplier(rec.RateMultiplier).
		SetSellRate(rec.SellRate).
		SetAccountRateMultiplier(rec.AccountRateMultiplier).
		SetServiceTier(rec.ServiceTier).
		SetStream(rec.Stream).
		SetDurationMs(rec.DurationMs).
		SetFirstTokenMs(rec.FirstTokenMs).
		SetUserAgent(rec.UserAgent).
		SetIPAddress(rec.IPAddress).
		SetEndpoint(rec.Endpoint).
		SetSource(normalizedSource(rec.Source)).
		SetRequestID(rec.RequestID).
		SetUserIDSnapshot(rec.UserID).
		SetUserEmailSnapshot(rec.UserEmail)
	// 无归属记录（渠道测试）不设 user/group/api_key/channel 边：
	// Optional FK 写 0 会触发外键违约。
	if rec.UserID > 0 {
		b.SetUserID(rec.UserID)
	}
	if rec.GroupID > 0 {
		b.SetGroupID(rec.GroupID)
	}
	if withChannel && rec.ChannelID > 0 {
		b.SetChannelID(rec.ChannelID)
	}
	if withChannel && rec.ChannelKeyID > 0 {
		b.SetChannelKeyID(rec.ChannelKeyID)
	}
	if rec.APIKeyID > 0 {
		b.SetAPIKeyID(rec.APIKeyID)
	}
	return b
}

// applyUsageCharges 在事务内扣费，返回实际扣减了 user.balance 的用户 ID 列表
// （供事务提交后触发余额预警检查；SkipBalanceCharge 记录不计入）。
func applyUsageCharges(ctx context.Context, tx *ent.Tx, batch []UsageRecord) ([]int, error) {
	// 在同一事务中扣费 —— 三个独立累加器：
	// - User.balance：按 actual_cost 扣减。
	// - APIKey.used_quota：按 billed_cost 累加。
	// - APIKey.used_quota_actual：按 actual_cost 累加。
	userActualCosts := make(map[int]float64)
	keyBilledCosts := make(map[int]float64)
	keyActualCosts := make(map[int]float64)

	for _, rec := range batch {
		if rec.ActualCost > 0 {
			// SkipBalanceCharge（异步任务）：余额已在预扣/结算同步动账，
			// 此处只累加 key 用量，不再扣 user.balance。
			if !rec.SkipBalanceCharge {
				userActualCosts[rec.UserID] += rec.ActualCost
			}
			if rec.APIKeyID > 0 {
				keyActualCosts[rec.APIKeyID] += rec.ActualCost
			}
		}
		if rec.APIKeyID > 0 && rec.BilledCost > 0 {
			keyBilledCosts[rec.APIKeyID] += rec.BilledCost
		}
	}

	for userID, cost := range userActualCosts {
		if err := tx.User.UpdateOneID(userID).
			AddBalance(-cost).
			Exec(ctx); err != nil {
			return nil, fmt.Errorf("扣减用户余额失败 user_id=%d cost=%.8f: %w", userID, cost, err)
		}
	}

	// APIKey 双累加器：billed 和 actual 都更新（key 集合相同，合并一次 update 调用）
	// APIKeyID == 0 表示渠道测试等无用户归属的记录（无 API Key），跳过 APIKey 累加。
	keyIDs := make(map[int]struct{}, len(keyBilledCosts))
	for k := range keyBilledCosts {
		keyIDs[k] = struct{}{}
	}
	for k := range keyActualCosts {
		keyIDs[k] = struct{}{}
	}
	for keyID := range keyIDs {
		if keyID == 0 {
			continue
		}
		update := tx.APIKey.UpdateOneID(keyID)
		if billed := keyBilledCosts[keyID]; billed > 0 {
			update = update.AddUsedQuota(billed)
		}
		if actual := keyActualCosts[keyID]; actual > 0 {
			update = update.AddUsedQuotaActual(actual)
		}
		if err := update.Exec(ctx); err != nil {
			return nil, fmt.Errorf("更新 API Key 用量失败 key_id=%d: %w", keyID, err)
		}
	}
	chargedUserIDs := make([]int, 0, len(userActualCosts))
	for uid := range userActualCosts {
		chargedUserIDs = append(chargedUserIDs, uid)
	}
	return chargedUserIDs, nil
}
