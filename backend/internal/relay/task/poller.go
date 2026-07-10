package task

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/DouDOU-start/airgate-core/internal/billing"
	"github.com/DouDOU-start/airgate-core/internal/errlog"
	"github.com/DouDOU-start/airgate-core/internal/pkg/upstreamclient"
	"github.com/DouDOU-start/airgate-core/internal/relay/outcome"
	"github.com/DouDOU-start/airgate-core/internal/relay/pipeline"
	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
)

const (
	// pollInterval 轮询节拍。
	pollInterval = 10 * time.Second
	// pollBatchSize 单轮扫描的未完成任务上限（updated_at 升序，长任务不会饿死）。
	pollBatchSize = 100
	// pollChannelConcurrency 渠道组间并发上限。
	pollChannelConcurrency = 4
	// perTaskQueryGap 渠道组内逐任务查询的间隔（对上游限速友好）。
	perTaskQueryGap = 200 * time.Millisecond
	// queryTimeout 单次上游查询超时。
	queryTimeout = 30 * time.Second
	// settleEpsilon 结算差额低于该值不动账（decimal(20,8) 精度以下的浮点噪声）。
	settleEpsilon = 1e-9
)

// Poller 任务后台轮询器：扫未完成任务 → 按渠道分组查上游 → CAS 刷新状态 →
// 终态结算（成功差额多退少补 + 落 usage_log；失败/超时全额退款）。
// 单体部署设计（无分布式租约）；多副本部署需先加租约防重复轮询。
type Poller struct {
	store      Store
	registry   *registry.Registry
	pricing    PriceSource
	calculator *billing.Calculator
	sink       UsageSink
	errSink    ErrSink
	settings   SettingsSource
	balance    BalanceOps
	client     *http.Client

	// now / sleep 可注入以便测试。
	now   func() time.Time
	sleep func(ctx context.Context, d time.Duration)
}

// NewPoller 创建轮询器（依赖与 Flow 同源，共用 Options）。
func NewPoller(opts Options) *Poller {
	settings := opts.Settings
	if settings == nil {
		settings = pipeline.NewSettingsReader(nil)
	}
	calculator := opts.Calculator
	if calculator == nil {
		calculator = billing.NewCalculator()
	}
	return &Poller{
		store:      opts.Store,
		registry:   opts.Registry,
		pricing:    opts.Pricing,
		calculator: calculator,
		sink:       opts.Sink,
		errSink:    opts.ErrLog,
		settings:   settings,
		balance:    opts.Balance,
		client:     upstreamclient.NewClient(0),
		now:        time.Now,
		sleep:      sleepCtx,
	}
}

// Run 阻塞运行轮询循环，ctx 取消即停（由 Server.StartBackground 拉起）。
func (p *Poller) Run(ctx context.Context) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.tick(ctx)
		}
	}
}

// tick 单轮轮询：空转短路 → 扫描 → 超时清扫 → 按渠道分组并发查询。
func (p *Poller) tick(ctx context.Context) {
	n, err := p.store.CountUnfinished(ctx)
	if err != nil {
		slog.Warn("task_poll_count_failed", "error", err)
		return
	}
	if n == 0 {
		return
	}
	tasks, err := p.store.ListUnfinished(ctx, pollBatchSize)
	if err != nil {
		slog.Warn("task_poll_list_failed", "error", err)
		return
	}

	timeout := time.Duration(p.settings.Get(ctx).TaskTimeoutMinutes) * time.Minute
	groups := map[int][]*Task{}
	for _, t := range tasks {
		if p.now().Sub(t.SubmitTime) > timeout {
			p.failTask(ctx, t, "任务超时（超过 "+timeout.String()+" 未完成）", errlog.PhaseTaskTimeout)
			continue
		}
		groups[t.ChannelID] = append(groups[t.ChannelID], t)
	}

	sem := make(chan struct{}, pollChannelConcurrency)
	var wg sync.WaitGroup
	for chID, ts := range groups {
		wg.Add(1)
		sem <- struct{}{}
		go func(chID int, ts []*Task) {
			defer wg.Done()
			defer func() { <-sem }()
			p.pollChannel(ctx, chID, ts)
		}(chID, ts)
	}
	wg.Wait()
}

