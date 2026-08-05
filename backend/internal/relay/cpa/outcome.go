package cpa

import (
	"errors"
	"net/http"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"

	"github.com/DouDOU-start/airgate-core/internal/relay/outcome"
)

// ErrorInfo 从 CPA 错误中提取的 HTTP 语义。
type ErrorInfo struct {
	StatusCode int
	Body       []byte
	Headers    http.Header
	RetryAfter time.Duration
	// NetLike 是否更像网络/传输错误（无明确 HTTP 状态）。
	NetLike bool
	// Message 错误文案。
	Message string
}

// ClassifyError 把 CPA executor 错误映射为 airgate 可用的 ErrorInfo。
// 能提取 StatusCode 的走 HTTP 语义；否则当网络类错误。
func ClassifyError(err error) ErrorInfo {
	if err == nil {
		return ErrorInfo{}
	}
	info := ErrorInfo{Message: err.Error()}

	// StatusError：CPA 标准状态错误（多数 executor 的 statusErr）。
	var se cliproxyexecutor.StatusError
	if errors.As(err, &se) && se != nil {
		if code := se.StatusCode(); code > 0 {
			info.StatusCode = code
			info.Body = []byte(err.Error())
		}
	}
	// 兼容匿名 StatusCode() 接口。
	if info.StatusCode == 0 {
		type statusCoder interface{ StatusCode() int }
		if sc, ok := err.(statusCoder); ok {
			if code := sc.StatusCode(); code > 0 {
				info.StatusCode = code
				info.Body = []byte(err.Error())
			}
		}
	}
	// Retry-After 接口（CPA statusErr.RetryAfter）。
	type retryAfterProvider interface{ RetryAfter() *time.Duration }
	if ra, ok := err.(retryAfterProvider); ok {
		if d := ra.RetryAfter(); d != nil && *d > 0 {
			info.RetryAfter = *d
			if info.Headers == nil {
				info.Headers = make(http.Header)
			}
			info.Headers.Set("Retry-After", formatRetryAfterSeconds(*d))
		}
	}

	if info.StatusCode == 0 {
		info.NetLike = true
	}
	return info
}

// ToOutcome 将 ErrorInfo 转为 airgate outcome 判决（与渠道路径共用判定表）。
func ToOutcome(info ErrorInfo) outcome.Outcome {
	if info.NetLike || info.StatusCode == 0 {
		msg := info.Message
		if msg == "" {
			msg = "CPA 执行失败"
		}
		return outcome.Outcome{Verdict: outcome.Transient, Reason: "网络/执行错误: " + msg}
	}
	return outcome.Classify(info.StatusCode, info.Headers, info.Body, nil)
}

func formatRetryAfterSeconds(d time.Duration) string {
	sec := int(d.Seconds())
	if sec < 1 {
		sec = 1
	}
	return itoa(sec)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [12]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
