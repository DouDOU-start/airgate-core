package plugin

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/server/middleware"
)

// requestFields 从 JSON body 中一次性解析需要的字段。
type requestFields struct {
	Model    string `json:"model"`
	Stream   bool   `json:"stream"`
	Metadata struct {
		UserID string `json:"user_id"`
	} `json:"metadata"`
}

func (f *Forwarder) buildForwardState(c *gin.Context) (*forwardState, bool) {
	startedAt := time.Now()

	keyInfo, ok := getForwardKeyInfo(c)
	if !ok {
		return nil, false
	}

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		openAIError(c, http.StatusBadRequest, "invalid_request_error", "invalid_request", "读取请求体失败")
		return nil, false
	}

	requestPath := resolveForwardRequestPath(c)
	parsed := parseForwardRequestBody(body, requestPath)
	inst := f.matchForwardPlugin(c, keyInfo, requestPath)
	if inst == nil {
		return nil, false
	}

	return &forwardState{
		startedAt:   startedAt,
		requestPath: requestPath,
		body:        body,
		model:       parsed.Model,
		stream:      parsed.Stream,
		sessionID:   parsed.SessionID,
		keyInfo:     keyInfo,
		plugin:      inst,
	}, true
}

func getForwardKeyInfo(c *gin.Context) (*auth.APIKeyInfo, bool) {
	keyInfoRaw, exists := c.Get(middleware.CtxKeyKeyInfo)
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": gin.H{
				"message": "未认证",
				"type":    "authentication_error",
				"code":    "missing_api_key",
			},
		})
		return nil, false
	}

	keyInfo, ok := keyInfoRaw.(*auth.APIKeyInfo)
	if !ok || keyInfo == nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": gin.H{
				"message": "未认证",
				"type":    "authentication_error",
				"code":    "missing_api_key",
			},
		})
		return nil, false
	}

	return keyInfo, true
}

func resolveForwardRequestPath(c *gin.Context) string {
	requestPath := c.Param("path")
	if requestPath == "" {
		requestPath = c.Request.URL.Path
	}
	return requestPath
}

func parseForwardRequestBody(body []byte, requestPath string) parsedForwardRequest {
	fields := decodeRequestFields(body)
	parsed := parsedForwardRequest{
		Model:     fields.Model,
		Stream:    fields.Stream,
		SessionID: fields.Metadata.UserID,
	}
	if shouldForceStreamForPath(requestPath) {
		parsed.Stream = true
	}
	return parsed
}

func decodeRequestFields(body []byte) requestFields {
	var parsed requestFields
	_ = json.Unmarshal(body, &parsed)
	return parsed
}

// extractModelAndStream 从 JSON body 中提取 model 和 stream 字段。
func extractModelAndStream(body []byte) (string, bool) {
	parsed := decodeRequestFields(body)
	return parsed.Model, parsed.Stream
}

// extractSessionID 从 JSON body 的 metadata.user_id 中提取会话 ID。
func extractSessionID(body []byte) string {
	parsed := decodeRequestFields(body)
	return parsed.Metadata.UserID
}

func shouldForceStreamForPath(requestPath string) bool {
	return strings.HasSuffix(requestPath, "/responses")
}

func (f *Forwarder) matchForwardPlugin(c *gin.Context, keyInfo *auth.APIKeyInfo, requestPath string) *PluginInstance {
	if keyInfo.GroupPlatform != "" {
		inst := f.manager.MatchPluginByPlatformAndPath(keyInfo.GroupPlatform, requestPath)
		if inst != nil {
			return inst
		}

		slog.Warn("分组平台未找到可处理请求的插件",
			"group_id", keyInfo.GroupID,
			"platform", keyInfo.GroupPlatform,
			"path", requestPath,
		)
		openAIError(c, http.StatusNotFound, "invalid_request_error", "route_not_found", "当前 API Key 绑定的平台不支持该 API 路径")
		return nil
	}

	inst := f.manager.MatchPluginByPathPrefix(requestPath)
	if inst == nil {
		openAIError(c, http.StatusNotFound, "invalid_request_error", "route_not_found", "未找到匹配的插件")
		return nil
	}
	return inst
}
