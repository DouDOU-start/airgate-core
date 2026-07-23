package server

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/billing"
	"github.com/DouDOU-start/airgate-core/internal/server/middleware"
)

// handleBilling 响应 GET /v1/airgate/billing：下游网关用 sk- key 认证后查询本 key 的计费倍率。
//
// 响应格式与上游倍率探测（probe.BillingProbeResult）约定一致，
// 使得两个 AirGate 实例级联时下游可自动感知上游的计费策略。
func (s *Server) handleBilling(c *gin.Context) {
	raw, exists := c.Get(middleware.CtxKeyKeyInfo)
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing api key"})
		return
	}
	keyInfo, ok := raw.(*auth.APIKeyInfo)
	if !ok || keyInfo == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid api key context"})
		return
	}

	rate := billing.ResolveBillingRate(keyInfo)

	c.JSON(http.StatusOK, gin.H{
		"object":          "airgate.key_billing",
		"rate_multiplier": rate,
		"group_rate":      keyInfo.GroupRateMultiplier,
		"sell_rate":       keyInfo.SellRate,
		"max_rate":        keyInfo.MaxRate,
		"observed_at":     time.Now().Format(time.RFC3339),
	})
}
