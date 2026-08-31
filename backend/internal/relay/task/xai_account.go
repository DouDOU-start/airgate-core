package task

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/billing"
	"github.com/DouDOU-start/airgate-core/internal/errlog"
	"github.com/DouDOU-start/airgate-core/internal/relay/accountreg"
	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
	"github.com/DouDOU-start/airgate-core/internal/relay/clientid"
	"github.com/DouDOU-start/airgate-core/internal/relay/cpa"
	"github.com/DouDOU-start/airgate-core/internal/relay/errfmt"
	"github.com/DouDOU-start/airgate-core/internal/relay/outcome"
	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
)

// submitXAIAccount 通过 xAI OAuth 账号池提交原生视频任务。
// 计费沿用任务子系统的预扣模型，提交成功后持久化 account_id，供后台轮询固定回源。
func (f *Flow) submitXAIAccount(c *gin.Context, keyInfo *auth.APIKeyInfo, ad Adaptor, sub *SubmitRequest) {
	start := time.Now()
	ctx := c.Request.Context()

	if !f.applyTaskClientRestriction(c, keyInfo, sub, start) {
		return
	}

	price, priced := f.pricing.Get(sub.Model)
	estTotal, estSeconds := 0.0, 0
	if priced {
		estTotal, estSeconds = estimate(price, sub)
	}
	if !priced || estTotal <= 0 {
		msg := "模型 " + sub.Model + " 未配置任务计价（per_request_price 或 pricing_extra.video 秒价）"
		writeError(c, http.StatusBadRequest, "invalid_request_error", "model_price_not_configured", msg)
		f.recordFailure(c, keyInfo, sub.Model, start, errlog.Entry{
			Phase: errlog.PhasePrecheckPrice, StatusCode: http.StatusBadRequest,
			ErrorType: "invalid_request_error", ErrorCode: "model_price_not_configured", Message: msg,
		})
		return
	}
	sub.Seconds = estSeconds

	billingRate := billing.ResolveBillingRate(keyInfo)
	if billing.ExceedsKeyMaxRate(keyInfo, billingRate) {
		msg := fmt.Sprintf("当前计费倍率 %.2f 超过密钥最高倍率 %.2f", billingRate, keyInfo.MaxRate)
		writeError(c, http.StatusForbidden, "permission_error", "billing_rate_exceeded", msg)
		f.recordFailure(c, keyInfo, sub.Model, start, errlog.Entry{
			Phase: errlog.PhasePrecheckRate, StatusCode: http.StatusForbidden,
			ErrorType: "permission_error", ErrorCode: "billing_rate_exceeded", Message: msg,
		})
		return
	}

	hold := estTotal * billingRate
	if f.balance == nil {
		writeError(c, http.StatusInternalServerError, "server_error", "internal_error", "视频任务计费组件未配置")
		return
	}
	if err := f.balance.Hold(ctx, keyInfo.UserID, hold, "任务预扣 xai_video "+sub.Model); err != nil {
		if errors.Is(err, ErrInsufficientBalance) {
			writeError(c, http.StatusPaymentRequired, "insufficient_quota", "insufficient_balance", "账户余额不足")
			return
		}
		slog.Error("xai_video_hold_failed", "user_id", keyInfo.UserID, "model", sub.Model, "error", err)
		writeError(c, http.StatusInternalServerError, "server_error", "internal_error", "预扣余额失败，请稍后重试")
		return
	}
	refund := func(reason string) { f.refundHold(keyInfo.UserID, hold, reason) }

	releaseClient, limitCode := f.acquireClientSlots(c, keyInfo)
	if limitCode != "" {
		refund("并发上限拒绝")
		return
	}
	defer releaseClient()

	if f.accounts == nil || f.cpa == nil {
		refund("xAI 账号转发未配置")
		writeError(c, http.StatusServiceUnavailable, "server_error", "no_available_account", "xAI OAuth 账号转发未配置")
		return
	}

	settings := f.settings.Get(ctx)
	var hardExclude, softExclude []int
	summary := submitFailureSummary{}
	var hops []errlog.AttemptHop
	attempts := 0

	for attempts < maxFailoverAttempts {
		if ctx.Err() != nil {
			refund("客户端取消")
			markCanceled(c)
			return
		}

		exclude := append(append(make([]int, 0, len(hardExclude)+len(softExclude)), hardExclude...), softExclude...)
		acc, err := f.accounts.Pick(keyInfo.GroupID, sub.Model, exclude)
		if err != nil {
			break
		}
		if cpa.ResolveProvider(acc.Platform) != "xai" {
			// 防止管理员把媒体模型误配给其它平台账号后串用凭证。
			hardExclude = append(hardExclude, acc.ID)
			continue
		}

		slotID := uuid.New().String()
		rpmMinute, err := f.concurrency.AcquireAccountCapacity(ctx, acc.ID, slotID, acc.MaxRPM, acc.MaxConcurrency, 0)
		if err != nil {
			summary.localCapacity = true
			softExclude = append(softExclude, acc.ID)
			continue
		}

		attemptStart := time.Now()
		result := f.forwardXAIAccount(ctx, c, acc, sub.Model, adaptor.EndpointXAIVideosGenerations, sub.Body)
		f.concurrency.ReleaseAccountSlot(context.Background(), acc.ID, slotID)
		attemptLatency := time.Since(attemptStart).Milliseconds()
		attempts++

		if len(result.RefreshedCredentials) > 0 {
			f.accounts.UpdateCredentials(acc.ID, result.RefreshedCredentials)
		}
		secret := accountCredentialHint(acc)
		if partialErr := unreplayableCPAResult(result); partialErr != nil {
			// A non-empty CPA response followed by a transport error is
			// indeterminate: xAI may already have accepted the video job. Do not
			// submit it again through another OAuth account.
			refund("上游 xAI 任务提交响应中断")
			reason := outcome.SanitizeKeyLeak(partialErr.Error(), []string{secret})
			writeError(c, http.StatusBadGateway, "upstream_error", "upstream_response_interrupted", "上游 xAI 任务提交响应中断，已停止重试以避免重复创建任务")
			hop := accountTaskAttemptHop(len(hops)+1, acc, result.StatusCode, "responseInterrupted", reason, 0, attemptLatency, false)
			f.recordFailure(c, keyInfo, sub.Model, start, errlog.Entry{
				Phase: errlog.PhaseStreamAborted, StatusCode: http.StatusBadGateway,
				ErrorType: "upstream_error", ErrorCode: "upstream_response_interrupted",
				Message:  "上游 xAI 任务提交已返回部分数据后中断，已阻止账号 failover",
				Attempts: attempts, Chain: append(hops, hop),
				AccountID: acc.ID, AccountName: acc.Name,
			})
			return
		}
		if result.BuildErr != nil {
			f.rpm.DecrementAccountRPM(context.Background(), acc.ID, rpmMinute)
			refund("构建 xAI 视频请求失败")
			adminMsg := outcome.SanitizeKeyLeak(result.BuildErr.Error(), []string{secret})
			userMsg := outcome.SanitizeUpstreamLeak(result.BuildErr.Error(), []string{secret}, "")
			writeError(c, http.StatusBadRequest, "invalid_request_error", "bad_request", userMsg)
			f.recordFailure(c, keyInfo, sub.Model, start, errlog.Entry{
				Phase: errlog.PhaseBadRequest, StatusCode: http.StatusBadRequest,
				Message: adminMsg, Attempts: attempts, AccountID: acc.ID, AccountName: acc.Name,
			})
			return
		}

		o := outcome.Classify(result.StatusCode, result.Headers, result.Body, result.NetErr)
		o.Reason = outcome.SanitizeKeyLeak(o.Reason, []string{secret})
		if o.Verdict == outcome.Success {
			taskID, st, parseErr := ad.ParseSubmitResponse(result.Body)
			if parseErr != nil || taskID == "" {
				// A successful HTTP status does not prove that the response contains
				// a usable task id. xAI may already have accepted the job, so do not
				// submit it again through another OAuth account on parse failure.
				reason := outcome.SanitizeKeyLeak(taskSubmitResponseParseFailureReason(result.Body, parseErr), []string{secret})
				refund("上游 xAI 任务提交响应无效")
				writeError(c, http.StatusBadGateway, "upstream_error", "invalid_upstream_response", "上游 xAI 任务提交响应无效，已停止重试以避免重复创建任务")
				hop := accountTaskAttemptHop(len(hops)+1, acc, result.StatusCode, "responseInvalid", reason, 0, attemptLatency, false)
				f.recordFailure(c, keyInfo, sub.Model, start, errlog.Entry{
					Phase: errlog.PhaseUpstreamClientError, StatusCode: http.StatusBadGateway,
					ErrorType: "upstream_error", ErrorCode: "invalid_upstream_response",
					Message:  "上游 xAI 任务提交返回 2xx 但缺少可解析的任务 ID，已阻止账号 failover",
					Attempts: attempts, Chain: append(hops, hop),
					AccountID: acc.ID, AccountName: acc.Name,
				})
				return
			} else {
				f.accounts.MarkActive(acc.ID)
				t := f.newXAIAccountTask(c, keyInfo, acc, sub, taskID, st, hold, estTotal, billingRate)
				id, insertErr := f.store.Insert(context.Background(), t)
				if insertErr != nil {
					slog.Error("xai_video_task_insert_failed", "task_id", taskID, "account_id", acc.ID, "error", insertErr)
					refund("任务落库失败")
					writeError(c, http.StatusInternalServerError, "server_error", "internal_error", "任务保存失败，费用已退回")
					return
				}
				t.ID = id
				f.rpm.IncrementUserGroupRPM(context.Background(), keyInfo.UserID, keyInfo.GroupID)
				c.Data(http.StatusOK, "application/json", ad.RenderTask(t))
				return
			}
		}

		switch o.Verdict {
		case outcome.RateLimited:
			f.rpm.DecrementAccountRPM(context.Background(), acc.ID, rpmMinute)
			retryUntil := time.Now().Add(o.RetryAfter)
			if o.RetryAfter <= 0 {
				retryUntil = time.Now().Add(time.Minute)
			}
			f.accounts.MarkRateLimited(acc.ID, retryUntil, o.Reason)
			hardExclude = append(hardExclude, acc.ID)
			summary.rateLimited = true
			summary.observeRetryAfter(o.RetryAfter)
			hops = append(hops, accountTaskAttemptHop(len(hops)+1, acc, result.StatusCode, "rateLimited", o.Reason, o.RetryAfter.Milliseconds(), attemptLatency, false))
		case outcome.AuthFailed:
			f.rpm.DecrementAccountRPM(context.Background(), acc.ID, rpmMinute)
			if settings.AutoBanEnabled {
				f.accounts.MarkDisabled(acc.ID, outcome.TruncateErrorMsg(o.Reason))
			}
			hardExclude = append(hardExclude, acc.ID)
			summary.authFailed = true
			hops = append(hops, accountTaskAttemptHop(len(hops)+1, acc, result.StatusCode, "authFailed", o.Reason, 0, attemptLatency, settings.AutoBanEnabled))
		case outcome.Transient:
			f.rpm.DecrementAccountRPM(context.Background(), acc.ID, rpmMinute)
			softExclude = append(softExclude, acc.ID)
			summary.transient = true
			verdict := "transient"
			if result.NetErr != nil {
				verdict = "networkError"
			}
			hops = append(hops, accountTaskAttemptHop(len(hops)+1, acc, result.StatusCode, verdict, o.Reason, 0, attemptLatency, false))
		default:
			refund("上游拒绝请求")
			up := errfmt.ParseUpstream(result.StatusCode, result.Body)
			up.Message = outcome.SanitizeUpstreamLeak(up.Message, []string{secret}, "")
			writeUpstreamError(c, result.StatusCode, up)
			hop := accountTaskAttemptHop(len(hops)+1, acc, result.StatusCode, "clientError", o.Reason, 0, attemptLatency, false)
			f.recordFailure(c, keyInfo, sub.Model, start, errlog.Entry{
				Phase: errlog.PhaseUpstreamClientError, StatusCode: result.StatusCode,
				Message: o.Reason, Attempts: attempts, Chain: append(hops, hop),
				AccountID: acc.ID, AccountName: acc.Name,
			})
			return
		}
	}

	refund("全部账号失败")
	status, errType, errCode, msg := writeSubmitAllFailed(c, summary)
	f.recordFailure(c, keyInfo, sub.Model, start, errlog.Entry{
		Phase: errlog.PhaseUpstreamExhausted, StatusCode: status,
		ErrorType: errType, ErrorCode: errCode, Message: msg,
		Attempts: attempts, Chain: hops,
	})
}

