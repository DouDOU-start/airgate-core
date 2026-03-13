package setup

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/config"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
	"github.com/DouDOU-start/airgate-core/internal/server/response"
)

// 安装完成回调
var onInstallDone func()

// RegisterRoutes 注册安装向导路由（无回调）
func RegisterRoutes(r *gin.Engine) {
	RegisterRoutesWithCallback(r, nil)
}

// RegisterRoutesWithCallback 注册安装向导路由，安装成功后触发回调
func RegisterRoutesWithCallback(r *gin.Engine, callback func()) {
	onInstallDone = callback
	setup := r.Group("/setup")
	{
		setup.GET("/status", handleStatus)
		guarded := setup.Group("")
		guarded.Use(setupGuard())
		guarded.POST("/test-db", handleTestDB)
		guarded.POST("/test-redis", handleTestRedis)
		guarded.POST("/install", handleInstall)
	}
}

// setupGuard 安装保护中间件：安装完成后禁止访问
func setupGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !NeedsSetup() {
			response.Error(c, http.StatusForbidden, 403, "系统已安装")
			c.Abort()
			return
		}
		c.Next()
	}
}

func handleStatus(c *gin.Context) {
	response.Success(c, dto.SetupStatusResp{
		NeedsSetup: NeedsSetup(),
	})
}

func handleTestDB(c *gin.Context) {
	var req dto.TestDBReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	err := TestDBConnection(req.Host, req.Port, req.User, req.Password, req.DBName, req.SSLMode)
	if err != nil {
		response.Success(c, dto.TestConnectionResp{Success: false, ErrorMsg: err.Error()})
		return
	}
	response.Success(c, dto.TestConnectionResp{Success: true})
}

func handleTestRedis(c *gin.Context) {
	var req dto.TestRedisReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	err := TestRedisConnection(req.Host, req.Port, req.Password, req.DB)
	if err != nil {
		response.Success(c, dto.TestConnectionResp{Success: false, ErrorMsg: err.Error()})
		return
	}
	response.Success(c, dto.TestConnectionResp{Success: true})
}

func handleInstall(c *gin.Context) {
	var req dto.InstallReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	params := InstallParams{
		DB: config.DatabaseConfig{
			Host:     req.Database.Host,
			Port:     req.Database.Port,
			User:     req.Database.User,
			Password: req.Database.Password,
			DBName:   req.Database.DBName,
			SSLMode:  req.Database.SSLMode,
		},
		Redis: config.RedisConfig{
			Host:     req.Redis.Host,
			Port:     req.Redis.Port,
			Password: req.Redis.Password,
			DB:       req.Redis.DB,
			TLS:      req.Redis.TLS,
		},
	}
	params.Admin.Email = req.Admin.Email
	params.Admin.Password = req.Admin.Password

	if err := Install(params); err != nil {
		response.InternalError(c, err.Error())
		return
	}

	response.Success(c, nil)

	// 安装成功，触发回调通知主进程切换到正常模式
	if onInstallDone != nil {
		go func() {
			// 延迟一点让响应先发回前端
			time.Sleep(500 * time.Millisecond)
			onInstallDone()
		}()
	}
}
