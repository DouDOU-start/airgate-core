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
	defaultBufferSize = 1000            // 内存 channel 缓冲大小
	batchSize         = 100             // 批量写入阈值
	flushInterval     = 5 * time.Second // 定时刷新间隔
	maxRetries        = 3               // 写入失败最大重试次数
)

// UsageRecord 使用记录
type UsageRecord struct {
	UserID                int
	APIKeyID              int
	AccountID             int
	GroupID               int
	Platform              string
	Model                 string
	InputTokens           int
	OutputTokens          int
	CachedInputTokens     int
	CacheCreationTokens   int
	CacheCreation5mTokens int
	CacheCreation1hTokens int
	ReasoningOutputTokens int
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
	AccountCost           float64 // 账号实际成本（仅服务"账号计费"统计）
	RateMultiplier        float64 // 快照：本次生效的平台计费倍率
	SellRate              float64 // 快照：本次生效的销售倍率（0 表示未启用 markup）
	AccountRateMultiplier float64 // 快照：本次生效的 account_rate
	ServiceTier           string
	Stream                bool
	DurationMs            int64
	FirstTokenMs          int64
	UserAgent             string
	IPAddress             string
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
}

// NewRecorder 创建使用量记录器
func NewRecorder(db *ent.Client, bufferSize int) *Recorder {
	if bufferSize <= 0 {
		bufferSize = defaultBufferSize
	}
	return &Recorder{
		db:      db,
		ch:      make(chan UsageRecord, bufferSize),
		stopCh:  make(chan struct{}),
		stopped: make(chan struct{}),
	}
}

// Record 提交使用记录（非阻塞）
func (r *Recorder) Record(record UsageRecord) {
	select {
	case r.ch <- record:
	default:
		slog.Warn("使用量记录缓冲已满，丢弃记录",
			"user_id", record.UserID,
			"model", record.Model,
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
			// 停止前处理剩余数据
			close(r.ch)
			for rec := range r.ch {
				batch = append(batch, rec)
			}
			if len(batch) > 0 {
				r.flush(ctx, batch)
			}
			return
		}
	}
}

// flush 批量写入数据库，失败时重试
func (r *Recorder) flush(ctx context.Context, batch []UsageRecord) {
	for attempt := 0; attempt < maxRetries; attempt++ {
		if err := r.batchInsert(ctx, batch); err != nil {
			slog.Error("批量写入使用记录失败",
				"attempt", attempt+1,
				"count", len(batch),
				"error", err,
			)
			if attempt < maxRetries-1 {
				time.Sleep(time.Duration(attempt+1) * time.Second)
				continue
			}
			slog.Error("批量写入使用记录最终失败，丢弃数据", "count", len(batch))
			return
		}
		slog.Debug("批量写入使用记录成功", "count", len(batch))
		return
	}
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
		b := tx.UsageLog.Create().
			SetPlatform(rec.Platform).
			SetModel(rec.Model).
			SetInputTokens(rec.InputTokens).
			SetOutputTokens(rec.OutputTokens).
			SetCachedInputTokens(rec.CachedInputTokens).
			SetCacheCreationTokens(rec.CacheCreationTokens).
			SetCacheCreation5mTokens(rec.CacheCreation5mTokens).
			SetCacheCreation1hTokens(rec.CacheCreation1hTokens).
			SetReasoningOutputTokens(rec.ReasoningOutputTokens).
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
			SetAccountCost(rec.AccountCost).
			SetRateMultiplier(rec.RateMultiplier).
			SetSellRate(rec.SellRate).
			SetAccountRateMultiplier(rec.AccountRateMultiplier).
			SetServiceTier(rec.ServiceTier).
			SetStream(rec.Stream).
			SetDurationMs(rec.DurationMs).
			SetFirstTokenMs(rec.FirstTokenMs).
			SetUserAgent(rec.UserAgent).
			SetIPAddress(rec.IPAddress).
			SetUserID(rec.UserID).
			SetAccountID(rec.AccountID).
			SetGroupID(rec.GroupID)
		if rec.APIKeyID > 0 {
			b.SetAPIKeyID(rec.APIKeyID)
		}
		builders = append(builders, b)
	}

	if _, err := tx.UsageLog.CreateBulk(builders...).Save(ctx); err != nil {
		return fmt.Errorf("批量插入 UsageLog 失败: %w", err)
	}

	// 2. 在同一事务中扣费 —— 三个独立累加器：
	//    - User.balance：按 actual_cost 扣减（平台真实成本，永远不受 sell_rate 影响）
	//    - APIKey.used_quota：按 billed_cost 累加（客户账面值，对 end customer 可见）
	//    - APIKey.used_quota_actual：按 actual_cost 累加（reseller 成本核算用，end customer 不可见）
	//    sell_rate=0 时 billed_cost==actual_cost，used_quota 与 used_quota_actual 相等，行为完全等价于改造前。
	userActualCosts := make(map[int]float64)
	keyBilledCosts := make(map[int]float64)
	keyActualCosts := make(map[int]float64)

	for _, rec := range batch {
		if rec.ActualCost > 0 {
			userActualCosts[rec.UserID] += rec.ActualCost
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
			return fmt.Errorf("扣减用户余额失败 user_id=%d cost=%.8f: %w", userID, cost, err)
		}
	}

	// APIKey 双累加器：billed 和 actual 都更新（key 集合相同，合并一次 update 调用）
	// APIKeyID == 0 表示 Host.Forward 发起的请求（无 API Key），跳过 APIKey 累加。
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
			return fmt.Errorf("更新 API Key 用量失败 key_id=%d: %w", keyID, err)
		}
	}

	// 3. 提交事务
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交事务失败: %w", err)
	}
	return nil
}