func unreplayableCPAResult(result cpa.ForwardResult) error {
	if result.Written || (!result.DataReceived &&
		(result.StatusCode < http.StatusOK || result.StatusCode >= http.StatusMultipleChoices)) {
		return nil
	}
	switch {
	case result.NetErr != nil:
		return result.NetErr
	case result.StreamErr != nil:
		return result.StreamErr
	case result.BuildErr != nil:
		return result.BuildErr
	default:
		return nil
	}
}

// hasXAIVideoAccount 判断当前分组和模型是否存在真正可调度的 xAI 账号。
// 本机没有账号或 CPA 未装配时返回 false，由调用方改走普通渠道级联。
func (f *Flow) hasXAIVideoAccount(groupID int, model string) bool {
	if f.accounts == nil || f.cpa == nil {
		return false
	}
	for _, acc := range f.accounts.ListCandidates(groupID, model, nil) {
		if cpa.ResolveProvider(acc.Platform) == "xai" {
			return true
		}
	}
	return false
}

func (f *Flow) applyTaskClientRestriction(c *gin.Context, keyInfo *auth.APIKeyInfo, sub *SubmitRequest, start time.Time) bool {
	clientid.Detect(c)
	if len(keyInfo.GroupAllowedClients) == 0 {
		return true
	}
	if clientid.Matches(clientid.Get(c), keyInfo.GroupAllowedClients) {
		return true
	}
	if keyInfo.GroupFallbackID != nil {
		keyInfo.GroupID = *keyInfo.GroupFallbackID
		return true
	}
	writeError(c, http.StatusForbidden, "permission_error", "client_restricted", "当前客户端类型不允许访问此分组")
	f.recordFailure(c, keyInfo, sub.Model, start, errlog.Entry{
		Phase: errlog.PhasePrecheckClientRestrict, StatusCode: http.StatusForbidden,
		ErrorType: "permission_error", ErrorCode: "client_restricted", Message: "当前客户端类型不允许访问此分组",
	})
	return false
}

