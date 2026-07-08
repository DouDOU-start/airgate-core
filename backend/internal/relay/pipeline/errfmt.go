package pipeline

import (
	"math"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// errorBody OpenAI 形态错误体：{"error":{"message","type","code"}}。
// 本阶段所有错误路径（402/429/400/503…）统一走它。
type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}

// writeError 写出 OpenAI 形态错误体。
func writeError(c *gin.Context, status int, errType, code, message string) {
	c.JSON(status, errorBody{Error: errorDetail{Message: message, Type: errType, Code: code}})
}

// writeRateLimitError 写出 429 错误体并携带 Retry-After 头（秒向上取整，最小 1）。
func writeRateLimitError(c *gin.Context, code, message string, retryAfter time.Duration) {
	seconds := int(math.Ceil(retryAfter.Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	c.Header("Retry-After", strconv.Itoa(seconds))
	writeError(c, 429, "rate_limit_error", code, message)
}