// pollChannel 单渠道组查询：suno 走批量接口，其余逐任务查询（组内限速）。
func (p *Poller) pollChannel(ctx context.Context, chID int, tasks []*Task) {
	ch, ok := p.registry.Snapshot(chID)
	if !ok {
		// 渠道已删除：任务永远无法向上游查询，置失败退款（而非无限挂起到超时）。
		for _, t := range tasks {
			p.failTask(ctx, t, "任务所属渠道已删除，无法跟踪", errlog.PhaseTaskFailed)
		}
		return
	}
	ad, err := GetAdaptor(ch.Type)
	if err != nil {
		slog.Warn("task_poll_adaptor_missing", "channel_id", chID, "type", ch.Type)
		return
	}
	apiKey := p.registry.NextKey(chID)
	if apiKey == "" {
		slog.Warn("task_poll_channel_no_api_key", "channel_id", chID)
		return
	}
	info := &Info{Channel: ch, APIKey: apiKey, Client: p.client}

	if bq, ok := ad.(BatchQuerying); ok {
		p.pollBatch(ctx, bq, info, tasks)
		return
	}
	for i, t := range tasks {
		if ctx.Err() != nil {
			return
		}
		if i > 0 {
			p.sleep(ctx, perTaskQueryGap)
		}
		info.RequestModel, info.UpstreamModel = t.RequestModel, t.UpstreamModel
		st, authFailed := p.queryOne(ctx, ad, info, t)
		if authFailed {
			// 渠道密钥失效：按同步转发口径自动禁用（开关一致），本轮该渠道剩余任务跳过。
			p.autoBan(ctx, ch, "任务轮询上游认证失败")
			return
		}
		if st != nil {
			p.applyStatus(ctx, t, st)
		}
	}
}

// pollBatch 批量查询路径（suno）：一次带全组 ID，缺席 ID 视为本轮无更新。
func (p *Poller) pollBatch(ctx context.Context, bq BatchQuerying, info *Info, tasks []*Task) {
	ids := make([]string, len(tasks))
	byID := make(map[string]*Task, len(tasks))
	for i, t := range tasks {
		ids[i] = t.TaskID
		byID[t.TaskID] = t
	}
	reqCtx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	httpReq, err := bq.BuildBatchQueryRequest(reqCtx, info, ids)
	if err != nil {
		slog.Warn("task_poll_batch_build_failed", "channel_id", info.Channel.ID, "error", err)
		return
	}
	status, body, netErr := p.doQuery(httpReq)
	o := outcome.Classify(status, nil, body, netErr)
	switch o.Verdict {
	case outcome.Success:
		sts, err := bq.ParseBatchQueryResponse(body)
		if err != nil {
			slog.Warn("task_poll_batch_parse_failed", "channel_id", info.Channel.ID, "error", err)
			return
		}
		for id, st := range sts {
			if t, ok := byID[id]; ok && st != nil {
				p.applyStatus(ctx, t, st)
			}
		}
	case outcome.AuthFailed:
		p.autoBan(ctx, info.Channel, "任务轮询上游认证失败")
	default:
		slog.Warn("task_poll_batch_failed", "channel_id", info.Channel.ID,
			"reason", outcome.SanitizeKeyLeak(o.Reason, info.Channel.APIKeys))
	}
}

