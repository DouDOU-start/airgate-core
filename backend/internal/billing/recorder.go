package billing

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/DouDOU-start/airgate-core/ent"
	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"
)

const (
	defaultBufferSize = 1000            // 内存 channel 缓冲大小
	batchSize         = 100             // 批量写入阈值
	flushInterval     = 5 * time.Second // 定时刷新间隔
	maxRetries        = 3               // 写入失败最大重试次数
)

// UsageRecord 使用记录
type UsageRecord struct {
	UserID                       int
	UserEmail                    string
	APIKeyID                     int
	AccountID                    int
	GroupID                      int
	Platform                     string
	Model                        string
	InputTokens                  int
	OutputTokens                 int
	CachedInputTokens            int
	CacheCreationTokens          int
	CacheCreation5mTokens        int
	CacheCreation1hTokens        int
	ReasoningOutputTokens        int
	InputPrice                   float64
	OutputPrice                  float64
	CachedInputPrice             float64
	CacheCreationPrice           float64
	CacheCreation1hPrice         float64
	InputCost                    float64
	OutputCost                   float64
	CachedInputCost              float64
	CacheCreationCost            float64
	ImageCost                    float64
	ImageFixedPriceApplied       bool
	ImageFixedPriceReplacesTotal bool
	TotalCost                    float64
	ActualCost                   float64 // 平台真实成本（扣 reseller 余额）
	BilledCost                   float64 // 客户账面消耗（累加到 APIKey.used_quota）
	AccountCost                  float64 // 账号实际成本（仅服务"账号计费"统计）
	RateMultiplier               float64 // 快照：本次生效的平台计费倍率
	SellRate                     float64 // 快照：本次生效的销售倍率（0 表示未启用 markup）
	AccountRateMultiplier        float64 // 快照：本次生效的 account_rate
	ServiceTier                  string
	ImageSize                    string // 图像生成请求的实际出图尺寸（"WxH"），非图像请求留空
	Stream                       bool
	DurationMs                   int64
	FirstTokenMs                 int64
	UserAgent                    string
	IPAddress                    string
	Endpoint                     string
	ReasoningEffort              string
	UsageAttributes              []sdk.UsageAttribute
	UsageMetrics                 []sdk.UsageMetric
	UsageCostDetails             []sdk.UsageCostDetail
	UsageMetadata                map[string]string
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
		slog.Warn("billing_record_buffer_full",
			"user_id", record.UserID,
			"model", record.Model,
		)
	}
}

// RecordSync 同步写入一条使用记录并返回 usage_log.id。
// 需要立即把 usage_id 关联到任务时使用；普通转发仍走异步 Record。
func (r *Recorder) RecordSync(ctx context.Context, record UsageRecord) (int, error) {
	tx, err := r.db.Tx(ctx)
	if err != nil {
		return 0, fmt.Errorf("开启事务失败: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	log, err := usageLogCreate(tx, record).Save(ctx)
	if err != nil {
		return 0, fmt.Errorf("插入 UsageLog 失败: %w", err)
	}
	if err := applyUsageCharges(ctx, tx, []UsageRecord{record}); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("提交事务失败: %w", err)
	}
	return log.ID, nil
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
			slog.Error("billing_batch_flush_failed",
				"attempt", attempt+1,
				"count", len(batch),
				"error", err,
			)
			if attempt < maxRetries-1 {
				time.Sleep(time.Duration(attempt+1) * time.Second)
				continue
			}
			slog.Error("billing_batch_flush_dropped", "count", len(batch))
			return
		}
		slog.Debug("billing_batch_flush_succeeded", "count", len(batch))
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
		builders = append(builders, usageLogCreate(tx, rec))
	}

	if _, err := tx.UsageLog.CreateBulk(builders...).Save(ctx); err != nil {
		return fmt.Errorf("批量插入 UsageLog 失败: %w", err)
	}

	if err := applyUsageCharges(ctx, tx, batch); err != nil {
		return err
	}

	// 3. 提交事务
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交事务失败: %w", err)
	}
	return nil
}

