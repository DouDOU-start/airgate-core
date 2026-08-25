package handler

import (
	"runtime"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/server/response"
	"github.com/DouDOU-start/airgate-core/internal/version"
)

// VersionHandler 暴露 core 版本信息（管理员可见）。
type VersionHandler struct{}

// NewVersionHandler 创建 VersionHandler。
func NewVersionHandler() *VersionHandler {
	return &VersionHandler{}
}

// GetVersion 返回 core 版本号、Go 版本和运行平台。
func (h *VersionHandler) GetVersion(c *gin.Context) {
	response.Success(c, gin.H{
		"version":    version.Version,
		"go_version": runtime.Version(),
		"platform":   runtime.GOOS + "/" + runtime.GOARCH,
	})
}
