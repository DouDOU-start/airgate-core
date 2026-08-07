package pipeline

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
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/proxyutil"

	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/billing"
	"github.com/DouDOU-start/airgate-core/internal/errlog"
	"github.com/DouDOU-start/airgate-core/internal/relay/accountreg"
	"github.com/DouDOU-start/airgate-core/internal/relay/cpa"
	"github.com/DouDOU-start/airgate-core/internal/relay/dto"
	"github.com/DouDOU-start/airgate-core/internal/relay/errfmt"
	"github.com/DouDOU-start/airgate-core/internal/relay/outcome"
	"github.com/DouDOU-start/airgate-core/internal/relay/pricing"
	"github.com/DouDOU-start/airgate-core/internal/requestaudit"
)

// AccountForwarder 是 Pipeline 使用的 CPA 转发窄接口。
// *cpa.Bridge 天然满足；测试可注入确定性替身覆盖账号 failover 状态机。
type AccountForwarder interface {
	Forward(ctx context.Context, c *gin.Context, req cpa.ForwardRequest) cpa.ForwardResult
}

// executeAccountAttempt 执行一次账号路径转发（CPA）。
// 槽位/RPM 由调用方在抢到后传入；本函数 defer 释放账号并发槽。
func (p *Pipeline) executeAccountAttempt(
	c *gin.Context,
	acc *accountreg.Snapshot,
	req *dto.ChatRequest,
	endpoint string,
	protocol string,
	payload []byte,
	start time.Time,
	requestID string,
	rpmMinute int64,
	auditRequest *requestaudit.Handle,
) attemptResult {
	defer func() {
		p.concurrency.ReleaseAccountSlot(context.Background(), acc.ID, requestID)
		if rec := recover(); rec != nil {
			p.rpm.DecrementAccountRPM(context.Background(), acc.ID, rpmMinute)
			panic(rec)
		}
	}()

	if p.cpa == nil {
		return attemptResult{buildErr: errCPAUnavailable}
	}

	fwdReq := cpa.ForwardRequest{
		Account: cpa.AccountAuthInput{
			AccountID:   acc.ID,
			Name:        acc.Name,
			Platform:    acc.Platform,
			Type:        acc.Type,
			Credentials: acc.Credentials,
			ProxyURL:    acc.ProxyURL,
		},
		Model:         req.Model,
		Endpoint:      endpoint,
		EntryProtocol: protocol,
		Stream:        req.Stream,
		Payload:       payload,
		Headers:       http.Header{"Content-Type": []string{"application/json"}},
	}

	ctx := c.Request.Context()
	if auditRequest != nil {
		baseTransport := http.RoundTripper(http.DefaultTransport)
		if strings.TrimSpace(acc.ProxyURL) != "" {
			transport, _, errBuild := proxyutil.BuildHTTPTransport(acc.ProxyURL)
			if errBuild != nil {
				return attemptResult{auditErr: fmt.Errorf("构造账号代理审计传输层失败: %w", errBuild)}
			}
			if transport != nil {
				baseTransport = transport
			}
		}
		target := requestaudit.Target{
			RouteKind: "account", AccountID: acc.ID, AccountName: acc.Name,
			AccountEmail: accountEmail(acc), AccountPlatform: acc.Platform, AccountType: acc.Type,
		}
		// CPA 通过固定字符串上下文键接收最终网络层；同时清空 Auth.ProxyURL，
		// 避免 executor 的代理优先级绕过审计 RoundTripper。
		//nolint:staticcheck
		ctx = context.WithValue(ctx, "cliproxy.roundtripper", requestaudit.NewRoundTripper(baseTransport, auditRequest, target))
		fwdReq.Account.ProxyURL = ""
	}
	cancel := context.CancelFunc(func() {})
	if !req.Stream {
		ctx, cancel = context.WithTimeout(ctx, nonStreamTimeout)
	}
	defer cancel()

	result := p.cpa.Forward(ctx, c, fwdReq)
	if isAuditWriteError(result.NetErr) || isAuditWriteError(result.BuildErr) || isAuditWriteError(result.StreamErr) {
		return attemptResult{auditErr: requestaudit.ErrWrite}
	}
	// 在 handleAccountOutcome 的成功分支调用 MarkActive 之前，捕获“账号已被并发请求
	// 标为限流，但当前在途请求仍成功”的 Codex OAuth 诊断现场。
	p.logCodexRateLimitedAccountSuccess(c, acc, req.Model, endpoint, req.Stream, payload, fwdReq.Headers, result)

	// refresh 后的凭证写回内存（+ 可选落库）。
	if len(result.RefreshedCredentials) > 0 && p.accounts != nil {
		p.accounts.UpdateCredentials(acc.ID, result.RefreshedCredentials)
	}

	return attemptResult{
		netErr:       result.NetErr,
		buildErr:     result.BuildErr,
		statusCode:   result.StatusCode,
		headers:      result.Headers,
		body:         result.Body,
		contentType:  result.ContentType,
		usage:        result.Usage,
		firstTokenMs: result.FirstTokenMs,
		written:      result.Written,
		streamErr:    result.StreamErr,
		done:         result.Done,
	}
}

