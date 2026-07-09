package payment

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/DouDOU-start/airgate-core/internal/app/payment/provider"
	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/pkg/logx"
	"github.com/DouDOU-start/airgate-core/internal/pkg/timezone"
)

// 模块配置默认值（settings 表 payment 组缺省时生效），沿用原 epay 插件默认。
const (
	defaultMinAmount     = 1
	defaultMaxAmount     = 10000
	defaultDailyLimit    = 10000
	defaultExpireMinutes = 30
)

var providerKeyPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

// encPrefix 敏感配置密文的标记前缀。敏感字段一律带前缀密文落库；
// 无前缀的敏感值视为损坏配置直接报错（本分支不保留明文过渡兼容）。
// 用确定性标记而非格式启发式：64 位 hex 商户密钥这类值与 base64 密文无法靠形态区分。
const encPrefix = "enc:v1:"

// moduleConfig settings 表 payment 组的模块级配置快照。
type moduleConfig struct {
	CallbackBaseURL string
	MinAmount       float64
	MaxAmount       float64
	DailyLimit      float64
	ExpireMinutes   int
}

// Service 支付域用例编排：下单、回调入账、订单查询、服务商配置管理。
type Service struct {
	repo     Repository
	settings SettingsLister
	registry *provider.Registry
	secret   string // AES-256-GCM 密钥（与渠道 api_keys 同源），加密服务商敏感配置
}

// NewService 创建支付服务并完成首次 Provider 装载。
func NewService(repo Repository, settings SettingsLister, secret string) *Service {
	s := &Service{
		repo:     repo,
		settings: settings,
		registry: provider.NewRegistry(),
		secret:   secret,
	}
	return s
}

// loadConfig 读取模块配置（每次请求读取，settings 更新即时生效）。
func (s *Service) loadConfig(ctx context.Context) moduleConfig {
	cfg := moduleConfig{
		MinAmount:     defaultMinAmount,
		MaxAmount:     defaultMaxAmount,
		DailyLimit:    defaultDailyLimit,
		ExpireMinutes: defaultExpireMinutes,
	}
	items, err := s.settings.List(ctx, "payment")
	if err != nil {
		return cfg
	}
	for _, item := range items {
		switch item.Key {
		case "payment.callback_base_url":
			cfg.CallbackBaseURL = strings.TrimRight(strings.TrimSpace(item.Value), "/")
		case "payment.min_amount":
			if v, err := strconv.ParseFloat(item.Value, 64); err == nil && v > 0 {
				cfg.MinAmount = v
			}
		case "payment.max_amount":
			if v, err := strconv.ParseFloat(item.Value, 64); err == nil && v > 0 {
				cfg.MaxAmount = v
			}
		case "payment.daily_limit":
			if v, err := strconv.ParseFloat(item.Value, 64); err == nil && v >= 0 {
				cfg.DailyLimit = v
			}
		case "payment.order_expire_minutes":
			if v, err := strconv.Atoi(item.Value); err == nil && v > 0 {
				cfg.ExpireMinutes = v
			}
		}
	}
	return cfg
}

// ===================== Provider 装载 =====================

// ReloadProviders 从库里读全部服务商配置，解密敏感字段后重建注册表。
// admin 增删改后调用，热生效无需重启。
func (s *Service) ReloadProviders(ctx context.Context) error {
	logger := logx.LoggerFromContext(ctx)
	configs, err := s.repo.ListProviderConfigs(ctx)
	if err != nil {
		return err
	}
	providers := make([]provider.Provider, 0, len(configs))
	for _, c := range configs {
		plain, err := s.decryptSensitive(c.Kind, c.Config)
		if err != nil {
			// 敏感配置未按约定加密（如手工改库）：跳过该实例，管理端重新保存即可恢复
			logger.Warn("payment_provider_config_invalid", "provider", c.ProviderKey, "kind", c.Kind, logx.LogFieldError, err)
			continue
		}
		p, err := provider.Build(c.Kind, c.ProviderKey, c.Enabled, plain)
		if err != nil {
			// 单个实例构建失败不阻塞其余实例（如未知 kind、配置损坏）
			logger.Warn("payment_provider_build_failed", "provider", c.ProviderKey, "kind", c.Kind, logx.LogFieldError, err)
			continue
		}
		providers = append(providers, p)
	}
	s.registry.Replace(providers)
	logger.Info("payment_providers_reloaded", "count", len(providers))
	return nil
}

