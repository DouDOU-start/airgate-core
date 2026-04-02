package handler

import (
	"github.com/gin-gonic/gin"

	appauth "github.com/DouDOU-start/airgate-core/internal/app/auth"
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
		TOTPCode: req.TOTPCode,
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

// Register 用户注册。
func (h *AuthHandler) Register(c *gin.Context) {
	var req dto.RegisterReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}

	result, err := h.service.Register(c.Request.Context(), appauth.RegisterInput{
		Email:    req.Email,
		Password: req.Password,
		Username: req.Username,
	})
	if err != nil {
		httpCode, message := h.handleRegisterError(err)
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

// TOTPSetup 启用 TOTP，生成密钥。
func (h *AuthHandler) TOTPSetup(c *gin.Context) {
	identity, ok := authIdentityFromContext(c)
	if !ok {
		response.Unauthorized(c, "用户未认证")
		return
	}

	result, err := h.service.TOTPSetup(c.Request.Context(), identity)
	if err != nil {
		httpCode, message := h.handleTOTPSetupError(err)
		switch httpCode {
		case 400:
			response.BadRequest(c, message)
		case 401:
			response.Unauthorized(c, message)
		default:
			response.InternalError(c, message)
		}
		return
	}

	response.Success(c, dto.TOTPSetupResp{
		Secret: result.Secret,
		URI:    result.URI,
	})
}

// TOTPVerify 验证 TOTP 验证码。
func (h *AuthHandler) TOTPVerify(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		response.Unauthorized(c, "用户未认证")
		return
	}

	var req dto.TOTPVerifyReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}

	if err := h.service.TOTPVerify(c.Request.Context(), userID, req.Code); err != nil {
		httpCode, message := h.handleTOTPVerifyError(err)
		if httpCode == 400 {
			response.BadRequest(c, message)
			return
		}
		response.InternalError(c, message)
		return
	}

	response.Success(c, nil)
}

// TOTPDisable 禁用 TOTP。
func (h *AuthHandler) TOTPDisable(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		response.Unauthorized(c, "用户未认证")
		return
	}

	var req dto.TOTPVerifyReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}

	if err := h.service.TOTPDisable(c.Request.Context(), userID, req.Code); err != nil {
		httpCode, message := h.handleTOTPDisableError(err)
		if httpCode == 400 {
			response.BadRequest(c, message)
			return
		}
		response.InternalError(c, message)
		return
	}

	response.Success(c, nil)
}

// RefreshToken 刷新 JWT Token。
func (h *AuthHandler) RefreshToken(c *gin.Context) {
	identity, ok := authIdentityFromContext(c)
	if !ok {
		response.Unauthorized(c, "用户未认证")
		return
	}

	token, err := h.service.RefreshToken(identity)
	if err != nil {
		response.InternalError(c, "刷新 Token 失败")
		return
	}

	response.Success(c, dto.RefreshResp{
		Token: token,
	})
}