func accountEmail(acc *accountreg.Snapshot) string {
	if acc == nil {
		return ""
	}
	if email := strings.TrimSpace(acc.Email); email != "" {
		return email
	}
	return strings.TrimSpace(acc.Credentials["email"])
}

func isAuditWriteError(err error) bool {
	return err != nil && (errors.Is(err, requestaudit.ErrWrite) || strings.Contains(err.Error(), requestaudit.ErrWrite.Error()))
}

// errCPAUnavailable CPA 桥未注入时的构建错误（不 failover 到同账号）。
var errCPAUnavailable = errString("CPA 桥接层未配置")

type errString string

func (e errString) Error() string { return string(e) }

// accountAttemptHop 构造账号路径的重试链一跳。
func accountAttemptHop(seq int, acc *accountreg.Snapshot, upstreamStatus int, verdict, reason string, retryAfterMs, latencyMs int64, autoDisabled bool) errlog.AttemptHop {
	return errlog.AttemptHop{
		Seq:          seq,
		AccountID:    acc.ID,
		AccountName:  acc.Name,
		UpstreamStat: upstreamStatus,
		Verdict:      verdict,
		Reason:       reason,
		RetryAfterMs: retryAfterMs,
		LatencyMs:    latencyMs,
		AutoDisabled: autoDisabled,
	}
}

// recordAccountUsage 账号路径计费收尾（ChannelID/ChannelKeyID=0，填 AccountID）。
func (p *Pipeline) recordAccountUsage(
	c *gin.Context,
	keyInfo *auth.APIKeyInfo,
	acc *accountreg.Snapshot,
	req *dto.ChatRequest,
	result attemptResult,
	start time.Time,
	price pricing.Price,
) {
	if p.sink == nil {
		return
	}
	p.rpm.IncrementUserGroupRPM(context.Background(), keyInfo.UserID, keyInfo.GroupID)

	var usage dto.Usage
	if result.usage != nil {
		usage = *result.usage
	}
	usage = enrichImageBillingUsage(c.Request.URL.Path, req, result.body, usage)
	tier := serviceTierOf(req)
	reasoningEffort := reasoningEffortOf(c, req)
	costs := pricing.ComputeCosts(price, pricing.Usage{
		PromptTokens:          usage.PromptTokens,
		CompletionTokens:      usage.CompletionTokens,
		CachedTokens:          usage.CachedTokens,
		CacheCreationTokens:   usage.CacheCreationTokens,
		CacheCreation5mTokens: usage.CacheCreation5mTokens,
		CacheCreation1hTokens: usage.CacheCreation1hTokens,
		Calls:                 usage.Calls,
		ImageSize:             usage.ImageSize,
		ImageQuality:          usage.ImageQuality,
	}, tier)
	calc := p.calculator.Calculate(billing.CalculateInput{
		InputCost:         costs.Input,
		OutputCost:        costs.Output,
		CachedInputCost:   costs.Cached,
		CacheCreationCost: costs.CacheCreation5m + costs.CacheCreation1h,
		BillingRate:       billing.ResolveBillingRate(keyInfo),
		SellRate:          keyInfo.SellRate,
		AccountRate:       acc.EffectiveCostRatio(),
	})

	inputTokens := usage.PromptTokens - usage.CachedTokens
	if inputTokens < 0 {
		inputTokens = 0
	}
	inputPrice := price.Input
	billedCalls := usage.Calls
	if perImage, ok := pricing.ImagePriceFor(price, usage.ImageQuality, usage.ImageSize); ok {
		inputPrice = perImage
		if billedCalls < 1 {
			billedCalls = 1
		}
	} else if price.PerRequest > 0 {
		inputPrice = price.PerRequest
		if billedCalls < 1 {
			billedCalls = 1
		}
	}

	p.sink.Record(billing.UsageRecord{
		UserID:                keyInfo.UserID,
		UserEmail:             keyInfo.UserEmail,
		APIKeyID:              keyInfo.KeyID,
		AccountID:             acc.ID,
		GroupID:               keyInfo.GroupID,
		Model:                 req.Model,
		InputTokens:           inputTokens,
		OutputTokens:          usage.CompletionTokens,
		CachedInputTokens:     usage.CachedTokens,
		CacheCreationTokens:   usage.CacheCreationTokens,
		CacheCreation5mTokens: usage.CacheCreation5mTokens,
		CacheCreation1hTokens: usage.CacheCreation1hTokens,
		Calls:                 billedCalls,
		InputPrice:            inputPrice,
		OutputPrice:           price.Output,
		CachedInputPrice:      price.CachedInput,
		CacheCreationPrice:    price.CacheCreation5m,
		CacheCreation1hPrice:  price.CacheCreation1h,
		ServiceTier:           tier,
		ReasoningEffort:       reasoningEffort,
		ImageSize:             usage.ImageSize,
		ImageQuality:          usage.ImageQuality,
		InputCost:             calc.InputCost,
		OutputCost:            calc.OutputCost,
		CachedInputCost:       calc.CachedInputCost,
		CacheCreationCost:     calc.CacheCreationCost,
		TotalCost:             calc.TotalCost,
		ActualCost:            calc.ActualCost,
		BilledCost:            calc.BilledCost,
		RateMultiplier:        calc.RateMultiplier,
		SellRate:              calc.SellRate,
		AccountRateMultiplier: calc.AccountRateMultiplier,
		Stream:                req.Stream,
		DurationMs:            time.Since(start).Milliseconds(),
		FirstTokenMs:          result.firstTokenMs,
		UserAgent:             truncateRunes(c.Request.UserAgent(), maxUserAgentLen),
		IPAddress:             c.ClientIP(),
		Endpoint:              c.Request.URL.Path,
		Source:                billing.SourceRelay,
		RequestID:             requestIDOf(c),
	})
}

