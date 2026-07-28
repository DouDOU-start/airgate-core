package handler

import (
	"errors"
	"html"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	appsettings "github.com/DouDOU-start/airgate-core/internal/app/settings"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
	"github.com/DouDOU-start/airgate-core/internal/server/response"
)

// GetPublicSettings 获取公开设置（无需认证）。
func (h *SettingsHandler) GetPublicSettings(c *gin.Context) {
	result, err := h.service.ListPublic(c.Request.Context())
	if err != nil {
		slog.Error("查询公共设置失败", "error", err)
		response.InternalError(c, "查询失败")
		return
	}

	response.Success(c, result)
}

// GetSettings 获取所有设置（脱敏语义收口在 service.ListMasked：
// security 组整组过滤、smtp_password 等敏感键掩码为哨兵值，handler 纯透传）。
func (h *SettingsHandler) GetSettings(c *gin.Context) {
	list, err := h.service.ListMasked(c.Request.Context(), c.Query("group"))
	if err != nil {
		slog.Error("查询设置失败", "error", err)
		response.InternalError(c, "查询失败")
		return
	}

	resp := make([]dto.SettingResp, 0, len(list))
	for _, item := range list {
		resp = append(resp, toSettingResp(item))
	}
	response.Success(c, resp)
}

// UpdateSettings 批量更新设置（掩码哨兵保持/security 组拒写语义在 service.Update）。
func (h *SettingsHandler) UpdateSettings(c *gin.Context) {
	var req dto.UpdateSettingsReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}

	items := make([]appsettings.ItemInput, 0, len(req.Settings))
	for _, item := range req.Settings {
		items = append(items, appsettings.ItemInput{
			Key:   item.Key,
			Value: item.Value,
			Group: item.Group,
		})
	}

	if err := h.service.Update(c.Request.Context(), items); err != nil {
		if errors.Is(err, appsettings.ErrSecuritySettingReadOnly) {
			response.BadRequest(c, err.Error())
			return
		}
		slog.Error("更新设置失败", "error", err)
		response.InternalError(c, "更新设置失败")
		return
	}

	response.Success(c, nil)
}

// TestSMTP 测试 SMTP 连接并发送测试邮件。
func (h *SettingsHandler) TestSMTP(c *gin.Context) {
	var req dto.TestSMTPReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}

	input := appsettings.TestSMTPInput{
		Host:     req.Host,
		Port:     req.Port,
		Username: req.Username,
		Password: req.Password,
		UseTLS:   req.UseTLS,
		From:     req.From,
		To:       req.To,
	}

	if err := h.service.TestSMTP(c.Request.Context(), input); err != nil {
		if errors.Is(err, appsettings.ErrSMTPConnection) {
			response.BadRequest(c, err.Error())
			return
		}
		response.InternalError(c, "SMTP 测试失败")
		return
	}

	response.Success(c, nil)
}

// TestWeChat 测试微信公众号模板消息。
func (h *SettingsHandler) TestWeChat(c *gin.Context) {
	var req dto.TestWeChatReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}

	input := appsettings.TestWeChatInput{
		AppID:      req.AppID,
		AppSecret:  req.AppSecret,
		TemplateID: req.TemplateID,
		OpenID:     req.OpenID,
		DetailURL:  req.DetailURL,
	}
	if err := h.service.TestWeChat(c.Request.Context(), input); err != nil {
		if errors.Is(err, appsettings.ErrWeChatConnection) {
			response.BadRequest(c, err.Error())
			return
		}
		response.InternalError(c, "微信公众号测试失败")
		return
	}

	response.Success(c, nil)
}

// CreateWeChatBind 创建管理员微信扫码绑定会话。
func (h *SettingsHandler) CreateWeChatBind(c *gin.Context) {
	result, err := h.service.CreateWeChatBind(c.Request.Context())
	if err != nil {
		if errors.Is(err, appsettings.ErrWeChatBinding) {
			response.BadRequest(c, err.Error())
			return
		}
		response.InternalError(c, "创建微信绑定二维码失败")
		return
	}
	response.Success(c, dto.WeChatBindSessionResp{
		ID: result.ID, OAuthURL: result.OAuthURL, ExpiresAt: result.ExpiresAt,
	})
}

// GetWeChatBindStatus 查询管理员微信扫码绑定状态。
func (h *SettingsHandler) GetWeChatBindStatus(c *gin.Context) {
	result, err := h.service.GetWeChatBindStatus(c.Request.Context(), c.Param("id"))
	if err != nil {
		if errors.Is(err, appsettings.ErrWeChatBinding) {
			response.BadRequest(c, err.Error())
			return
		}
		response.InternalError(c, "查询微信绑定状态失败")
		return
	}
	response.Success(c, dto.WeChatBindStatusResp{Status: result.Status, OpenIDHint: result.OpenIDHint})
}

// UnbindWeChat 解除管理员微信绑定。
func (h *SettingsHandler) UnbindWeChat(c *gin.Context) {
	if err := h.service.UnbindWeChat(c.Request.Context()); err != nil {
		if errors.Is(err, appsettings.ErrWeChatBinding) {
			response.BadRequest(c, err.Error())
			return
		}
		response.InternalError(c, "解除微信绑定失败")
		return
	}
	response.Success(c, nil)
}