func usageLogCreate(tx *ent.Tx, rec UsageRecord) *ent.UsageLogCreate {
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
		SetImageCost(rec.ImageCost).
		SetTotalCost(rec.TotalCost).
		SetActualCost(rec.ActualCost).
		SetBilledCost(rec.BilledCost).
		SetAccountCost(rec.AccountCost).
		SetRateMultiplier(rec.RateMultiplier).
		SetSellRate(rec.SellRate).
		SetAccountRateMultiplier(rec.AccountRateMultiplier).
		SetServiceTier(rec.ServiceTier).
		SetImageSize(rec.ImageSize).
		SetStream(rec.Stream).
		SetDurationMs(rec.DurationMs).
		SetFirstTokenMs(rec.FirstTokenMs).
		SetUserAgent(rec.UserAgent).
		SetIPAddress(rec.IPAddress).
		SetEndpoint(rec.Endpoint).
		SetReasoningEffort(rec.ReasoningEffort).
		SetUsageAttributes(rec.UsageAttributes).
		SetUsageMetrics(rec.UsageMetrics).
		SetUsageCostDetails(enrichUsageCostDetails(rec)).
		SetUsageMetadata(rec.UsageMetadata).
		SetUserIDSnapshot(rec.UserID).
		SetUserEmailSnapshot(rec.UserEmail).
		SetUserID(rec.UserID).
		SetAccountID(rec.AccountID).
		SetGroupID(rec.GroupID)
	if rec.APIKeyID > 0 {
		b.SetAPIKeyID(rec.APIKeyID)
	}
	return b
}

func enrichUsageCostDetails(rec UsageRecord) []sdk.UsageCostDetail {
	if len(rec.UsageCostDetails) == 0 {
		return rec.UsageCostDetails
	}

	items := make([]sdk.UsageCostDetail, len(rec.UsageCostDetails))
	copy(items, rec.UsageCostDetails)

	var imageCostSum float64
	var imageInputCostSum float64
	for _, item := range items {
		if item.AccountCost > 0 && isImageCostDetail(item) {
			imageCostSum += item.AccountCost
		}
		if item.AccountCost > 0 && isImageInputCostDetail(item) {
			imageInputCostSum += item.AccountCost
		}
	}
	rate := rec.RateMultiplier
	if rate <= 0 {
		rate = 1
	}
	nonImageBaseCost := rec.InputCost + rec.OutputCost + rec.CachedInputCost + rec.CacheCreationCost
	expectedTokenActualCost := (nonImageBaseCost + imageInputCostSum + imageCostSum) * rate
	fixedImagePricing := rec.ImageFixedPriceApplied || (imageCostSum > 0 && math.Abs(rec.ActualCost-expectedTokenActualCost) > 1e-9)
	imageUserCost := imageCostSum * rate
	if fixedImagePricing {
		imageUserCost = rec.ActualCost
		if !rec.ImageFixedPriceReplacesTotal {
			imageUserCost = rec.ActualCost - nonImageBaseCost*rate
			if imageUserCost < 0 {
				imageUserCost = 0
			}
		}
	}

	for i := range items {
		accountCost := items[i].AccountCost
		if accountCost <= 0 {
			items[i].BillingMultiplier = rate
			continue
		}
		if fixedImagePricing && isImageInputCostDetail(items[i]) {
			items[i].BillingMultiplier = 0
			items[i].UserCost = 0
			continue
		}
		if fixedImagePricing && rec.ImageFixedPriceReplacesTotal && !isImageCostDetail(items[i]) {
			items[i].BillingMultiplier = 0
			items[i].UserCost = 0
			continue
		}
		if isImageCostDetail(items[i]) {
			if imageCostSum > 0 && (fixedImagePricing || imageUserCost > 0) {
				if imageUserCost > 0 {
					items[i].UserCost = imageUserCost * accountCost / imageCostSum
					items[i].BillingMultiplier = items[i].UserCost / accountCost
				} else {
					items[i].UserCost = 0
					items[i].BillingMultiplier = 0
				}
				items[i].Metadata = cloneCostMetadata(items[i].Metadata)
				if fixedImagePricing {
					items[i].Metadata["billing_mode"] = "fixed_image_price"
					if imageCount := parseCostMetadataPositiveInt(items[i].Metadata, "image_count"); imageCount > 0 {
						items[i].Metadata["fixed_unit_price"] = fmt.Sprintf("%.10g", items[i].UserCost/float64(imageCount))
						items[i].Metadata["fixed_unit"] = "USD/image"
					}
				} else {
					items[i].Metadata["billing_mode"] = "image_token"
				}
			} else {
				items[i].BillingMultiplier = rate
				items[i].UserCost = accountCost * rate
			}
			continue
		}
		items[i].BillingMultiplier = rate
		items[i].UserCost = accountCost * rate
	}

	if !fixedImagePricing {
		return mergeImageTokenCostDetails(items)
	}

	return items
}

func isImageCostDetail(item sdk.UsageCostDetail) bool {
	key := strings.ToLower(strings.TrimSpace(item.Key))
	key = strings.ReplaceAll(key, "-", "_")
	if strings.Contains(key, "input") {
		return false
	}
	switch key {
	case "image", "images", "image_generation", "image_tool", "image_output", "image_outputs", "image_output_tokens":
		return true
	default:
		return strings.Contains(key, "image")
	}
}