// handleAccountOutcome 处理账号路径一次 attempt 的 outcome（与渠道路径对称）。
// 返回 true 表示请求已终止（成功/不可重试错误/已写出流）。
func (p *Pipeline) handleAccountOutcome(
	c *gin.Context,
	keyInfo *auth.APIKeyInfo,
	acc *accountreg.Snapshot,
	req *dto.ChatRequest,
	result attemptResult,
	start time.Time,
	price pricing.Price,
	settings GatewaySettings,
	opts forwardOptions,
	rpmMinute int64,
	attempts int,
	hops *[]errlog.AttemptHop,
	summary *failureSummary,
	hardExclude, softExclude *[]int,
	attemptLatency int64,
	probeLease accountreg.RateLimitProbeLease,
) (done bool) {
	rateLimitProbe := probeLease != 0
	apiKeyHint := ""
	if acc.Credentials != nil {
		apiKeyHint = acc.Credentials["access_token"]
		if apiKeyHint == "" {
			apiKeyHint = acc.Credentials["api_key"]
		}
	}

	// buildErr：一次性 400
	if result.buildErr != nil {
		p.rpm.DecrementAccountRPM(context.Background(), acc.ID, rpmMinute)
		adminMsg := outcome.SanitizeKeyLeak(result.buildErr.Error(), []string{apiKeyHint})
		if rateLimitProbe && p.accounts != nil {
			p.accounts.MarkRateLimitProbeFailed(acc.ID, probeLease, 0, adminMsg)
		}
		userMsg := outcome.SanitizeUpstreamLeak(result.buildErr.Error(), []string{apiKeyHint}, "")
		writeError(c, http.StatusBadRequest, "invalid_request_error", "bad_request", userMsg)
		p.recordFailure(c, keyInfo, req, start, errlog.Entry{
			Phase: errlog.PhaseBadRequest, StatusCode: http.StatusBadRequest,
			ErrorType: "invalid_request_error", ErrorCode: "bad_request",
			Message: adminMsg, Attempts: attempts,
			AccountID: acc.ID, AccountName: acc.Name,
		})
		return true
	}

	ctx := c.Request.Context()
	if ctx.Err() != nil && !result.written {
		if rateLimitProbe && p.accounts != nil {
			// Forward 已经开始执行，取消时无法确定请求是否触达上游；按失败探测
			// 冷却，避免客户端连续取消后立即重新取得同一账号的探测资格。
			p.accounts.MarkRateLimitProbeFailed(acc.ID, probeLease, 0, "限流恢复探测在响应完成前被取消，结果未知")
		}
		p.rpm.DecrementAccountRPM(context.Background(), acc.ID, rpmMinute)
		markCanceled(c)
		p.recordFailure(c, keyInfo, req, start, errlog.Entry{
			Phase: errlog.PhaseCanceled, StatusCode: statusClientClosedRequest,
			Message: "客户端取消请求", Attempts: attempts, Chain: *hops,
		})
		return true
	}

	// 流式已写出
	if result.written {
		streamComplete := result.done
		if result.streamErr != nil || !streamComplete {
			slog.Warn("relay_account_stream_aborted",
				"account_id", acc.ID, "model", req.Model,
				"complete", streamComplete, "error", result.streamErr)
			if rateLimitProbe && p.accounts != nil {
				reason := "上游未发送完成标志即断流"
				if result.streamErr != nil {
					reason = outcome.SanitizeKeyLeak(result.streamErr.Error(), []string{apiKeyHint})
				}
				p.accounts.MarkRateLimitProbeFailed(acc.ID, probeLease, 0, reason)
			}
		} else {
			if p.accounts != nil {
				if rateLimitProbe {
					p.accounts.MarkRateLimitProbeSucceeded(acc.ID, probeLease)
				} else {
					p.accounts.MarkActiveIfNotRateLimited(acc.ID)
				}
			}
			if result.usage == nil {
				slog.Warn("relay_account_stream_usage_missing",
					"account_id", acc.ID, "model", req.Model)
			}
		}
		if !opts.zeroBilling {
			p.recordAccountUsage(c, keyInfo, acc, req, result, start, price)
		}
		if result.streamErr != nil || !streamComplete {
			reason := "上游未发送完成标志即断流"
			if result.streamErr != nil {
				reason = outcome.SanitizeKeyLeak(result.streamErr.Error(), []string{apiKeyHint})
			}
			hop := accountAttemptHop(len(*hops)+1, acc, result.statusCode, "streamAborted", reason, 0, attemptLatency, false)
			p.recordFailure(c, keyInfo, req, start, errlog.Entry{
				Phase: errlog.PhaseStreamAborted, StatusCode: result.statusCode,
				Message: "上游流中断，响应未完成", Billed: true,
				Attempts: attempts, Chain: append(*hops, hop),
				AccountID: acc.ID, AccountName: acc.Name,
			})
		}
		return true
	}

	o := outcome.Classify(result.statusCode, result.headers, result.body, result.netErr)
	o.Reason = outcome.SanitizeKeyLeak(o.Reason, []string{apiKeyHint})

	switch o.Verdict {
	case outcome.Success:
		if p.accounts != nil {
			if rateLimitProbe {
				p.accounts.MarkRateLimitProbeSucceeded(acc.ID, probeLease)
			} else {
				p.accounts.MarkActiveIfNotRateLimited(acc.ID)
			}
		}
		if !opts.zeroBilling {
			p.recordAccountUsage(c, keyInfo, acc, req, result, start, price)
		}
		writeUpstreamBody(c, result)
		return true

	case outcome.RateLimited:
		p.rpm.DecrementAccountRPM(context.Background(), acc.ID, rpmMinute)
		retryAfter := o.RetryAfter
		if p.accounts != nil {
			if rateLimitProbe {
				blockedUntil := p.accounts.MarkRateLimitProbeFailed(acc.ID, probeLease, retryAfter, o.Reason)
				if remaining := time.Until(blockedUntil); remaining > retryAfter {
					retryAfter = remaining
				}
			} else {
				retryUntil := time.Now().Add(retryAfter)
				if retryAfter <= 0 {
					retryUntil = time.Now().Add(5 * time.Second)
				}
				p.accounts.MarkRateLimited(acc.ID, retryUntil, o.Reason)
			}
		}
		*hardExclude = append(*hardExclude, acc.ID)
		summary.rateLimited = true
		summary.observeRetryAfter(retryAfter)
		*hops = append(*hops, accountAttemptHop(len(*hops)+1, acc, result.statusCode, "rateLimited", o.Reason, retryAfter.Milliseconds(), attemptLatency, false))
		slog.Warn("relay_account_rate_limited",
			"account_id", acc.ID, "model", req.Model, "retry_after", retryAfter.String(), "recovery_probe", rateLimitProbe)
		return false

	case outcome.AuthFailed:
		p.rpm.DecrementAccountRPM(context.Background(), acc.ID, rpmMinute)
		autoBan := settings.AutoBanEnabled
		if autoBan && p.accounts != nil {
			p.accounts.MarkDisabled(acc.ID, outcome.TruncateErrorMsg(o.Reason))
		} else if rateLimitProbe && p.accounts != nil {
			p.accounts.MarkRateLimitProbeFailed(acc.ID, probeLease, 0, o.Reason)
		}
		*hardExclude = append(*hardExclude, acc.ID)
		summary.authFailed = true
		*hops = append(*hops, accountAttemptHop(len(*hops)+1, acc, result.statusCode, "authFailed", o.Reason, 0, attemptLatency, autoBan))
		slog.Warn("relay_account_auth_failed",
			"account_id", acc.ID, "model", req.Model, "auto_ban", autoBan, "reason", o.Reason)
		return false

	case outcome.Transient:
		p.rpm.DecrementAccountRPM(context.Background(), acc.ID, rpmMinute)
		if rateLimitProbe {
			if p.accounts != nil {
				p.accounts.MarkRateLimitProbeFailed(acc.ID, probeLease, 0, o.Reason)
			}
			*hardExclude = append(*hardExclude, acc.ID)
		} else {
			// 账号池场景已收敛到渠道；普通 transient 仅本轮 soft 排除，不做 pool 软降级。
			*softExclude = append(*softExclude, acc.ID)
		}
		summary.transient = true
		verdictName := "transient"
		if result.netErr != nil {
			verdictName = "networkError"
		}
		*hops = append(*hops, accountAttemptHop(len(*hops)+1, acc, result.statusCode, verdictName, o.Reason, 0, attemptLatency, false))
		slog.Warn("relay_account_transient_failure",
			"account_id", acc.ID, "model", req.Model, "reason", o.Reason)
		return false

	default: // ClientError
		if rateLimitProbe && p.accounts != nil {
			p.accounts.MarkRateLimitProbeFailed(acc.ID, probeLease, 0, o.Reason)
		}
		billed := result.usage != nil && !opts.zeroBilling
		if billed {
			p.recordAccountUsage(c, keyInfo, acc, req, result, start, price)
		}
		up := errfmt.ParseUpstream(result.statusCode, result.body)
		up.Message = outcome.SanitizeUpstreamLeak(up.Message, []string{apiKeyHint}, "")
		writeUpstreamError(c, result.statusCode, up)
		hop := accountAttemptHop(len(*hops)+1, acc, result.statusCode, "clientError", o.Reason, 0, attemptLatency, false)
		p.recordFailure(c, keyInfo, req, start, errlog.Entry{
			Phase: errlog.PhaseUpstreamClientError, StatusCode: result.statusCode,
			Message: o.Reason, Billed: billed,
			Attempts: attempts, Chain: append(*hops, hop),
			AccountID: acc.ID, AccountName: acc.Name,
		})
		return true
	}
}

// prepareAccountPayload 序列化请求体供 CPA 转发（原样 JSON）。
func prepareAccountPayload(req *dto.ChatRequest, opts forwardOptions) ([]byte, error) {
	if opts.rawBody != nil {
		return opts.rawBody, nil
	}
	return req.Marshal()
}

// acquireAccountSlots 抢账号 RPM + 并发槽；失败返回 soft=true 表示容量满。
func (p *Pipeline) acquireAccountSlots(ctx context.Context, acc *accountreg.Snapshot, stream bool) (requestID string, rpmMinute int64, soft bool, ok bool) {
	rpmOK, minute, _ := p.rpm.TryIncrementAccountRPM(ctx, acc.ID, acc.MaxRPM)
	if !rpmOK {
		return "", 0, true, false
	}
	requestID = uuid.New().String()
	if err := p.concurrency.AcquireAccountSlot(ctx, acc.ID, requestID, acc.MaxConcurrency, channelSlotTTL(stream)); err != nil {
		p.rpm.DecrementAccountRPM(ctx, acc.ID, minute)
		return "", 0, true, false
	}
	return requestID, minute, false, true
}
