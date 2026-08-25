package dto

// PluginResp 插件响应
type PluginResp struct {
	Name                string             `json:"name"`
	DisplayName         string             `json:"display_name,omitempty"`
	Version             string             `json:"version,omitempty"`
	Author              string             `json:"author,omitempty"`
	Type                string             `json:"type,omitempty"`
	Platform            string             `json:"platform"`
	AccountTypes        []AccountTypeResp  `json:"account_types,omitempty"`
	FrontendPages       []FrontendPageResp `json:"frontend_pages,omitempty"`
	InstructionPresets  []string           `json:"instruction_presets,omitempty"`
	ConfigSchemaVersion string             `json:"config_schema_version,omitempty"`
	ConfigSchema        []ConfigFieldResp  `json:"config_schema,omitempty"`
	Metadata            map[string]string  `json:"metadata,omitempty"`
	HasWebAssets        bool               `json:"has_web_assets"`
	IsDev               bool               `json:"is_dev"`
}

// ConfigFieldResp 插件配置字段声明
type ConfigFieldResp struct {
	Key         string            `json:"key"`
	FallbackKey string            `json:"fallback_key,omitempty"`
	Label       string            `json:"label,omitempty"`
	Type        string            `json:"type"`
	Widget      string            `json:"widget,omitempty"`
	DataSource  string            `json:"data_source,omitempty"`
	Required    bool              `json:"required,omitempty"`
	Default     any               `json:"default,omitempty"`
	Description string            `json:"description,omitempty"`
	Placeholder string            `json:"placeholder,omitempty"`
	Filter      map[string]string `json:"filter,omitempty"`
}

// PluginConfigResp 插件持久化配置
type PluginConfigResp struct {
	Config map[string]string `json:"config"`
}

// PluginConfigUpdateReq 插件配置更新请求
type PluginConfigUpdateReq struct {
	Config map[string]string `json:"config" binding:"required"`
}

// FrontendPageResp 前端页面声明响应
type FrontendPageResp struct {
	Path        string `json:"path"`
	Title       string `json:"title"`
	Icon        string `json:"icon,omitempty"`
	Description string `json:"description,omitempty"`
	// Audience: "admin" | "user" | "all"，空字符串视为 "admin"（向后兼容）
	Audience string `json:"audience,omitempty"`
}

// InstallGithubReq 从 GitHub 安装插件请求
type InstallGithubReq struct {
	Repo string `json:"repo" binding:"required"` // owner/repo 或完整 GitHub URL
}

// PluginOAuthStartResp 插件 OAuth 授权开始响应
type PluginOAuthStartResp struct {
	AuthorizeURL string `json:"authorize_url"`
	State        string `json:"state"`
}

// PluginOAuthExchangeReq 插件 OAuth 回调交换请求
type PluginOAuthExchangeReq struct {
	CallbackURL string `json:"callback_url" binding:"required"`
}

// PluginOAuthExchangeResp 插件 OAuth 回调交换响应
type PluginOAuthExchangeResp struct {
	AccountType string            `json:"account_type"`
	AccountName string            `json:"account_name"`
	Credentials map[string]string `json:"credentials"`
}

// MarketplacePluginResp 插件市场条目
type MarketplacePluginResp struct {
	Name             string `json:"name"`
	Version          string `json:"version"`
	Description      string `json:"description"`
	Author           string `json:"author"`
	Type             string `json:"type"`
	GithubRepo       string `json:"github_repo,omitempty"`
	Installed        bool   `json:"installed"`
	InstalledVersion string `json:"installed_version,omitempty"`
	HasUpdate        bool   `json:"has_update,omitempty"`
}
