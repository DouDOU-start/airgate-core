package middleware

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// CORSConfig CORS 中间件配置。
type CORSConfig struct {
	// AllowOrigins 允许的来源列表。为空时仅允许同源请求（不设置 Access-Control-Allow-Origin）。
	// 设为 ["*"] 允许所有来源（仅开发环境推荐）。
	AllowOrigins []string

	// AllowMethods 允许的 HTTP 方法。为空时使用默认值。
	AllowMethods []string

	// AllowHeaders 允许的请求头。为空时使用默认值。
	AllowHeaders []string

	// MaxAge 预检请求缓存秒数。0 表示不缓存。
	MaxAge int
}

// defaultAllowMethods CORS 默认允许的方法。
var defaultAllowMethods = []string{
	http.MethodGet,
	http.MethodPost,
	http.MethodPut,
	http.MethodPatch,
	http.MethodDelete,
	http.MethodOptions,
}

// defaultAllowHeaders CORS 默认允许的请求头。
var defaultAllowHeaders = []string{
	"Content-Type",
	"Authorization",
	"X-Requested-With",
	"x-api-key",
}

// CORS 返回 CORS 中间件，处理跨域请求和 OPTIONS 预检。
//
// 不传参数时仅处理 OPTIONS 预检（同源策略），传入 CORSConfig 可定制允许的来源。
func CORS(cfgs ...CORSConfig) gin.HandlerFunc {
	cfg := CORSConfig{}
	if len(cfgs) > 0 {
		cfg = cfgs[0]
	}

	methods := cfg.AllowMethods
	if len(methods) == 0 {
		methods = defaultAllowMethods
	}
	headers := cfg.AllowHeaders
	if len(headers) == 0 {
		headers = defaultAllowHeaders
	}
	maxAge := cfg.MaxAge
	if maxAge == 0 {
		maxAge = 86400 // 24 小时
	}

	allowAll := false
	originSet := make(map[string]struct{}, len(cfg.AllowOrigins))
	for _, o := range cfg.AllowOrigins {
		if o == "*" {
			allowAll = true
		}
		originSet[strings.ToLower(o)] = struct{}{}
	}

	methodsStr := strings.Join(methods, ", ")
	headersStr := strings.Join(headers, ", ")
	maxAgeStr := strconv.Itoa(maxAge)

	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin == "" {
			// 同源请求，无需 CORS 头
			c.Next()
			return
		}

		// 判断是否允许该来源
		allowed := false
		if allowAll {
			allowed = true
		} else if len(originSet) > 0 {
			_, allowed = originSet[strings.ToLower(origin)]
		}

		if !allowed {
			// 来源不在白名单中：不设置 CORS 头，浏览器会自行拒绝
			if c.Request.Method == http.MethodOptions {
				c.AbortWithStatus(http.StatusForbidden)
				return
			}
			c.Next()
			return
		}

		// 设置 CORS 响应头
		c.Header("Access-Control-Allow-Origin", origin)
		c.Header("Access-Control-Allow-Methods", methodsStr)
		c.Header("Access-Control-Allow-Headers", headersStr)
		c.Header("Access-Control-Allow-Credentials", "true")
		c.Header("Access-Control-Max-Age", maxAgeStr)

		// 预检请求直接返回 204
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}
