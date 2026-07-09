package handler

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/server/dto"
	"github.com/DouDOU-start/airgate-core/internal/server/response"
)

// ==================== 管理端：OAuth 客户端 CRUD ====================

// ListOAuthClients GET /api/v1/admin/oauth-clients
func (h *OAuthHandler) ListOAuthClients(c *gin.Context) {
	items, err := h.service.ListClients(c.Request.Context())
	if err != nil {
		httpCode, message := h.handleError("oauth_client_list_failed", "查询应用列表失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}
	list := make([]dto.OAuthClientResp, 0, len(items))
	for _, item := range items {
		list = append(list, toOAuthClientResp(item))
	}
	response.Success(c, list)
}

// CreateOAuthClient POST /api/v1/admin/oauth-clients
func (h *OAuthHandler) CreateOAuthClient(c *gin.Context) {
	var req dto.CreateOAuthClientReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	item, secret, err := h.service.CreateClient(c.Request.Context(), toOAuthClientMutation(
		req.Name, req.Description, req.RedirectURIs, req.FirstParty, enabled,
		req.ShowInNav, req.LaunchURL, req.Icon, req.SortOrder,
	))
	if err != nil {
		httpCode, message := h.handleError("oauth_client_create_failed", "创建应用失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}
	response.Success(c, toOAuthClientSecretResp(item, secret))
}

// UpdateOAuthClient PUT /api/v1/admin/oauth-clients/:id
func (h *OAuthHandler) UpdateOAuthClient(c *gin.Context) {
	id, err := ParseID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的应用 ID")
		return
	}
	var req dto.UpdateOAuthClientReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}
	item, err := h.service.UpdateClient(c.Request.Context(), id, toOAuthClientMutation(
		req.Name, req.Description, req.RedirectURIs, req.FirstParty, req.Enabled,
		req.ShowInNav, req.LaunchURL, req.Icon, req.SortOrder,
	))
	if err != nil {
		httpCode, message := h.handleError("oauth_client_update_failed", "更新应用失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}
	response.Success(c, toOAuthClientResp(item))
}

// DeleteOAuthClient DELETE /api/v1/admin/oauth-clients/:id
func (h *OAuthHandler) DeleteOAuthClient(c *gin.Context) {
	id, err := ParseID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的应用 ID")
		return
	}
	if err := h.service.DeleteClient(c.Request.Context(), id); err != nil {
		httpCode, message := h.handleError("oauth_client_delete_failed", "删除应用失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}
	response.Success(c, nil)
}

// ResetOAuthClientSecret POST /api/v1/admin/oauth-clients/:id/reset-secret
func (h *OAuthHandler) ResetOAuthClientSecret(c *gin.Context) {
	id, err := ParseID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的应用 ID")
		return
	}
	item, secret, err := h.service.ResetSecret(c.Request.Context(), id)
	if err != nil {
		httpCode, message := h.handleError("oauth_client_reset_secret_failed", "重置密钥失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}
	response.Success(c, toOAuthClientSecretResp(item, secret))
}

// ==================== 用户端：授权与应用导航 ====================

// GetAuthorizeInfo GET /api/v1/oauth/authorize-info
func (h *OAuthHandler) GetAuthorizeInfo(c *gin.Context) {
	var query dto.AuthorizeInfoQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		response.BindError(c, err)
		return
	}
	client, err := h.service.AuthorizeInfo(c.Request.Context(), query.ClientID, query.RedirectURI)
	if err != nil {
		httpCode, message := h.handleError("oauth_authorize_info_failed", "查询授权信息失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}
	response.Success(c, dto.AuthorizeInfoResp{
		Name:        client.Name,
		Description: client.Description,
		Icon:        client.Icon,
		FirstParty:  client.FirstParty,
	})
}

// Authorize POST /api/v1/oauth/authorize（登录态，SPA 授权页转发）
func (h *OAuthHandler) Authorize(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, http.StatusUnauthorized, "未登录")
		return
	}
	var req dto.AuthorizeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}
	code, err := h.service.Authorize(c.Request.Context(), userID, toAuthorizeInput(req))
	if err != nil {
		httpCode, message := h.handleError("oauth_authorize_failed", "授权失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}
	response.Success(c, dto.AuthorizeResp{Code: code, State: req.State})
}

// ListApps GET /api/v1/apps（用户端导航入口）
func (h *OAuthHandler) ListApps(c *gin.Context) {
	items, err := h.service.NavApps(c.Request.Context())
	if err != nil {
		httpCode, message := h.handleError("oauth_apps_list_failed", "查询应用入口失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}
	list := make([]dto.AppEntryResp, 0, len(items))
	for _, item := range items {
		list = append(list, toAppEntryResp(item))
	}
	response.Success(c, list)
}

// ==================== 协议端点（RFC 6749 原始形态，不走信封） ====================

// Token POST /oauth/token（公开；form 或 JSON）
func (h *OAuthHandler) Token(c *gin.Context) {
	var req dto.OAuthTokenReq
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusBadRequest, dto.OAuthErrorResp{Error: "invalid_request", ErrorDescription: err.Error()})
		return
	}
	out, err := h.service.ExchangeToken(c.Request.Context(), toTokenInput(req))
	if err != nil {
		httpCode, code := oauthProtocolError(err)
		c.JSON(httpCode, dto.OAuthErrorResp{Error: code})
		return
	}
	// RFC 6749 §5.1：令牌响应禁止缓存。
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, dto.OAuthTokenResp{
		AccessToken: out.AccessToken,
		TokenType:   out.TokenType,
		ExpiresIn:   out.ExpiresIn,
		Scope:       out.Scope,
	})
}

// UserInfo GET /oauth/userinfo（Bearer 访问令牌）
func (h *OAuthHandler) UserInfo(c *gin.Context) {
	token, ok := bearerAccessToken(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, dto.OAuthErrorResp{Error: "invalid_token"})
		return
	}
	info, err := h.service.ResolveUserInfo(c.Request.Context(), token)
	if err != nil {
		httpCode, code := oauthProtocolError(err)
		c.JSON(httpCode, dto.OAuthErrorResp{Error: code})
		return
	}
	name := info.Username
	if name == "" {
		name = info.Email
	}
	c.JSON(http.StatusOK, dto.OAuthUserInfoResp{
		Sub:   strconv.Itoa(info.ID),
		Name:  name,
		Email: info.Email,
	})
}

// ProvisionKey POST /oauth/provision-key（Bearer 访问令牌）
// 为令牌对应用户 get-or-create 该应用专属的 sk- key；明文仅供应用后端持有。
func (h *OAuthHandler) ProvisionKey(c *gin.Context) {
	token, ok := bearerAccessToken(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, dto.OAuthErrorResp{Error: "invalid_token"})
		return
	}
	var req dto.ProvisionKeyReq
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		// 空 body 合法（全部走默认值）；有 body 但格式非法才拒绝。
		c.JSON(http.StatusBadRequest, dto.OAuthErrorResp{Error: "invalid_request", ErrorDescription: err.Error()})
		return
	}
	result, err := h.service.ProvisionKey(c.Request.Context(), token, req.GroupID)
	if err != nil {
		httpCode, code := oauthProtocolError(err)
		c.JSON(httpCode, dto.OAuthErrorResp{Error: code, ErrorDescription: err.Error()})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, dto.ProvisionKeyResp{
		APIKey:  result.APIKey,
		KeyHint: result.KeyHint,
		Created: result.Created,
	})
}

// bearerAccessToken 从 Authorization 头提取 OAuth 访问令牌。
func bearerAccessToken(c *gin.Context) (string, bool) {
	raw := c.GetHeader("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(raw, prefix) {
		return "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(raw, prefix))
	return token, token != ""
}
