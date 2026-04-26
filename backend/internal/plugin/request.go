package plugin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/server/middleware"
	sdk "github.com/DouDOU-start/airgate-sdk"
)

// parseRequest 从 HTTP 请求构造 forwardState。认证 / body 读取 / 插件匹配失败时
// 直接写响应并返回 false。
func (f *Forwarder) parseRequest(c *gin.Context) (*forwardState, bool) {
	startedAt := time.Now()

	keyInfo, ok := requireKeyInfo(c)
	if !ok {
		return nil, false
	}

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		openAIError(c, http.StatusBadRequest, "invalid_request_error", "invalid_request", "读取请求体失败")
		return nil, false
	}

	path := requestPath(c)
	parsed := parseBody(body, c.GetHeader("Content-Type"))
	requestedPlatform := requestedPlatform(c, keyInfo)
	inst := f.matchPlugin(c, keyInfo, requestedPlatform, path)
	if inst == nil {
		return nil, false
	}

	return &forwardState{
		startedAt:         startedAt,
		requestPath:       path,
		body:              body,
		model:             parsed.Model,
		stream:            parsed.Stream,
		sessionID:         parsed.SessionID,
		requestedPlatform: requestedPlatform,
		keyInfo:           keyInfo,
		plugin:            inst,
	}, true
}

func requireKeyInfo(c *gin.Context) (*auth.APIKeyInfo, bool) {
	raw, exists := c.Get(middleware.CtxKeyKeyInfo)
	if !exists {
		writeUnauthenticated(c)
		return nil, false
	}
	keyInfo, ok := raw.(*auth.APIKeyInfo)
	if !ok || keyInfo == nil {
		writeUnauthenticated(c)
		return nil, false
	}
	return keyInfo, true
}

func writeUnauthenticated(c *gin.Context) {
	c.JSON(http.StatusUnauthorized, gin.H{
		"error": gin.H{
			"message": "未认证",
			"type":    "authentication_error",
			"code":    "missing_api_key",
		},
	})
}

func requestPath(c *gin.Context) string {
	if p := c.Param("path"); p != "" {
		return p
	}
	return c.Request.URL.Path
}

func requestedPlatform(c *gin.Context, keyInfo *auth.APIKeyInfo) string {
	if platform := strings.TrimSpace(c.GetHeader("X-Airgate-Platform")); platform != "" {
		return platform
	}
	return keyInfo.GroupPlatform
}

func parseBody(body []byte, contentType string) parsedRequest {
	var fields requestFields
	if json.Unmarshal(body, &fields) == nil {
		return parsedRequest{
			Model:     fields.Model,
			Stream:    fields.Stream,
			SessionID: fields.Metadata.UserID,
		}
	}
	if strings.HasPrefix(contentType, "multipart/") {
		return parseMultipartFields(body, contentType)
	}
	return parsedRequest{}
}

func requestNeedsImage(path, model string) bool {
	return isImageAPIPath(path) || isImageModel(model)
}

func isImageAPIPath(path string) bool {
	if path == "" {
		return false
	}
	u, err := url.Parse(path)
	if err == nil {
		path = u.Path
	}
	path = strings.TrimRight(strings.ToLower(path), "/")
	return strings.HasSuffix(path, "/images/generations") ||
		strings.HasSuffix(path, "/images/edits")
}

func isImageModel(model string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(model)), "image")
}

func parseMultipartFields(body []byte, contentType string) parsedRequest {
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil || params["boundary"] == "" {
		return parsedRequest{}
	}
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	var pr parsedRequest
	for {
		part, err := reader.NextPart()
		if err != nil {
			break
		}
		data, _ := io.ReadAll(part)
		_ = part.Close()
		switch part.FormName() {
		case "model":
			pr.Model = strings.TrimSpace(string(data))
		case "stream":
			pr.Stream = strings.TrimSpace(string(data)) == "true"
		}
	}
	return pr
}

