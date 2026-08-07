// Package cpa 将 CLIProxyAPI（固定模块版本 v7.x）的订阅账号转发能力桥接到 airgate。
//
// 仅依赖 CPA 公开 SDK：
//   - sdk/config
//   - sdk/cliproxy (+ auth Manager)
//   - sdk/translator/builtin（注册翻译器）
//
// 不 import CPA internal/*。内置 executor 通过 cliproxy.Service.Run 的
// registerConfigAPIKeyAuths 路径，用占位 API Key 触发 ensureExecutors 注册到共享 Manager。
//
// 依赖约束：本包禁止 import ent / internal/app。
package cpa

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v7/sdk/config"

	// 注册 CPA 全部内置翻译器。
	_ "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator/builtin"
)

// Bridge 持有已注册 baseline executor 的 CPA Manager。
type Bridge struct {
	cfg     *sdkconfig.Config
	manager *coreauth.Manager
	svc     *cliproxy.Service

	mu             sync.RWMutex
	ready          bool
	initErr        error
	cancel         context.CancelFunc
	refreshLocks   sync.Map
	refreshedAuths sync.Map
}

// NewBridge 创建桥接层并异步启动嵌入式 cliproxy.Service 以注册 executor。
// 调用 Forward 前建议 WaitReady。cfg 可为 nil。
func NewBridge(cfg *sdkconfig.Config) *Bridge {
	b := &Bridge{}
	if err := b.init(cfg); err != nil {
		b.initErr = err
	}
	return b
}

// Manager 返回底层 CPA auth Manager。
func (b *Bridge) Manager() *coreauth.Manager {
	if b == nil {
		return nil
	}
	return b.manager
}

