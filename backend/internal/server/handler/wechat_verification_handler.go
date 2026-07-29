package handler

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/server/dto"
	"github.com/DouDOU-start/airgate-core/internal/server/response"
)

const (
	wechatVerificationDir         = "data/wechat-verification"
	wechatVerificationMaxFileSize = 64 * 1024
)

var wechatVerificationFilenamePattern = regexp.MustCompile(`^MP_verify_[A-Za-z0-9_-]+\.txt$`)

// ListWeChatVerificationFiles 返回已上传的微信域名校验文件。
func (h *SettingsHandler) ListWeChatVerificationFiles(c *gin.Context) {
	entries, err := os.ReadDir(wechatVerificationDir)
	if err != nil {
		if os.IsNotExist(err) {
			response.Success(c, []dto.WeChatVerificationFileResp{})
			return
		}
		response.InternalError(c, "读取微信域名校验文件失败")
		return
	}

	files := make([]dto.WeChatVerificationFileResp, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !isWeChatVerificationFilename(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		files = append(files, newWeChatVerificationFileResp(entry.Name(), info.Size(), info.ModTime()))
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].UpdatedAt.After(files[j].UpdatedAt)
	})
	response.Success(c, files)
}

// UploadWeChatVerificationFile 保存微信后台下载的原始 MP_verify_*.txt 文件。
// 文件内容按原始字节落盘，不做换行、编码或空白字符转换，确保微信校验结果一致。
func (h *SettingsHandler) UploadWeChatVerificationFile(c *gin.Context) {
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		response.BadRequest(c, "请选择微信下载的域名校验文件")
		return
	}
	defer func() { _ = file.Close() }()

	filename := strings.TrimSpace(header.Filename)
	if filepath.Base(filename) != filename || !isWeChatVerificationFilename(filename) {
		response.BadRequest(c, "文件名必须符合 MP_verify_*.txt 格式")
		return
	}
	if header.Size <= 0 {
		response.BadRequest(c, "微信域名校验文件不能为空")
		return
	}
	if header.Size > wechatVerificationMaxFileSize {
		response.BadRequest(c, "微信域名校验文件不能超过 64KB")
		return
	}

	data, err := io.ReadAll(io.LimitReader(file, wechatVerificationMaxFileSize+1))
	if err != nil {
		response.InternalError(c, "读取微信域名校验文件失败")
		return
	}
	if len(data) == 0 {
		response.BadRequest(c, "微信域名校验文件不能为空")
		return
	}
	if len(data) > wechatVerificationMaxFileSize {
		response.BadRequest(c, "微信域名校验文件不能超过 64KB")
		return
	}
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		response.BadRequest(c, "微信域名校验文件必须是有效的纯文本文件")
		return
	}

	if err := os.MkdirAll(wechatVerificationDir, 0o755); err != nil {
		response.InternalError(c, "创建微信域名校验文件目录失败")
		return
	}
	temporary, err := os.CreateTemp(wechatVerificationDir, ".wechat-verification-*")
	if err != nil {
		response.InternalError(c, "保存微信域名校验文件失败")
		return
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()

	if err := temporary.Chmod(0o644); err != nil {
		_ = temporary.Close()
		response.InternalError(c, "设置微信域名校验文件权限失败")
		return
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		response.InternalError(c, "写入微信域名校验文件失败")
		return
	}
	if err := temporary.Close(); err != nil {
		response.InternalError(c, "保存微信域名校验文件失败")
		return
	}

	target := filepath.Join(wechatVerificationDir, filename)
	if err := os.Rename(temporaryName, target); err != nil {
		response.InternalError(c, "保存微信域名校验文件失败")
		return
	}
	info, err := os.Stat(target)
	if err != nil {
		response.InternalError(c, "读取微信域名校验文件状态失败")
		return
	}
	response.Success(c, newWeChatVerificationFileResp(filename, info.Size(), info.ModTime()))
}

// DeleteWeChatVerificationFile 删除指定微信域名校验文件。
func (h *SettingsHandler) DeleteWeChatVerificationFile(c *gin.Context) {
	filename := strings.TrimSpace(c.Param("filename"))
	if !isWeChatVerificationFilename(filename) {
		response.BadRequest(c, "微信域名校验文件名不合法")
		return
	}
	if err := os.Remove(filepath.Join(wechatVerificationDir, filename)); err != nil {
		if os.IsNotExist(err) {
			response.NotFound(c, "微信域名校验文件不存在")
			return
		}
		response.InternalError(c, "删除微信域名校验文件失败")
		return
	}
	response.Success(c, nil)
}

// ServeWeChatVerificationFile 在站点根路径原样输出微信域名校验文件。
// 返回值表示当前请求是否属于微信校验文件，供 SPA NoRoute 决定是否继续回退 index.html。
func (h *SettingsHandler) ServeWeChatVerificationFile(c *gin.Context) bool {
	filename := strings.TrimPrefix(c.Request.URL.Path, "/")
	if strings.Contains(filename, "/") || !isWeChatVerificationFilename(filename) {
		return false
	}
	if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
		c.Status(http.StatusMethodNotAllowed)
		return true
	}

	file, err := os.Open(filepath.Join(wechatVerificationDir, filename))
	if err != nil {
		if os.IsNotExist(err) {
			c.Data(http.StatusNotFound, "text/plain; charset=utf-8", []byte("微信域名校验文件不存在"))
			return true
		}
		slog.Error("wechat_verification_file_open_failed", "filename", filename, "error", err)
		c.Data(http.StatusInternalServerError, "text/plain; charset=utf-8", []byte("读取微信域名校验文件失败"))
		return true
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > wechatVerificationMaxFileSize {
		c.Data(http.StatusNotFound, "text/plain; charset=utf-8", []byte("微信域名校验文件不存在"))
		return true
	}
	data, err := io.ReadAll(io.LimitReader(file, wechatVerificationMaxFileSize+1))
	if err != nil || len(data) > wechatVerificationMaxFileSize {
		slog.Error("wechat_verification_file_read_failed", "filename", filename, "error", err)
		c.Data(http.StatusInternalServerError, "text/plain; charset=utf-8", []byte("读取微信域名校验文件失败"))
		return true
	}

	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Disposition", fmt.Sprintf("inline; filename=%q", filename))
	c.Data(http.StatusOK, "text/plain; charset=utf-8", data)
	return true
}

func isWeChatVerificationFilename(filename string) bool {
	return len(filename) <= 180 && wechatVerificationFilenamePattern.MatchString(filename)
}

func newWeChatVerificationFileResp(filename string, size int64, updatedAt time.Time) dto.WeChatVerificationFileResp {
	return dto.WeChatVerificationFileResp{
		Filename:  filename,
		URL:       "/" + filename,
		Size:      size,
		UpdatedAt: updatedAt,
	}
}
