package billing

import (
	"context"
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
	CacheTokens           int
	InputCost             float64
	OutputCost            float64
	CacheCost             float64
	TotalCost             float64
	ActualCost            float64
	RateMultiplier        float64
	AccountRateMultiplier float64
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

// batchInsert 使用 Ent 批量创建
func (r *Recorder) batchInsert(ctx context.Context, batch []UsageRecord) error {
	builders := make([]*ent.UsageLogCreate, 0, len(batch))
	for _, rec := range batch {
		b := r.db.UsageLog.Create().
			SetPlatform(rec.Platform).
			SetModel(rec.Model).
			SetInputTokens(rec.InputTokens).
			SetOutputTokens(rec.OutputTokens).
			SetCacheTokens(rec.CacheTokens).
			SetInputCost(rec.InputCost).
			SetOutputCost(rec.OutputCost).
			SetCacheCost(rec.CacheCost).
			SetTotalCost(rec.TotalCost).
			SetActualCost(rec.ActualCost).
			SetRateMultiplier(rec.RateMultiplier).
			SetAccountRateMultiplier(rec.AccountRateMultiplier).
			SetStream(rec.Stream).
			SetDurationMs(rec.DurationMs).
			SetFirstTokenMs(rec.FirstTokenMs).
			SetUserAgent(rec.UserAgent).
			SetIPAddress(rec.IPAddress).
			SetUserID(rec.UserID).
			SetAPIKeyID(rec.APIKeyID).
			SetAccountID(rec.AccountID).
			SetGroupID(rec.GroupID)
		builders = append(builders, b)
	}

	_, err := r.db.UsageLog.CreateBulk(builders...).Save(ctx)
	return err
}