// sensitiveFields 返回 kind 声明中的敏感字段集合（password / textarea 类型：
// 密钥、私钥 PEM 等），这些字段密文落库、响应中掩码。
func sensitiveFields(kind string) map[string]bool {
	meta, ok := provider.GetKindMeta(kind)
	if !ok {
		return nil
	}
	out := make(map[string]bool)
	for _, f := range meta.FieldDescriptors {
		if f.Type == "password" || f.Type == "textarea" {
			out[f.Key] = true
		}
	}
	return out
}

// decryptSensitive 还原敏感字段明文：只接受带 encPrefix 标记的密文，
// 非空却无前缀的敏感值属于违规落库（本分支不保留明文过渡兼容），直接报错。
func (s *Service) decryptSensitive(kind string, config map[string]string) (map[string]string, error) {
	sensitive := sensitiveFields(kind)
	out := make(map[string]string, len(config))
	for k, v := range config {
		if !sensitive[k] || v == "" {
			out[k] = v
			continue
		}
		if !strings.HasPrefix(v, encPrefix) {
			return nil, fmt.Errorf("敏感配置字段 %s 未加密落库（缺少 %s 标记），请在管理端重新保存", k, encPrefix)
		}
		plain, err := auth.DecryptAPIKey(strings.TrimPrefix(v, encPrefix), s.secret)
		if err != nil {
			// 带标记却解不开（如换过 APIKeySecret）：保留原值，Provider 会因
			// 配置无效呈 Enabled=false，管理端重新保存密钥即可恢复
			slog.Warn("payment_config_decrypt_failed", "kind", kind, "field", k, "error", err)
			out[k] = v
			continue
		}
		out[k] = plain
	}
	return out, nil
}

// encryptValue 加密敏感值并打上 encPrefix 标记。
func (s *Service) encryptValue(plain string) (string, error) {
	enc, err := auth.EncryptAPIKey(plain, s.secret)
	if err != nil {
		return "", err
	}
	return encPrefix + enc, nil
}

// ===================== 用户端 =====================

// MethodsResult 用户可用支付方式。
type MethodsResult struct {
	Methods    []provider.MethodInfo
	Configured bool
}

// AvailableMethods 当前真正可用的支付方式（有启用的 Provider 承接才算可用）。
func (s *Service) AvailableMethods(ctx context.Context) MethodsResult {
	cfg := s.loadConfig(ctx)
	methods := s.registry.AvailableMethods()
	return MethodsResult{
		Methods:    methods,
		Configured: cfg.CallbackBaseURL != "" && len(methods) > 0,
	}
}

