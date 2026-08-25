package openclaw

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"text/template"

	appsettings "github.com/DouDOU-start/airgate-core/internal/app/settings"
	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"
)

// Service 提供 OpenClaw 一键接入相关的领域用例。
//
// 设计要点：
//   - 只读取 setting，不写；写路径完全复用 settings.Service.Update。
//   - 任何 key 缺省都走 defaults.go 里的常量，避免数据库里没有行时返回空串。
//   - SiteName / 站点 BaseURL 的回退链在这里集中处理，handler 只负责拼 URL 和 HTTP 编排。
type Service struct {
	settings *appsettings.Service
}

// NewService 创建 OpenClaw 服务。
func NewService(settings *appsettings.Service) *Service {
	return &Service{settings: settings}
}

// Config 聚合渲染脚本/文档所需的全部字段，由上层 handler 填充再传入模板。
type Config struct {
	Enabled             bool
	ProviderName        string
	BaseURL             string // 最终写进 openclaw.json 的 airgate 站点 URL（不含 /v1 后缀）
	ModelsPresetJSON    string // 原样 JSON 文本，保留给脚本/前端
	MemorySearchEnabled bool
	MemorySearchModel   string
	SiteName            string
	GatewayMode         string // 写进 openclaw.json 的 gateway.mode，默认 local
}

// Load 读取 openclaw/site 两个分组的设置并应用默认值。
//
// 注意：BaseURL 这里返回的是 setting 中显式配置的值；如果为空，调用方（handler）
// 应再根据请求 Host 推导一个兜底 URL —— 把 Host 推导放在 service 里会让本包依赖 gin。
func (s *Service) Load(ctx context.Context) (Config, error) {
	cfg := Config{
		Enabled:             true,
		ProviderName:        DefaultProviderName,
		ModelsPresetJSON:    DefaultModelsPresetJSON,
		MemorySearchEnabled: false,
		MemorySearchModel:   DefaultMemorySearchModel,
		SiteName:            "AirGate",
		GatewayMode:         DefaultGatewayMode,
	}

	ocItems, err := s.settings.List(ctx, GroupName)
	if err != nil {
		sdk.LoggerFromContext(ctx).Error("openclaw_sync_failed",
			"stage", "load_openclaw_settings",
			sdk.LogFieldError, err)
		return cfg, err
	}
	for _, it := range ocItems {
		switch it.Key {
		case KeyEnabled:
			if it.Value != "" {
				cfg.Enabled = it.Value == "true"
			}
		case KeyProviderName:
			if v := strings.TrimSpace(it.Value); v != "" {
				cfg.ProviderName = v
			}
		case KeyBaseURL:
			cfg.BaseURL = strings.TrimRight(strings.TrimSpace(it.Value), "/")
		case KeyModelsPreset:
			if v := strings.TrimSpace(it.Value); v != "" {
				cfg.ModelsPresetJSON = v
			}
		case KeyMemorySearchEnabled:
			cfg.MemorySearchEnabled = it.Value == "true"
		case KeyMemorySearchModel:
			if v := strings.TrimSpace(it.Value); v != "" {
				cfg.MemorySearchModel = v
			}
		case KeyGatewayMode:
			if v := strings.TrimSpace(it.Value); v != "" {
				cfg.GatewayMode = v
			}
		}
	}

	// 站点名来自 site 分组，方便在文档/脚本中显示品牌名。
	siteItems, err := s.settings.List(ctx, "site")
	if err == nil {
		for _, it := range siteItems {
			if it.Key == "site_name" && strings.TrimSpace(it.Value) != "" {
				cfg.SiteName = it.Value
			}
			// 若 openclaw.base_url 没配，回退到 site.base_url。
			if cfg.BaseURL == "" && it.Key == "site_base_url" {
				cfg.BaseURL = strings.TrimRight(strings.TrimSpace(it.Value), "/")
			}
		}
	}

	return cfg, nil
}

// installScriptTemplateData 是两份安装脚本模板共享的变量集合。
//
// 因为 bash 和 PowerShell 都只需要 BaseURL / SiteName（其他 provider / memorySearch /
// gatewayMode 相关的渲染都挪到了服务端 render-config 端点），这里保留 ProviderName 等
// 字段是为了不破坏已有的 Go 模板（将来可能还会用到），text/template 不会因为字段
// 未被引用而报错。
type installScriptTemplateData struct {
	BaseURL             string
	SiteName            string
	ProviderName        string
	MemorySearchEnabled bool
	MemorySearchModel   string
	GatewayMode         string
}

func (s *Service) newInstallScriptData(cfg Config) installScriptTemplateData {
	gatewayMode := strings.TrimSpace(cfg.GatewayMode)
	if gatewayMode == "" {
		gatewayMode = DefaultGatewayMode
	}
	return installScriptTemplateData{
		BaseURL:             cfg.BaseURL,
		SiteName:            cfg.SiteName,
		ProviderName:        cfg.ProviderName,
		MemorySearchEnabled: cfg.MemorySearchEnabled,
		MemorySearchModel:   cfg.MemorySearchModel,
		GatewayMode:         gatewayMode,
	}
}

// RenderInstallScript 用 text/template 把 install.sh 模板中的占位符替换掉。
//
// 模板中使用 Go template 语法（{{.BaseURL}} 等），而不是简单字符串替换，
// 方便以后要加条件块（虽然当前版本已经去掉了所有客户端分支）。
func (s *Service) RenderInstallScript(cfg Config) (string, error) {
	tpl, err := template.New("install.sh").Parse(InstallScriptTemplate())
	if err != nil {
		slog.Default().Error("openclaw_sync_failed",
			"stage", "parse_install_sh",
			sdk.LogFieldError, err)
		return "", err
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, s.newInstallScriptData(cfg)); err != nil {
		slog.Default().Error("openclaw_sync_failed",
			"stage", "render_install_sh",
			sdk.LogFieldError, err)
		return "", err
	}
	return buf.String(), nil
}

// RenderInstallScriptPowerShell 渲染 Windows PowerShell 版安装脚本（/openclaw/install.ps1）。
//
// 与 bash 版共用模板数据结构，保证两份脚本嵌入的 BaseURL / SiteName 行为一致。
func (s *Service) RenderInstallScriptPowerShell(cfg Config) (string, error) {
	tpl, err := template.New("install.ps1").Parse(InstallScriptPowerShellTemplate())
	if err != nil {
		slog.Default().Error("openclaw_sync_failed",
			"stage", "parse_install_ps1",
			sdk.LogFieldError, err)
		return "", err
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, s.newInstallScriptData(cfg)); err != nil {
		slog.Default().Error("openclaw_sync_failed",
			"stage", "render_install_ps1",
			sdk.LogFieldError, err)
		return "", err
	}
	return buf.String(), nil
}