func isImageInputCostDetail(item sdk.UsageCostDetail) bool {
	key := strings.ToLower(strings.TrimSpace(item.Key))
	key = strings.ReplaceAll(key, "-", "_")
	label := strings.ToLower(strings.TrimSpace(item.Label))
	return (strings.Contains(key, "image") || strings.Contains(label, "图片")) &&
		(strings.Contains(key, "input") || strings.Contains(label, "输入"))
}

func mergeImageTokenCostDetails(items []sdk.UsageCostDetail) []sdk.UsageCostDetail {
	merged := make([]sdk.UsageCostDetail, 0, len(items))
	inputIndex := -1
	outputIndex := -1
	for _, item := range items {
		switch {
		case isImageInputCostDetail(item):
			detail := normalizeTokenCostDetail(item, "input_tokens", "输入 Token")
			if inputIndex >= 0 {
				merged[inputIndex] = mergeCostDetail(merged[inputIndex], detail)
			} else {
				inputIndex = len(merged)
				merged = append(merged, detail)
			}
		case isImageCostDetail(item):
			detail := normalizeTokenCostDetail(item, "output_tokens", "输出 Token")
			if outputIndex >= 0 {
				merged[outputIndex] = mergeCostDetail(merged[outputIndex], detail)
			} else {
				outputIndex = len(merged)
				merged = append(merged, detail)
			}
		default:
			detail := item
			if isInputTokenCostDetail(detail) {
				if inputIndex >= 0 {
					merged[inputIndex] = mergeCostDetail(merged[inputIndex], detail)
					continue
				}
				inputIndex = len(merged)
			}
			if isOutputTokenCostDetail(detail) {
				if outputIndex >= 0 {
					merged[outputIndex] = mergeCostDetail(merged[outputIndex], detail)
					continue
				}
				outputIndex = len(merged)
			}
			merged = append(merged, detail)
		}
	}
	return merged
}

func normalizeTokenCostDetail(item sdk.UsageCostDetail, key, label string) sdk.UsageCostDetail {
	item.Key = key
	item.Label = label
	item.Metadata = tokenCostMetadata(item.Metadata)
	return item
}

func mergeCostDetail(base, extra sdk.UsageCostDetail) sdk.UsageCostDetail {
	base.AccountCost += extra.AccountCost
	base.UserCost += extra.UserCost
	if base.Currency == "" {
		base.Currency = extra.Currency
	}
	if len(base.Metadata) == 0 {
		base.Metadata = extra.Metadata
	}
	if base.AccountCost > 0 && base.UserCost > 0 {
		base.BillingMultiplier = base.UserCost / base.AccountCost
	}
	return base
}

func isInputTokenCostDetail(item sdk.UsageCostDetail) bool {
	key := strings.ToLower(strings.TrimSpace(item.Key))
	key = strings.ReplaceAll(key, "-", "_")
	return (key == "input_tokens" || key == "input" || key == "prompt_tokens") && !isImageInputCostDetail(item)
}

func isOutputTokenCostDetail(item sdk.UsageCostDetail) bool {
	key := strings.ToLower(strings.TrimSpace(item.Key))
	key = strings.ReplaceAll(key, "-", "_")
	return (key == "output_tokens" || key == "output" || key == "completion_tokens") && !isImageCostDetail(item)
}

func tokenCostMetadata(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string)
	for _, key := range []string{"unit", "unit_price", "billing_model"} {
		if value := strings.TrimSpace(in[key]); value != "" {
			out[key] = value
		}
	}
	if _, ok := out["unit"]; ok {
		out["unit"] = "USD/1M tokens"
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func cloneCostMetadata(in map[string]string) map[string]string {
	out := make(map[string]string, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	return out
}

func parseCostMetadataPositiveInt(metadata map[string]string, key string) int {
	if len(metadata) == 0 {
		return 0
	}
	value, err := strconv.Atoi(strings.TrimSpace(metadata[key]))
	if err != nil || value <= 0 {
		return 0
	}
	return value
}

func applyUsageCharges(ctx context.Context, tx *ent.Tx, batch []UsageRecord) error {
	// 在同一事务中扣费 —— 三个独立累加器：
	// - User.balance：按 actual_cost 扣减。
	// - APIKey.used_quota：按 billed_cost 累加。
	// - APIKey.used_quota_actual：按 actual_cost 累加。
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
	// APIKeyID == 0 表示插件经 Host 调用发起的请求（无 API Key），跳过 APIKey 累加。
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
	return nil
}