// CreateOrder 用户下单：校验金额/日限额 → 选 Provider → 渠道下单 → 落库。
func (s *Service) CreateOrder(ctx context.Context, in CreateOrderInput) (Order, error) {
	logger := logx.LoggerFromContext(ctx)
	cfg := s.loadConfig(ctx)
	if cfg.CallbackBaseURL == "" {
		return Order{}, ErrNotConfigured
	}
	if in.Amount < cfg.MinAmount || in.Amount > cfg.MaxAmount {
		return Order{}, fmt.Errorf("%w（%.2f - %.2f 元）", ErrInvalidAmount, cfg.MinAmount, cfg.MaxAmount)
	}
	if cfg.DailyLimit > 0 {
		todayStart := timezone.StartOfDay(time.Now())
		paid, err := s.repo.PaidAmountSince(ctx, in.UserID, todayStart)
		if err != nil {
			return Order{}, err
		}
		if paid+in.Amount > cfg.DailyLimit {
			return Order{}, fmt.Errorf("%w（单日上限 %.2f 元）", ErrDailyLimit, cfg.DailyLimit)
		}
	}

	prov, err := s.registry.Pick(in.Method)
	if err != nil {
		return Order{}, ErrNoProvider
	}

	outTradeNo := generateOutTradeNo()
	subject := in.Subject
	if subject == "" {
		subject = "账户充值"
	}
	expiresAt := time.Now().Add(time.Duration(cfg.ExpireMinutes) * time.Minute)

	res, err := prov.CreateOrder(ctx, provider.CreateOrderInput{
		OutTradeNo:    outTradeNo,
		Amount:        in.Amount,
		Subject:       subject,
		Method:        in.Method,
		NotifyURL:     cfg.CallbackBaseURL + "/api/v1/payment/notify/" + prov.ID(),
		ReturnURL:     cfg.CallbackBaseURL + "/recharge",
		ClientIP:      in.ClientIP,
		ExpireSeconds: cfg.ExpireMinutes * 60,
	})
	if err != nil {
		logger.Warn("payment_order_create_failed", "provider", prov.ID(), "method", in.Method, logx.LogFieldError, err)
		return Order{}, fmt.Errorf("渠道下单失败: %w", err)
	}

	order, err := s.repo.CreateOrder(ctx, Order{
		OutTradeNo:    outTradeNo,
		UserID:        in.UserID,
		Method:        in.Method,
		ProviderID:    prov.ID(),
		Amount:        in.Amount,
		Status:        StatusPending,
		Subject:       subject,
		ClientIP:      in.ClientIP,
		PaymentURL:    res.PaymentURL,
		QRCodeContent: res.QRCodeContent,
		ExpiresAt:     expiresAt,
	})
	if err != nil {
		logger.Error("payment_persist_failed", "op", "create_order", logx.LogFieldError, err)
		return Order{}, err
	}
	logger.Info("payment_order_created",
		"out_trade_no", outTradeNo, "provider", prov.ID(), "method", in.Method, "amount", in.Amount)
	return order, nil
}

// GetUserOrder 用户查单（校验归属，续付/轮询用）。
func (s *Service) GetUserOrder(ctx context.Context, userID int, outTradeNo string) (Order, error) {
	order, err := s.repo.GetOrder(ctx, outTradeNo)
	if err != nil || order.UserID != userID {
		return Order{}, ErrOrderNotFound
	}
	return order, nil
}

// ListUserOrders 用户充值记录。
func (s *Service) ListUserOrders(ctx context.Context, userID, limit int) ([]Order, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	return s.repo.ListUserOrders(ctx, userID, limit)
}

// ===================== 回调 =====================

// HandleCallback 处理支付平台异步通知：验签 → 入账（单事务、幂等）→ 返回平台要求的应答。
func (s *Service) HandleCallback(ctx context.Context, providerID string, req provider.CallbackRequest) (*provider.CallbackResult, error) {
	logger := logx.LoggerFromContext(ctx)
	prov := s.registry.Find(providerID)
	if prov == nil {
		return nil, ErrProviderNotFound
	}
	res, err := prov.VerifyCallback(ctx, req)
	if err != nil {
		// 验签失败只记订单号维度信息，不回显签名/载荷
		logger.Warn("payment_callback_verify_failed", "provider", providerID, logx.LogFieldError, err)
		return nil, err
	}
	if res.Status != StatusPaid {
		logger.Info("payment_callback_ignored", "provider", providerID, "out_trade_no", res.OutTradeNo, "status", res.Status)
		return res, nil
	}

	alreadyPaid, err := s.repo.CreditPaidOrder(ctx, CreditInput{
		OutTradeNo:    res.OutTradeNo,
		Amount:        res.Amount,
		NotifyPayload: flattenRaw(res.Raw),
		Remark:        "在线充值（" + provider.MethodInfoFor(methodOfCallback(ctx, s, res.OutTradeNo)).Label + "）",
	})
	if err != nil {
		logger.Error("payment_credit_failed", "provider", providerID, "out_trade_no", res.OutTradeNo, logx.LogFieldError, err)
		return nil, err
	}
	if alreadyPaid {
		logger.Info("payment_callback_idempotent", "out_trade_no", res.OutTradeNo)
	} else {
		logger.Info("payment_order_paid", "out_trade_no", res.OutTradeNo, "provider", providerID, "amount", res.Amount)
	}
	return res, nil
}

