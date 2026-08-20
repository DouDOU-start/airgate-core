package middleware

import (
	"crypto/sha256"
	"crypto/subtle"
	"net"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/pluginruntime/protocol"
	"github.com/DouDOU-start/airgate-core/internal/server/response"
)

// PluginTokenAuth 限制 Core 内部插件接口只能由回环地址携带进程期令牌访问。
// 来源判断直接使用 TCP 对端地址，不信任可由客户端伪造的代理转发头。
func PluginTokenAuth(expectedToken string) gin.HandlerFunc {
	expectedHash := sha256.Sum256([]byte(expectedToken))
	return func(c *gin.Context) {
		if !remoteAddrIsLoopback(c.Request.RemoteAddr) {
			response.Forbidden(c, "插件内部接口只允许本机访问")
			c.Abort()
			return
		}

		providedToken := c.GetHeader(protocol.CorePluginTokenHeader)
		providedHash := sha256.Sum256([]byte(providedToken))
		if expectedToken == "" || providedToken == "" || subtle.ConstantTimeCompare(expectedHash[:], providedHash[:]) != 1 {
			response.Unauthorized(c, "插件访问令牌无效")
			c.Abort()
			return
		}
		c.Next()
	}
}

func remoteAddrIsLoopback(remoteAddr string) bool {
	remoteAddr = strings.TrimSpace(remoteAddr)
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}