// Ready 报告 baseline executor 是否已就绪。
func (b *Bridge) Ready() bool {
	if b == nil {
		return false
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.ready
}

// InitError 返回初始化错误。
func (b *Bridge) InitError() error {
	if b == nil {
		return fmt.Errorf("cpa bridge 未初始化")
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.initErr
}

// WaitReady 等待 executor 注册完成或超时。
func (b *Bridge) WaitReady(ctx context.Context) error {
	if b == nil {
		return fmt.Errorf("cpa bridge 未初始化")
	}
	if err := b.InitError(); err != nil && !b.Ready() {
		// 初始化同步错误立即返回；异步错误在轮询中再查
		if b.manager == nil {
			return err
		}
	}
	deadline := time.Now().Add(45 * time.Second)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	for {
		if b.Ready() {
			return nil
		}
		if err := b.InitError(); err != nil && b.manager == nil {
			return err
		}
		if time.Now().After(deadline) {
			if err := b.InitError(); err != nil {
				return err
			}
			return fmt.Errorf("cpa bridge 等待 executor 注册超时")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// Executor 按 provider 取已注册执行器。
func (b *Bridge) Executor(provider string) (coreauth.ProviderExecutor, bool) {
	if b == nil || b.manager == nil {
		return nil, false
	}
	return b.manager.Executor(normalizeProvider(provider))
}

// HasProvider 报告 provider 是否已注册。
func (b *Bridge) HasProvider(provider string) bool {
	_, ok := b.Executor(provider)
	return ok
}

// EnsureExecutor 确保 provider 有执行器。
func (b *Bridge) EnsureExecutor(provider string) (coreauth.ProviderExecutor, error) {
	if b == nil || b.manager == nil {
		return nil, fmt.Errorf("cpa bridge 未初始化")
	}
	key := normalizeProvider(provider)
	if ex, ok := b.manager.Executor(key); ok && ex != nil {
		return ex, nil
	}
	// 常见别名回退
	for _, alt := range []string{key, "openai-compatibility", "gemini"} {
		if ex, ok := b.manager.Executor(alt); ok && ex != nil {
			return ex, nil
		}
	}
	return nil, fmt.Errorf("无可用 executor: %s（bridge 未就绪或平台未注册）", key)
}

// Close 停止嵌入式 Service。
func (b *Bridge) Close(ctx context.Context) error {
	if b == nil {
		return nil
	}
	if b.cancel != nil {
		b.cancel()
	}
	if b.svc != nil {
		return b.svc.Shutdown(ctx)
	}
	return nil
}

func (b *Bridge) init(cfg *sdkconfig.Config) error {
	workDir, err := os.MkdirTemp("", "airgate-cpa-*")
	if err != nil {
		return fmt.Errorf("创建 cpa 工作目录失败: %w", err)
	}
	authDir := filepath.Join(workDir, "auths")
	if err := os.MkdirAll(authDir, 0o755); err != nil {
		return err
	}
	cfgPath := filepath.Join(workDir, "config.yaml")

	// 最小 YAML：随机端口 + auth-dir。占位 API Key 在 seedPlaceholderKeys 注入。
	// port: 0 让 OS 分配，避免与主网关冲突。
	minimalYAML := []byte(fmt.Sprintf("port: 0\nhost: 127.0.0.1\nauth-dir: %q\ndebug: false\n", authDir))
	if err := os.WriteFile(cfgPath, minimalYAML, 0o600); err != nil {
		return err
	}

	if cfg == nil {
		loaded, errLoad := sdkconfig.LoadConfig(cfgPath)
		if errLoad != nil {
			// LoadConfig 可能因缺字段失败；退回手工构造。
			cfg = &sdkconfig.Config{}
			cfg.Host = "127.0.0.1"
			cfg.Port = 0
		} else {
			cfg = loaded
		}
	}
	cfg.AuthDir = authDir
	if cfg.Host == "" {
		cfg.Host = "127.0.0.1"
	}
	seedPlaceholderKeys(cfg)

	mgr := coreauth.NewManager(&memoryAuthStore{}, nil, nil)
	mgr.SetConfig(cfg)

	svc, err := cliproxy.NewBuilder().
		WithConfig(cfg).
		WithConfigPath(cfgPath).
		WithCoreAuthManager(mgr).
		WithTokenClientProvider(cliproxy.NewFileTokenClientProvider()).
		WithAPIKeyClientProvider(cliproxy.NewAPIKeyClientProvider()).
		Build()
	if err != nil {
		return fmt.Errorf("构建 cliproxy.Service 失败: %w", err)
	}

	b.cfg = cfg
	b.manager = mgr
	b.svc = svc

	ctx, cancel := context.WithCancel(context.Background())
	b.cancel = cancel

	go func() {
		runErr := svc.Run(ctx)
		if runErr != nil && ctx.Err() == nil {
			b.mu.Lock()
			if b.initErr == nil {
				b.initErr = runErr
			}
			b.mu.Unlock()
		}
	}()

	go b.waitExecutors(mgr)
	return nil
}

func (b *Bridge) waitExecutors(mgr *coreauth.Manager) {
	required := []string{"codex", "claude", "gemini", "xai"}
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		hit := 0
		for _, p := range required {
			if _, ok := mgr.Executor(p); ok {
				hit++
			}
		}
		// 至少 2 个核心 provider 就绪即认为可用（kimi/antigravity 可能无 API-key 占位）
		if hit >= 2 {
			b.mu.Lock()
			b.ready = true
			b.mu.Unlock()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	// 超时：有任意 executor 也标 ready
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, p := range required {
		if _, ok := mgr.Executor(p); ok {
			b.ready = true
			return
		}
	}
	if b.initErr == nil {
		b.initErr = fmt.Errorf("cpa baseline executor 注册超时")
	}
}

// seedPlaceholderKeys 写入各平台占位 API Key，驱动 CPA 内置 executor 注册。
// 业务路径使用账号表 OAuth 凭证，不会用这些占位 key 打上游。
func seedPlaceholderKeys(cfg *sdkconfig.Config) {
	if cfg == nil {
		return
	}
	const dummy = "airgate-cpa-placeholder"
	if len(cfg.ClaudeKey) == 0 {
		cfg.ClaudeKey = []sdkconfig.ClaudeKey{{APIKey: dummy}}
	}
	if len(cfg.CodexKey) == 0 {
		cfg.CodexKey = []sdkconfig.CodexKey{{APIKey: dummy}}
	}
	if len(cfg.GeminiKey) == 0 {
		cfg.GeminiKey = []sdkconfig.GeminiKey{{APIKey: dummy}}
	}
	if len(cfg.XAIKey) == 0 {
		cfg.XAIKey = []sdkconfig.XAIKey{{APIKey: dummy}}
	}
	if len(cfg.OpenAICompatibility) == 0 {
		cfg.OpenAICompatibility = []sdkconfig.OpenAICompatibility{{
			Name:    "openai-compatibility",
			BaseURL: "https://api.openai.com/v1",
			APIKeyEntries: []sdkconfig.OpenAICompatibilityAPIKey{
				{APIKey: dummy},
			},
		}}
	}
}

// memoryAuthStore 进程内 Auth 持久化。
type memoryAuthStore struct {
	mu    sync.Mutex
	items map[string]*coreauth.Auth
}

func (s *memoryAuthStore) List(context.Context) ([]*coreauth.Auth, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*coreauth.Auth, 0, len(s.items))
	for _, a := range s.items {
		out = append(out, a)
	}
	return out, nil
}

func (s *memoryAuthStore) Save(_ context.Context, auth *coreauth.Auth) (string, error) {
	if auth == nil {
		return "", fmt.Errorf("auth is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items == nil {
		s.items = make(map[string]*coreauth.Auth)
	}
	s.items[auth.ID] = auth
	return auth.ID, nil
}

func (s *memoryAuthStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.items, id)
	return nil
}

// normalizeProvider 规范化平台/provider 名。
func normalizeProvider(provider string) string {
	p := strings.ToLower(strings.TrimSpace(provider))
	p = strings.ReplaceAll(p, "_", "-")
	switch p {
	case "gemini-cli", "gemini-cli/aistudio":
		return "aistudio"
	case "openai-compat", "openai-compatible":
		return "openai-compatibility"
	case "grok":
		return "xai"
	case "anthropic":
		return "claude"
	default:
		return p
	}
}

// ResolveProvider 将 airgate platform 映射为 CPA executor 键。
func ResolveProvider(platform string) string {
	return normalizeProvider(platform)
}

// SupportedProviders 与 CPA baseline 对齐的平台列表。
func SupportedProviders() []string {
	return []string{
		"codex",
		"claude",
		"antigravity",
		"kimi",
		"xai",
		"gemini",
		"aistudio",
		"vertex",
		"openai-compatibility",
	}
}