// methodOfCallback 取订单的支付方式用于流水备注；查不到时返回空（备注退化为「在线充值」）。
func methodOfCallback(ctx context.Context, s *Service, outTradeNo string) string {
	order, err := s.repo.GetOrder(ctx, outTradeNo)
	if err != nil {
		return ""
	}
	return order.Method
}

// flattenRaw 回调原始字段拍平成 k=v 行文本留痕（避免引入 json 依赖歧义，仅审计用）。
func flattenRaw(raw map[string]string) string {
	if len(raw) == 0 {
		return ""
	}
	parts := make([]string, 0, len(raw))
	for k, v := range raw {
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, "\n")
}

// ===================== 管理端 =====================

// AdminListOrders 管理端订单列表 + 统计。
func (s *Service) AdminListOrders(ctx context.Context, f AdminOrderFilter) ([]Order, int64, OrderStats, error) {
	if f.Page <= 0 {
		f.Page = 1
	}
	if f.PageSize <= 0 || f.PageSize > 100 {
		f.PageSize = 20
	}
	list, total, err := s.repo.AdminListOrders(ctx, f)
	if err != nil {
		return nil, 0, OrderStats{}, err
	}
	stats, err := s.repo.OrderStats(ctx, timezone.StartOfDay(time.Now()))
	if err != nil {
		return nil, 0, OrderStats{}, err
	}
	return list, total, stats, nil
}

// ProviderView 管理端服务商实例视图（敏感字段已掩码为空串）。
type ProviderView struct {
	ProviderKey      string
	Kind             string
	Name             string
	Enabled          bool
	Config           map[string]string
	SupportedMethods []string
	IsRunning        bool
	SensitiveKeys    []string
}

// AdminListProviders 服务商实例列表 + 可配置的协议类型元信息。
func (s *Service) AdminListProviders(ctx context.Context) ([]ProviderView, []provider.KindMeta, error) {
	configs, err := s.repo.ListProviderConfigs(ctx)
	if err != nil {
		return nil, nil, err
	}
	views := make([]ProviderView, 0, len(configs))
	for _, c := range configs {
		sensitive := sensitiveFields(c.Kind)
		masked := make(map[string]string, len(c.Config))
		sensitiveKeys := make([]string, 0, len(sensitive))
		for k, v := range c.Config {
			if sensitive[k] {
				// 敏感字段明文永不出现在响应；已配置用占位标识，前端「留空保持不变」
				if v != "" {
					masked[k] = ""
					sensitiveKeys = append(sensitiveKeys, k)
				} else {
					masked[k] = ""
				}
				continue
			}
			masked[k] = v
		}
		view := ProviderView{
			ProviderKey:   c.ProviderKey,
			Kind:          c.Kind,
			Enabled:       c.Enabled,
			Config:        masked,
			SensitiveKeys: sensitiveKeys,
		}
		if meta, ok := provider.GetKindMeta(c.Kind); ok {
			view.Name = meta.Name
			view.SupportedMethods = meta.SupportedMethods
		}
		if p := s.registry.Find(c.ProviderKey); p != nil {
			view.IsRunning = p.Enabled()
		}
		views = append(views, view)
	}
	return views, provider.AllKindMetas(), nil
}

// UpsertProviderInput 管理端新增/编辑服务商实例。
type UpsertProviderInput struct {
	ProviderKey string
	OriginalKey string // 非空且 != ProviderKey 时执行重命名
	Kind        string
	Enabled     bool
	Config      map[string]string
}

