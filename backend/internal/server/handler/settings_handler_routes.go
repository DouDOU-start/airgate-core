package handler

import (
	"errors"
	"io"
	"log/slog"
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

// TestBark 测试 Bark 推送。
func (h *SettingsHandler) TestBark(c *gin.Context) {
	var req dto.TestBarkReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}

	input := appsettings.TestBarkInput{
		Server:    req.Server,
		DeviceKey: req.DeviceKey,
		Group:     req.Group,
		Sound:     req.Sound,
		Level:     req.Level,
		DetailURL: req.DetailURL,
	}
	if err := h.service.TestBark(c.Request.Context(), input); err != nil {
		if errors.Is(err, appsettings.ErrBarkConnection) {
			response.BadRequest(c, err.Error())
			return
		}
		response.InternalError(c, "Bark 测试失败")
		return
	}

	response.Success(c, nil)
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
