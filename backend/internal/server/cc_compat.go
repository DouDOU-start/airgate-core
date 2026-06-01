package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/ent/apikey"
	"github.com/DouDOU-start/airgate-core/internal/auth"
)

// cc-switch 通用模板兼容端点
//
// 背景：cc-switch（https://github.com/farion1231/cc-switch）的通用脚本可以配置为
//
//	GET {baseUrl}/v1/usage
//	Authorization: Bearer {{apiKey}}
//
// AirGate 安装脚本也使用 GET {baseUrl}/v1/usage 做轻量 Key 校验。
// 响应里的 balance / remaining / quota.remaining 都是同一个真实可用余额，
// 方便不同脚本模板读取。
//
// airgate 原生的用量接口 /api/v1/usage 需要 JWT，不接受 sk-xxx API Key，因此
// cc-switch 无法直接查询。这里提供轻量兼容端点 /v1/usage（在 router.go 中注册，
// 必须在 NoRoute 之前，否则会被插件动态路由吃掉），让 cc-switch 能看到余额。
//
// 故意不复用 middleware.APIKeyAuth：
//   - APIKeyAuth 在额度耗尽时返回 402，而额度耗尽恰恰是用户最需要在 cc-switch
//     UI 上看到的状态；
//   - APIKeyAuth 要求绑定 group，但查询余额本身不需要走计费链路。

// handleCCCompatUserBalance 响应 cc-switch 通用模板的 GET /v1/usage。
func (s *Server) handleCCCompatUserBalance(c *gin.Context) {
	key := extractCCBearerKey(c)
	if key == "" || !strings.HasPrefix(key, "sk-") {
		c.JSON(http.StatusUnauthorized, gin.H{
			"is_active": false,
			"balance":   0,
			"message":   "missing or invalid api key",
		})
		return
	}

	ak, err := s.db.APIKey.Query().
		Where(
			apikey.KeyHash(auth.HashAPIKey(key)),
			apikey.StatusEQ(apikey.StatusActive),
		).
		WithUser().
		Only(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"is_active": false,
			"balance":   0,
			"message":   "invalid api key",
		})
		return
	}

	if ak.ExpiresAt != nil && ak.ExpiresAt.Before(time.Now()) {
		c.JSON(http.StatusOK, gin.H{
			"is_active": false,
			"balance":   0,
			"message":   "api key expired",
		})
		return
	}

	u, err := ak.Edges.UserOrErr()
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"is_active": false,
			"balance":   0,
			"message":   "invalid api key owner",
		})
		return
	}

	balance, quotaRemaining := ccCompatAvailableBalance(u.Balance, ak.QuotaUsd, ak.UsedQuota)
	c.JSON(http.StatusOK, gin.H{
		"is_active": balance > 0,
		"balance":   balance,
		"remaining": balance,
		"unit":      "USD",
		"quota": gin.H{
			"remaining":         balance,
			"api_key_remaining": quotaRemaining,
			"total":             ak.QuotaUsd,
			"used":              ak.UsedQuota,
			"unlimited":         ak.QuotaUsd <= 0,
		},
	})
}

// ccCompatAvailableBalance 返回 cc-switch 展示的真实可用余额：
// - Key 有 quota_usd 时，Key 剩余额度和用户余额任一耗尽都会让请求不可用；
// - Key 无上限时，直接展示用户余额，避免旧实现的 1000000 假余额。
func ccCompatAvailableBalance(userBalance, quotaUSD, usedQuota float64) (balance float64, quotaRemaining float64) {
	if userBalance < 0 {
		userBalance = 0
	}
	if quotaUSD <= 0 {
		return userBalance, userBalance
	}
	quotaRemaining = quotaUSD - usedQuota
	if quotaRemaining < 0 {
		quotaRemaining = 0
	}
	if userBalance < quotaRemaining {
		return userBalance, quotaRemaining
	}
	return quotaRemaining, quotaRemaining
}

// extractCCBearerKey 从 Authorization 头提取 Bearer token。
// 独立于 middleware.extractBearerToken（后者不导出）。
func extractCCBearerKey(c *gin.Context) string {
	h := c.GetHeader("Authorization")
	if h == "" {
		return ""
	}
	parts := strings.SplitN(h, " ", 2)
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		return strings.TrimSpace(parts[1])
	}
	return ""
}