// matchPlugin 按 (platform, path) 路由到具体插件。
// 插件未运行返回 503；路由不匹配返回 404。
func (f *Forwarder) matchPlugin(c *gin.Context, keyInfo *auth.APIKeyInfo, platform, path string) *PluginInstance {
	if platform != "" {
		inst := f.manager.MatchPluginByPlatformAndPath(platform, path)
		if inst != nil {
			return inst
		}
		slog.Warn("请求平台未找到可处理请求的插件",
			"group_id", keyInfo.GroupID,
			"platform", platform,
			"path", path)
		if f.manager.GetPluginByPlatform(platform) == nil {
			openAIError(c, http.StatusServiceUnavailable, "server_error", "plugin_unavailable", "插件不可用，请联系管理员")
		} else {
			openAIError(c, http.StatusNotFound, "invalid_request_error", "route_not_found", "当前平台不支持该 API 路径")
		}
		return nil
	}

	inst := f.manager.MatchPluginByPathPrefix(path)
	if inst == nil {
		openAIError(c, http.StatusNotFound, "invalid_request_error", "route_not_found", "未找到匹配的插件")
	}
	return inst
}

// buildPluginRequest 组装给插件的 sdk.ForwardRequest。流式场景会带上 Writer。
func buildPluginRequest(c *gin.Context, state *forwardState) *sdk.ForwardRequest {
	headers := buildHeaders(c.Request.Header, state.keyInfo)
	// 路径和方法显式塞进 header：sdk.ForwardRequest 里没有这两字段，
	// 插件侧 extractForwardedPath 会优先读取这对 header。
	headers.Set("X-Forwarded-Path", state.requestPath)
	headers.Set("X-Forwarded-Method", c.Request.Method)

	req := &sdk.ForwardRequest{
		Account: buildSDKAccount(state.account),
		Body:    state.body,
		Headers: headers,
		Model:   state.model,
		Stream:  state.stream,
	}
	if state.stream {
		req.Writer = c.Writer
	}
	return req
}

// buildHeaders 克隆请求头并附加 X-Airgate-* 系列（分组级 service_tier / 强制 instructions / 插件开关）。
func buildHeaders(source http.Header, keyInfo *auth.APIKeyInfo) http.Header {
	headers := source.Clone()
	if keyInfo.GroupServiceTier != "" {
		headers.Set("X-Airgate-Service-Tier", keyInfo.GroupServiceTier)
	}
	if keyInfo.GroupForceInstructions != "" {
		headers.Set("X-Airgate-Force-Instructions", keyInfo.GroupForceInstructions)
	}
	// 分组级插件开关：X-Airgate-Plugin-{plugin}-{key} 约定。
	for plugin, kv := range keyInfo.GroupPluginSettings {
		for k, v := range kv {
			if v == "" {
				continue
			}
			headers.Set("X-Airgate-Plugin-"+canonicalHeaderToken(plugin)+"-"+canonicalHeaderToken(k), v)
		}
	}
	return headers
}

// canonicalHeaderToken 把 snake_case / kebab-case 规范化为 HTTP header token 风格（首字母大写、下划线变连字符）。
func canonicalHeaderToken(s string) string {
	out := make([]byte, 0, len(s))
	upNext := true
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '_' || c == '-' || c == '.' {
			out = append(out, '-')
			upNext = true
			continue
		}
		if upNext && c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		out = append(out, c)
		upNext = false
	}
	return string(out)
}

func buildSDKAccount(account *ent.Account) *sdk.Account {
	return &sdk.Account{
		ID:          int64(account.ID),
		Name:        account.Name,
		Platform:    account.Platform,
		Type:        account.Type,
		Credentials: account.Credentials,
		ProxyURL:    buildProxyURL(account),
	}
}

func buildProxyURL(account *ent.Account) string {
	proxy, err := account.Edges.ProxyOrErr()
	if err != nil || proxy == nil {
		return ""
	}
	if proxy.Username != "" {
		return fmt.Sprintf("%s://%s:%s@%s:%d", proxy.Protocol, proxy.Username, proxy.Password, proxy.Address, proxy.Port)
	}
	return fmt.Sprintf("%s://%s:%d", proxy.Protocol, proxy.Address, proxy.Port)
}
