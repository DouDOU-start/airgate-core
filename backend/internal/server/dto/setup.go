package dto

// SetupStatusResp 安装状态响应
type SetupStatusResp struct {
	NeedsSetup bool `json:"needs_setup"`
	// EnvDB 当 DB_HOST/DB_PORT/DB_USER/DB_PASSWORD/DB_NAME 全部由环境变量提供且实际可连通时填充
	// 前端检测到此字段非 nil 时跳过数据库配置步骤；后端 install 时会忽略前端传入的 database 字段
	// password 字段始终不返回，避免 wizard 页面在浏览器里能读出明文
	EnvDB *EnvDBHint `json:"env_db,omitempty"`
	// EnvRedis 当 REDIS_HOST/REDIS_PORT/REDIS_PASSWORD 全部由环境变量提供且实际可连通时填充
	EnvRedis *EnvRedisHint `json:"env_redis,omitempty"`
}

// EnvDBHint 环境变量预填的数据库配置（仅展示用，不含密码）
type EnvDBHint struct {
	Host    string `json:"host"`
	Port    int    `json:"port"`
	User    string `json:"user"`
	DBName  string `json:"dbname"`
	SSLMode string `json:"sslmode"`
}

// EnvRedisHint 环境变量预填的 Redis 配置（仅展示用，不含密码）
type EnvRedisHint struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	DB   int    `json:"db"`
}

// TestDBReq 测试数据库连接请求
type TestDBReq struct {
	Host     string `json:"host" binding:"required"`
	Port     int    `json:"port" binding:"required"`
	User     string `json:"user" binding:"required"`
	Password string `json:"password"`
	DBName   string `json:"dbname" binding:"required"`
	SSLMode  string `json:"sslmode"` // disable / require / verify-full
}

// TestRedisReq 测试 Redis 连接请求
type TestRedisReq struct {
	Host     string `json:"host" binding:"required"`
	Port     int    `json:"port" binding:"required"`
	Password string `json:"password"`
	DB       int    `json:"db"`
	TLS      bool   `json:"tls"`
}

// InstallReq 执行安装请求
type InstallReq struct {
	Database TestDBReq    `json:"database" binding:"required"`
	Redis    TestRedisReq `json:"redis" binding:"required"`
	Admin    AdminSetup   `json:"admin" binding:"required"`
}

// AdminSetup 管理员账户设置
type AdminSetup struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=6"`
}

// TestConnectionResp 测试连接响应
type TestConnectionResp struct {
	Success  bool   `json:"success"`
	ErrorMsg string `json:"error_msg,omitempty"`
}
