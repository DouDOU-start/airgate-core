package pipeline

import (
	"time"

	"github.com/DouDOU-start/airgate-core/internal/relay/outcome"
	"github.com/DouDOU-start/airgate-core/internal/requestaudit"
)

// finishChannelAuditAttempt 把普通渠道调用的最终结果回写到发包前创建的审计行。
func finishChannelAuditAttempt(result attemptResult, latencyMs int64, apiKey string) {
	if result.auditAttempt == nil {
		return
	}
	o := outcome.Classify(result.statusCode, result.headers, result.body, result.netErr)
	reason := outcome.SanitizeKeyLeak(o.Reason, []string{apiKey})
	streamCompleted := result.netErr == nil
	if result.written {
		streamCompleted = result.streamErr == nil && result.done
	}
	result.auditAttempt.Finish(requestaudit.AttemptFinish{
		StatusCode:      result.statusCode,
		Verdict:         auditVerdictName(o.Verdict, result.netErr),
		Reason:          reason,
		RetryAfter:      o.RetryAfter,
		Latency:         time.Duration(latencyMs) * time.Millisecond,
		FirstTokenMs:    result.firstTokenMs,
		ResponseStarted: result.statusCode > 0,
		StreamCompleted: streamCompleted,
	})
}

func auditVerdictName(verdict outcome.Verdict, netErr error) string {
	if netErr != nil {
		return "networkError"
	}
	switch verdict {
	case outcome.Success:
		return "success"
	case outcome.RateLimited:
		return "rateLimited"
	case outcome.AuthFailed:
		return "authFailed"
	case outcome.Transient:
		return "transient"
	default:
		return "clientError"
	}
}