// AdminUpsertProvider 新增/编辑服务商实例并热加载。
// 敏感字段传空串表示保持库中现值不变。返回最终实例 ID。
func (s *Service) AdminUpsertProvider(ctx context.Context, in UpsertProviderInput) (string, error) {
	logger := logx.LoggerFromContext(ctx)
	if _, ok := provider.GetKindMeta(in.Kind); !ok {
		return "", fmt.Errorf("未知的服务商类型: %s", in.Kind)
	}

	key := strings.TrimSpace(in.ProviderKey)
	if key == "" {
		generated, err := s.repo.NextProviderKeyForKind(ctx, in.Kind)
		if err != nil {
			return "", err
		}
		key = generated
	}
	if !providerKeyPattern.MatchString(key) {
		return "", ErrInvalidProviderKey
	}

	// 重命名：事务内同步订单引用，再按新 key 继续 upsert
	if in.OriginalKey != "" && in.OriginalKey != key {
		if err := s.repo.RenameProviderConfig(ctx, in.OriginalKey, key); err != nil {
			return "", err
		}
	}

	// 组装落库配置：敏感字段传空 = 保持库中现有密文；传新值 = 加密替换。
	existingConfig := map[string]string{}
	if existing, err := s.repo.GetProviderConfig(ctx, key); err == nil {
		existingConfig = existing.Config
	}
	sensitive := sensitiveFields(in.Kind)
	encrypted := make(map[string]string, len(in.Config))
	for k, v := range in.Config {
		if !sensitive[k] {
			encrypted[k] = v
			continue
		}
		if strings.TrimSpace(v) == "" {
			encrypted[k] = existingConfig[k] // 沿用现有密文（可能为空）
			continue
		}
		enc, err := s.encryptValue(v)
		if err != nil {
			return "", fmt.Errorf("加密服务商配置失败: %w", err)
		}
		encrypted[k] = enc
	}

	if err := s.repo.UpsertProviderConfig(ctx, ProviderConfig{
		ProviderKey: key,
		Kind:        in.Kind,
		Enabled:     in.Enabled,
		Config:      encrypted,
	}); err != nil {
		logger.Error("payment_persist_failed", "op", "upsert_provider", "provider", key, logx.LogFieldError, err)
		return "", err
	}
	logger.Info("payment_provider_upserted", "provider", key, "kind", in.Kind, "enabled", in.Enabled)

	if err := s.ReloadProviders(ctx); err != nil {
		logger.Warn("payment_provider_reload_failed", logx.LogFieldError, err)
	}
	return key, nil
}

// AdminDeleteProvider 删除服务商实例并热加载。
func (s *Service) AdminDeleteProvider(ctx context.Context, providerKey string) error {
	logger := logx.LoggerFromContext(ctx)
	if err := s.repo.DeleteProviderConfig(ctx, providerKey); err != nil {
		logger.Error("payment_persist_failed", "op", "delete_provider", "provider", providerKey, logx.LogFieldError, err)
		return err
	}
	logger.Info("payment_provider_deleted", "provider", providerKey)
	if err := s.ReloadProviders(ctx); err != nil {
		logger.Warn("payment_provider_reload_failed", logx.LogFieldError, err)
	}
	return nil
}

// ===================== 后台任务 =====================

// StartExpireLoop 每 5 分钟清理一次过期的 pending 订单（server 启动时以 goroutine 拉起）。
func (s *Service) StartExpireLoop(ctx context.Context) {
	const interval = 5 * time.Minute
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := s.repo.ExpirePendingOrders(ctx, time.Now())
			if err != nil {
				logx.LoggerFromContext(ctx).Warn("payment_orders_expire_failed", logx.LogFieldError, err)
				continue
			}
			if n > 0 {
				logx.LoggerFromContext(ctx).Info("payment_orders_expired", "count", n)
			}
		}
	}
}

// generateOutTradeNo 商户订单号：AG + 秒级时间戳 + 8 位随机 hex（沿用原插件格式）。
func generateOutTradeNo() string {
	buf := make([]byte, 4)
	_, _ = rand.Read(buf)
	return "AG" + time.Now().Format("20060102150405") + hex.EncodeToString(buf)
}