// CompleteWeChatBind 接收微信网页授权回调。该路由无需登录，安全性由一次性随机 state 保证。
func (h *SettingsHandler) CompleteWeChatBind(c *gin.Context) {
	result, err := h.service.CompleteWeChatBind(c.Request.Context(), c.Query("code"), c.Query("state"))
	if err != nil {
		slog.Warn("wechat_admin_bind_callback_failed", "error", err)
		writeWeChatBindPage(c, http.StatusBadRequest, "绑定失败", err.Error(), false)
		return
	}
	writeWeChatBindPage(c, http.StatusOK, "绑定成功", "管理员微信已绑定，可以关闭此页面并返回管理后台。", result.Status == "bound")
}

func writeWeChatBindPage(c *gin.Context, status int, title, message string, success bool) {
	accent := "#dc2626"
	icon := "!"
	if success {
		accent = "#16a34a"
		icon = "✓"
	}
	body := `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>` + html.EscapeString(title) + `</title></head><body style="margin:0;background:#f5f7f6;color:#17201b;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif"><main style="min-height:100vh;display:grid;place-items:center;padding:24px;box-sizing:border-box"><section style="width:min(420px,100%);background:#fff;border:1px solid #e3e9e5;border-radius:20px;padding:36px 28px;text-align:center;box-shadow:0 20px 60px rgba(23,32,27,.08)"><div style="width:64px;height:64px;margin:0 auto 22px;border-radius:50%;display:grid;place-items:center;background:` + accent + `18;color:` + accent + `;font-size:32px;font-weight:700">` + icon + `</div><h1 style="margin:0 0 12px;font-size:24px">` + html.EscapeString(title) + `</h1><p style="margin:0;color:#657169;line-height:1.8;font-size:15px">` + html.EscapeString(message) + `</p></section></main></body></html>`
	c.Data(status, "text/html; charset=utf-8", []byte(body))
}

// UploadFile 上传站点级静态资源（logo / favicon 等）。
//
// 有意使用本地磁盘存储（data/uploads），不引入对象存储：这类资源是站点全局共享、
// 几乎不变、每次页面加载都被请求，本地静态服务 + 长缓存是最稳妥的方案。
func (h *SettingsHandler) UploadFile(c *gin.Context) {
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		response.BadRequest(c, "请选择要上传的文件")
		return
	}
	defer func() { _ = file.Close() }()

	// 限制 2MB
	if header.Size > 2<<20 {
		response.BadRequest(c, "文件大小不能超过 2MB")
		return
	}

	// 只允许位图图片。SVG 属于可执行文档（可内嵌脚本），同源存储后可被用作
	// 存储型 XSS 载体，明确禁止。
	ext := strings.ToLower(filepath.Ext(header.Filename))
	allowed := map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".ico": true, ".webp": true}
	if !allowed[ext] {
		response.BadRequest(c, "只支持 PNG/JPG/GIF/ICO/WebP 格式")
		return
	}

	// 保存到 data/uploads/
	uploadDir := "data/uploads"
	if err := os.MkdirAll(uploadDir, 0o755); err != nil {
		response.InternalError(c, "创建上传目录失败")
		return
	}

	// 文件名用 UUID：不可枚举，也与 /uploads 路由注释的安全承诺一致
	filename := uuid.NewString() + ext
	dst, err := os.Create(filepath.Join(uploadDir, filename))
	if err != nil {
		response.InternalError(c, "保存文件失败")
		return
	}
	defer func() { _ = dst.Close() }()

	if _, err := io.Copy(dst, file); err != nil {
		response.InternalError(c, "写入文件失败")
		return
	}

	url := "/uploads/" + filename
	response.Success(c, map[string]string{"url": url})
}

// GetAdminAPIKey 获取管理员 API Key 信息（仅返回脱敏 hint）。
func (h *SettingsHandler) GetAdminAPIKey(c *gin.Context) {
	list, err := h.service.List(c.Request.Context(), "security")
	if err != nil {
		slog.Error("查询管理员 API Key 失败", "error", err)
		response.InternalError(c, "查询失败")
		return
	}

	var hint string
	for _, item := range list {
		if item.Key == "admin_api_key_hint" {
			hint = item.Value
		}
	}
	if hint == "" {
		response.Success(c, nil)
		return
	}

	response.Success(c, dto.AdminAPIKeyResp{Hint: hint})
}

// GenerateAdminAPIKey 生成（或重新生成）管理员 API Key。
func (h *SettingsHandler) GenerateAdminAPIKey(c *gin.Context) {
	result, err := h.service.GenerateAdminAPIKey(c.Request.Context())
	if err != nil {
		if errors.Is(err, appsettings.ErrGenerateKey) {
			response.InternalError(c, "生成密钥失败")
			return
		}
		if errors.Is(err, appsettings.ErrEncryptKey) {
			response.InternalError(c, "加密密钥失败")
			return
		}
		response.InternalError(c, "保存密钥失败")
		return
	}

	response.Success(c, dto.AdminAPIKeyResp{Hint: result.Hint, Key: result.Key})
}

// DeleteAdminAPIKey 删除管理员 API Key（security 组键经通用 Update 已拒写，
// 走 service 专用通道）。
func (h *SettingsHandler) DeleteAdminAPIKey(c *gin.Context) {
	if err := h.service.DeleteAdminAPIKey(c.Request.Context()); err != nil {
		slog.Error("删除管理员 API Key 失败", "error", err)
		response.InternalError(c, "删除密钥失败")
		return
	}

	response.Success(c, nil)
}