// queryOne 单任务上游查询；返回 (归一化状态, 是否认证失败)。
// 非认证类失败（网络错/5xx/解析失败）只记日志等下轮，不判任务失败。
func (p *Poller) queryOne(ctx context.Context, ad Adaptor, info *Info, t *Task) (*Status, bool) {
	reqCtx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	httpReq, err := ad.BuildQueryRequest(reqCtx, info, t.TaskID)
	if err != nil {
		slog.Warn("task_poll_build_failed", "task_id", t.TaskID, "error", err)
		return nil, false
	}
	status, body, netErr := p.doQuery(httpReq)
	o := outcome.Classify(status, nil, body, netErr)
	switch o.Verdict {
	case outcome.Success:
		st, err := ad.ParseQueryResponse(body)
		if err != nil {
			slog.Warn("task_poll_parse_failed", "task_id", t.TaskID, "error", err)
			return nil, false
		}
		return st, false
	case outcome.AuthFailed:
		return nil, true
	default:
		slog.Warn("task_poll_query_failed", "task_id", t.TaskID,
			"reason", outcome.SanitizeKeyLeak(o.Reason, info.Channel.APIKeys))
		return nil, false
	}
}

// doQuery 发出查询请求并读响应（≤4MB）。
func (p *Poller) doQuery(req *http.Request) (int, []byte, error) {
	resp, err := p.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxUpstreamRespBytes))
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, body, nil
}

// autoBan 轮询侧自动禁用（开关与同步转发共用 channel_auto_ban_enabled）。
func (p *Poller) autoBan(ctx context.Context, ch *registry.ChannelSnapshot, reason string) {
	if !p.settings.Get(ctx).AutoBanEnabled {
		return
	}
	p.registry.MarkAutoDisabled(ch.ID, outcome.TruncateErrorMsg(reason))
	if p.errSink != nil {
		p.errSink.CountFailure(context.Background(), ch.ID, "authFailed", "")
	}
}

// applyStatus 把归一化状态写回任务行（CAS），终态触发结算。
func (p *Poller) applyStatus(ctx context.Context, t *Task, st *Status) {
	if st.Status == "" {
		return
	}
	// 无变化跳过写库（终态必写：结算依赖终态行）。
	if st.Status == t.Status && st.Progress == t.Progress && !IsTerminal(st.Status) {
		return
	}
	upd := StatusUpdate{
		Status:     st.Status,
		Progress:   st.Progress,
		FailReason: st.FailReason,
		Seconds:    st.Seconds,
		Data:       truncateData(st.Raw),
	}
	if IsTerminal(st.Status) {
		now := p.now()
		upd.FinishTime = &now
		if st.Status == StatusSuccess {
			upd.Progress = 100
		}
	}
	applied, err := p.store.UpdateStatusCAS(ctx, t.ID, upd)
	if err != nil {
		slog.Warn("task_status_update_failed", "task_id", t.TaskID, "error", err)
		return
	}
	if !applied || !IsTerminal(st.Status) {
		return
	}

	seconds := st.Seconds
	if seconds <= 0 {
		seconds = t.Seconds
	}
	if st.Status == StatusSuccess {
		p.settleSuccess(ctx, t, seconds)
	} else {
		p.refundFailure(ctx, t, st.FailReason, errlog.PhaseTaskFailed)
	}
}

// failTask 超时/渠道消失路径：CAS 置失败 + 退款（幂等）。
func (p *Poller) failTask(ctx context.Context, t *Task, reason, phase string) {
	now := p.now()
	applied, err := p.store.UpdateStatusCAS(ctx, t.ID, StatusUpdate{
		Status:     StatusFailure,
		Progress:   t.Progress,
		FailReason: reason,
		FinishTime: &now,
	})
	if err != nil {
		slog.Warn("task_fail_update_failed", "task_id", t.TaskID, "error", err)
		return
	}
	if !applied {
		return
	}
	p.refundFailure(ctx, t, reason, phase)
}

