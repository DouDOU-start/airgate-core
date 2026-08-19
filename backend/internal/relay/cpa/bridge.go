// Package cpa 将 CLIProxyAPI（固定模块版本 v7.x）的订阅账号转发能力桥接到 airgate。
//
// 仅依赖 CPA 公开 SDK：
//   - sdk/config
//   - sdk/cliproxy (+ auth Manager)
//   - sdk/translator/builtin（注册翻译器）
//
// 不 import CPA internal/*。有 API Key 配置入口的平台通过占位 Key 注册 executor；
// Antigravity/Kimi 则通过短生命周期占位 auth 文件触发注册，完成后立即删除。
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

	"github.com/DouDOU-start/airgate-core/internal/relay/cursor"

	// 注册 CPA 全部内置翻译器。
	_ "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator/builtin"
)

// Bridge 持有已注册 baseline executor 的 CPA Manager。
type Bridge struct {
	cfg     *sdkconfig.Config
	manager *coreauth.Manager
	svc     *cliproxy.Service
	// executorSeedPaths 是为触发无 API Key 平台 executor 注册而创建的临时凭证文件。
	executorSeedPaths   []string
	executorSeedCleanup sync.Once

	mu             sync.RWMutex
	ready          bool
	initErr        error
	cancel         context.CancelFunc
	refreshLocks   sync.Map
	refreshedAuths sync.Map
	// mappedAuths 缓存 Codex 账号快照到 CPA Auth 的纯函数映射结果。同一账号的
	// 凭证、类型或代理发生变化时会在读取时自动失效，避免每次请求重复构造 map。
	mappedAuths sync.Map
}

// NewBridge 创建桥接层并异步启动嵌入式 cliproxy.Service 以注册 executor。
// 调用 Forward 前建议 WaitReady。cfg 可为 nil。
func NewBridge(cfg *sdkconfig.Config) *Bridge {
	b := &Bridge{}
	if err := b.init(cfg); err != nil {
		b.cleanupExecutorSeedAuthFiles()
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
	if key == "" {
		return nil, fmt.Errorf("provider 为空")
	}
	if ex, ok := b.manager.Executor(key); ok && ex != nil {
		return ex, nil
	}
	return nil, fmt.Errorf("无可用 executor: %s（bridge 未就绪或平台未注册）", key)
}

// Close 停止嵌入式 Service。
func (b *Bridge) Close(ctx context.Context) error {
	if b == nil {
		return nil
	}
	b.cleanupExecutorSeedAuthFiles()
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
	seedPaths, err := seedOAuthExecutorAuthFiles(authDir)
	if err != nil {
		return err
	}
	b.executorSeedPaths = seedPaths
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

	// cursor executor 是 airgate 原生实现（非 CPA baseline），直接注册即可用，
	// 不依赖占位凭证或 CPA service 的注册流程。
	mgr.RegisterExecutor(cursor.NewExecutor())

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
	required := []string{"codex", "claude", "antigravity", "kimi", "gemini", "xai"}
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		hit := 0
		for _, p := range required {
			if _, ok := mgr.Executor(p); ok {
				hit++
			}
		}
		if hit == len(required) {
			b.cleanupExecutorSeedAuthFiles()
			b.mu.Lock()
			b.ready = true
			b.mu.Unlock()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	// 超时也清理临时凭证文件；已注册的 executor 不会随凭证删除而注销。
	b.cleanupExecutorSeedAuthFiles()
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.initErr == nil {
		missing := make([]string, 0, len(required))
		for _, provider := range required {
			if executor, ok := mgr.Executor(provider); !ok || executor == nil {
				missing = append(missing, provider)
			}
		}
		b.initErr = fmt.Errorf("cpa baseline executor 注册超时，缺少: %s", strings.Join(missing, ", "))
	}
}

// seedOAuthExecutorAuthFiles 为没有 API Key 配置入口的平台创建短生命周期占位凭证。
// CPA 非 Home 模式只会按配置 API Key 和 auth 文件注册 executor；没有这些文件时，
// Antigravity/Kimi executor 不会出现。占位凭证带远期过期时间，不会触发真实换票。
func seedOAuthExecutorAuthFiles(authDir string) ([]string, error) {
	seeds := map[string]string{
		"airgate-antigravity-executor-seed.json": `{"type":"antigravity","access_token":"airgate-cpa-placeholder","refresh_token":"airgate-cpa-placeholder","project_id":"airgate-cpa-placeholder","expired":"2099-01-01T00:00:00Z","disabled":false}`,
		"airgate-kimi-executor-seed.json":        `{"type":"kimi","access_token":"airgate-cpa-placeholder","refresh_token":"airgate-cpa-placeholder","expired":"2099-01-01T00:00:00Z","disabled":false}`,
	}
	paths := make([]string, 0, len(seeds))
	for name, body := range seeds {
		path := filepath.Join(authDir, name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			for _, created := range paths {
				_ = os.Remove(created)
			}
			return nil, fmt.Errorf("写入 OAuth executor 占位凭证失败: %w", err)
		}
		paths = append(paths, path)
	}
	return paths, nil
}

func (b *Bridge) cleanupExecutorSeedAuthFiles() {
	if b == nil {
		return
	}
	b.executorSeedCleanup.Do(func() {
		for _, path := range b.executorSeedPaths {
			_ = os.Remove(path)
		}
	})
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
		"cursor",
	}
}
