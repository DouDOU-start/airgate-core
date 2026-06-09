package handler

import (
	"errors"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	appauth "github.com/DouDOU-start/airgate-core/internal/app/auth"
	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
	"github.com/DouDOU-start/airgate-core/internal/server/response"
)

// Login 用户登录。
func (h *AuthHandler) Login(c *gin.Context) {
	var req dto.LoginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}

	result, err := h.service.Login(c.Request.Context(), appauth.LoginInput{
		Email:    req.Email,
		Password: req.Password,
	})
	if err != nil {
		httpCode, message, unauthorized := h.handleLoginError(err)
		if unauthorized && httpCode == 401 {
			response.Unauthorized(c, message)
			return
		}
		if httpCode == 403 {
			response.Forbidden(c, message)
			return
		}
		if httpCode == 400 {
			response.BadRequest(c, message)
			return
		}
		response.InternalError(c, message)
		return
	}

	response.Success(c, dto.LoginResp{
		Token: result.Token,
		User:  userToResp(result.User),
	})
}

// LoginByAPIKey 使用 API Key 登录（仅能查看该 Key 的使用记录）。
func (h *AuthHandler) LoginByAPIKey(c *gin.Context) {
	var req dto.APIKeyLoginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}

	result, err := h.service.LoginByAPIKey(c.Request.Context(), appauth.LoginByAPIKeyInput{
		Key: req.Key,
	})
	if err != nil {
		httpCode, message := h.handleAPIKeyLoginError(err)
		switch httpCode {
		case 400:
			response.BadRequest(c, message)
		case 401:
			response.Unauthorized(c, message)
		case 403:
			response.Forbidden(c, message)
		default:
			response.InternalError(c, message)
		}
		return
	}

	userResp := userToResp(result.User)
	userResp.Role = auth.APIKeySessionRole
	userResp.APIKeyID = int64(result.APIKeyID)
	userResp.APIKeyName = result.APIKeyName
	userResp.APIKeyQuotaUSD = result.QuotaUSD
	userResp.APIKeyUsedQuota = result.UsedQuota
	userResp.APIKeyRate = result.Rate
	if result.ExpiresAt != nil {
		userResp.APIKeyExpiresAt = result.ExpiresAt.Format(time.RFC3339)
	}

	response.Success(c, dto.LoginResp{
		Token:      result.Token,
		User:       userResp,
		APIKeyID:   int64(result.APIKeyID),
		APIKeyName: result.APIKeyName,
	})
}

// Register 用户注册。
func (h *AuthHandler) Register(c *gin.Context) {
	var req dto.RegisterReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}

	result, err := h.service.Register(c.Request.Context(), appauth.RegisterInput{
		Email:      req.Email,
		Password:   req.Password,
		Username:   req.Username,
		VerifyCode: req.VerifyCode,
	})
	if err != nil {
		httpCode, message := h.handleRegisterError(err)
		switch httpCode {
		case 400:
			response.BadRequest(c, message)
		case 403:
			response.Forbidden(c, message)
		default:
			response.InternalError(c, message)
		}
		return
	}

	response.Success(c, dto.LoginResp{
		Token: result.Token,
		User:  userToResp(result.User),
	})
}

// VerifyCode 校验验证码（不消耗）。
func (h *AuthHandler) VerifyCode(c *gin.Context) {
	var req dto.VerifyCodeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}
	if !h.service.CheckVerifyCode(req.Email, req.Code) {
		response.BadRequest(c, "验证码无效或已过期")
		return
	}
	response.Success(c, nil)
}

// SendVerifyCode 发送邮箱验证码。
func (h *AuthHandler) SendVerifyCode(c *gin.Context) {
	var req dto.SendVerifyCodeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}

	if err := h.service.SendVerifyCode(c.Request.Context(), appauth.SendVerifyCodeInput{
		Email: req.Email,
	}); err != nil {
		httpCode, message := h.handleSendVerifyCodeError(err)
		switch httpCode {
		case 400:
			response.BadRequest(c, message)
		default:
			response.InternalError(c, message)
		}
		return
	}

	response.Success(c, nil)
}

// refreshGrace 允许过期不超过此时间窗口的 token 执行刷新。
const refreshGrace = 2 * time.Hour

// RefreshToken 刷新 JWT Token。
// 接受已过期但不超过 refreshGrace 的旧 token，签发新 token。
func (h *AuthHandler) RefreshToken(c *gin.Context) {
	tokenStr := extractRefreshBearerToken(c)
	if tokenStr == "" {
		response.Unauthorized(c, "缺少认证 Token")
		return
	}

	claims, err := h.jwtMgr.ParseTokenForRefresh(tokenStr, refreshGrace)
	if err != nil {
		response.Unauthorized(c, "Token 无效或已过期")
		return
	}

	identity := appauth.AuthIdentity{
		UserID:   claims.UserID,
		Role:     claims.Role,
		Email:    claims.Email,
		APIKeyID: claims.APIKeyID,
	}

	token, err := h.service.RefreshToken(c.Request.Context(), identity)
	if err != nil {
		if errors.Is(err, appauth.ErrInvalidAPIKeySession) {
			response.Unauthorized(c, "API Key 登录会话已失效，请重新登录")
			return
		}
		if errors.Is(err, appauth.ErrUserDisabled) {
			response.Forbidden(c, "用户已被禁用")
			return
		}
		response.InternalError(c, "刷新 Token 失败")
		return
	}

	response.Success(c, dto.RefreshResp{
		Token: token,
	})
}

func extractRefreshBearerToken(c *gin.Context) string {
	header := c.GetHeader("Authorization")
	if header == "" {
		return ""
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		return strings.TrimSpace(parts[1])
	}
	return ""
}