// settleSuccess 成功结算：按实际时长重算 total → 差额多退少补 → usage_log 落账。
// MarkSettled 为幂等闸：置位成功才动账，重复轮询/重启不双结。
func (p *Poller) settleSuccess(ctx context.Context, t *Task, seconds int) {
	ok, err := p.store.MarkSettled(ctx, t.ID)
	if err != nil || !ok {
		if err != nil {
			slog.Error("task_mark_settled_failed", "task_id", t.TaskID, "error", err)
		}
		return
	}

	finalTotal := t.EstTotal
	price, priced := p.pricing.Get(t.RequestModel)
	perUnitPrice := finalTotal
	calls := 1
	if priced {
		if price.PerRequest > 0 {
			finalTotal = price.PerRequest
			perUnitPrice = price.PerRequest
		} else if price.VideoPerSecond > 0 && seconds > 0 {
			finalTotal = price.VideoPerSecond * float64(seconds)
			perUnitPrice = price.VideoPerSecond
			calls = seconds
		}
	}

	calc := p.calculator.Calculate(billing.CalculateInput{
		InputCost:   finalTotal,
		BillingRate: t.RateMultiplier,
		SellRate:    t.SellRate,
		AccountRate: t.AccountRateMultiplier,
	})

	// 差额动账：hold - final（正数退、负数补扣）；幂等键防重复结算。
	delta := t.HoldAmount - calc.ActualCost
	if math.Abs(delta) > settleEpsilon {
		p.adjust(t, delta, fmt.Sprintf("任务结算 %s %s", t.Platform, t.TaskID),
			fmt.Sprintf("task:settle:%d", t.ID))
	}

	if p.sink == nil {
		return
	}
	durationMs := p.now().Sub(t.SubmitTime).Milliseconds()
	p.sink.Record(billing.UsageRecord{
		UserID:                t.UserID,
		UserEmail:             t.UserEmail,
		APIKeyID:              t.APIKeyID,
		ChannelID:             t.ChannelID,
		GroupID:               t.GroupID,
		Model:                 t.RequestModel,
		Calls:                 calls,
		InputPrice:            perUnitPrice,
		InputCost:             finalTotal,
		TotalCost:             calc.TotalCost,
		ActualCost:            calc.ActualCost,
		BilledCost:            calc.BilledCost,
		RateMultiplier:        calc.RateMultiplier,
		SellRate:              calc.SellRate,
		AccountRateMultiplier: calc.AccountRateMultiplier,
		DurationMs:            durationMs,
		Endpoint:              taskEndpoint(t),
		Source:                billing.SourceTask,
		RequestID:             t.RequestID,
		SkipBalanceCharge:     true,
	})
}

// refundFailure 失败/超时退款（幂等键防双退）+ 失败留痕。
func (p *Poller) refundFailure(ctx context.Context, t *Task, reason, phase string) {
	ok, err := p.store.MarkSettled(ctx, t.ID)
	if err != nil || !ok {
		if err != nil {
			slog.Error("task_mark_settled_failed", "task_id", t.TaskID, "error", err)
		}
		return
	}
	if t.HoldAmount > 0 {
		p.adjust(t, t.HoldAmount, fmt.Sprintf("任务失败退款 %s %s", t.Platform, t.TaskID),
			fmt.Sprintf("task:refund:%d", t.ID))
	}
	if p.errSink != nil {
		p.errSink.CountFailure(context.Background(), 0, "", phase)
		p.errSink.Record(errlog.Entry{
			RequestID: t.RequestID,
			Source:    errlog.SourceRelay,
			Phase:     phase,
			Message:   reason,
			Model:     t.RequestModel,
			Endpoint:  taskEndpoint(t),
			UserID:    t.UserID,
			UserEmail: t.UserEmail,
			APIKeyID:  t.APIKeyID,
			GroupID:   t.GroupID,
			ChannelID: t.ChannelID,
		})
	}
}

// adjust 结算/退款动账（后台超时上下文）；失败仅记日志（幂等键保证可人工补账）。
func (p *Poller) adjust(t *Task, amount float64, remark, idemKey string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := p.balance.Adjust(ctx, t.UserID, amount, remark, idemKey); err != nil {
		slog.Error("task_settle_adjust_failed",
			"task_id", t.TaskID, "user_id", t.UserID, "amount", amount, "idem_key", idemKey, "error", err)
	}
}

// taskEndpoint 任务的展示端点（usage_log/失败留痕 endpoint 列）。
func taskEndpoint(t *Task) string {
	switch t.Platform {
	case PlatformSuno:
		return "/suno/submit/" + t.Action
	default:
		return "/v1/videos"
	}
}

// sleepCtx 可被 ctx 取消的 sleep。
func sleepCtx(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