func (f *Flow) forwardXAIAccount(ctx context.Context, c *gin.Context, acc *accountreg.Snapshot, model, endpoint string, payload []byte) cpa.ForwardResult {
	requestCtx, cancel := context.WithTimeout(ctx, submitTimeout)
	defer cancel()
	return f.cpa.Forward(requestCtx, c, cpa.ForwardRequest{
		Account: cpa.AccountAuthInput{
			AccountID: acc.ID, Name: acc.Name, Platform: acc.Platform, Type: acc.Type,
			Credentials: acc.Credentials, ProxyURL: acc.ProxyURL,
		},
		Model: model, UpstreamModel: acc.ResolveModel(model), Endpoint: endpoint, EntryProtocol: registry.ProtocolOpenAI,
		Payload: payload, Headers: http.Header{"Content-Type": []string{"application/json"}},
	})
}

func (f *Flow) newXAIAccountTask(c *gin.Context, keyInfo *auth.APIKeyInfo, acc *accountreg.Snapshot, sub *SubmitRequest, taskID string, st *Status, hold, estTotal, billingRate float64) *Task {
	now := time.Now()
	t := &Task{
		TaskID: taskID, Platform: PlatformXAIVideo, Action: sub.Action,
		Status: StatusSubmitted, RequestModel: sub.Model, UpstreamModel: acc.ResolveModel(sub.Model),
		HoldAmount: hold, EstTotal: estTotal, RateMultiplier: billingRate,
		SellRate: keyInfo.SellRate, AccountRateMultiplier: acc.EffectiveCostRatio(),
		Seconds: sub.Seconds, Resolution: sub.Resolution,
		SubmitTime: now, RequestID: requestIDOf(c),
		UserID: keyInfo.UserID, UserEmail: keyInfo.UserEmail, APIKeyID: keyInfo.KeyID,
		GroupID: keyInfo.GroupID, AccountID: acc.ID,
		CreatedAt: now, UpdatedAt: now,
	}
	if st != nil {
		if st.Status != "" {
			t.Status = st.Status
		}
		t.Progress = st.Progress
		if st.Seconds > 0 {
			t.Seconds = st.Seconds
		}
		t.Data = truncateData(st.Raw)
	}
	return t
}

func accountCredentialHint(acc *accountreg.Snapshot) string {
	if acc == nil || acc.Credentials == nil {
		return ""
	}
	for _, key := range []string{"access_token", "api_key"} {
		if value := strings.TrimSpace(acc.Credentials[key]); value != "" {
			return value
		}
	}
	return ""
}

func accountTaskAttemptHop(seq int, acc *accountreg.Snapshot, status int, verdict, reason string, retryAfterMs, latencyMs int64, disabled bool) errlog.AttemptHop {
	return errlog.AttemptHop{
		Seq: seq, AccountID: acc.ID, AccountName: acc.Name,
		UpstreamStat: status, Verdict: verdict, Reason: reason,
		RetryAfterMs: retryAfterMs, LatencyMs: latencyMs, AutoDisabled: disabled,
	}
}
