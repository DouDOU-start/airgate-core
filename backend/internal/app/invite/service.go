package invite

import (
	"context"
	"errors"
	"math"
	"strconv"
	"strings"
)

// settings 表 invite 组：全局开关 + 全局返利比例。
const (
	settingGroup             = "invite"
	settingKeyEnabled        = "invite_enabled"
	settingKeyRebateRate     = "invite_rebate_rate_percent"
	defaultRebateRatePercent = 5.0
)

// Service 邀请返利域用例编排。
type Service struct {
	repo     Repository
	settings SettingsLister
}

// NewService 创建邀请返利服务。
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// SetSettingsLister 注入设置读取依赖（全局开关/比例）。
func (s *Service) SetSettingsLister(sl SettingsLister) {
	s.settings = sl
}

// IsEnabled 邀请返利总开关（默认关闭）。
func (s *Service) IsEnabled(ctx context.Context) bool {
	if s.settings == nil {
		return false
	}
	items, err := s.settings.List(ctx, settingGroup)
	if err != nil {
		return false
	}
	for _, item := range items {
		if item.Key == settingKeyEnabled {
			return item.Value == "true"
		}
	}
	return false
}

// globalRatePercent 读取并 clamp 全局返利比例；解析失败/缺失回退默认值。
func (s *Service) globalRatePercent(ctx context.Context) float64 {
	if s.settings == nil {
		return defaultRebateRatePercent
	}
	items, err := s.settings.List(ctx, settingGroup)
	if err != nil {
		return defaultRebateRatePercent
	}
	for _, item := range items {
		if item.Key == settingKeyRebateRate {
			if v, err := strconv.ParseFloat(strings.TrimSpace(item.Value), 64); err == nil {
				return clampRate(v)
			}
		}
	}
	return defaultRebateRatePercent
}

// resolveRatePercent 邀请人的有效返利比例：专属覆盖优先，否则全局比例。
func (s *Service) resolveRatePercent(ctx context.Context, inviter Profile) float64 {
	if inviter.RebateRateOverride != nil {
		return clampRate(*inviter.RebateRateOverride)
	}
	return s.globalRatePercent(ctx)
}

func clampRate(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return defaultRebateRatePercent
	}
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

// GetMyInfo 用户端"我的邀请"聚合信息。总开关关闭时返回 Enabled=false 且不落库。
func (s *Service) GetMyInfo(ctx context.Context, userID int) (MyInfo, error) {
	enabled := s.IsEnabled(ctx)
	if !enabled {
		return MyInfo{Enabled: false}, nil
	}
	profile, err := s.repo.EnsureProfile(ctx, userID)
	if err != nil {
		return MyInfo{}, err
	}
	return MyInfo{
		Enabled:              true,
		InviteCode:           profile.InviteCode,
		InviterID:            profile.InviterID,
		EffectiveRatePercent: s.resolveRatePercent(ctx, profile),
		InvitedCount:         profile.InvitedCount,
		RebateBalance:        profile.RebateBalance,
		RebateTotal:          profile.RebateTotal,
	}, nil
}

// ValidateCode 注册前置校验：邀请码需存在。总开关关闭或码为空时静默通过（不阻断注册）。
func (s *Service) ValidateCode(ctx context.Context, rawCode string) error {
	code := normalizeCode(rawCode)
	if code == "" || !s.IsEnabled(ctx) {
		return nil
	}
	_, err := s.repo.GetByCode(ctx, code)
	if err != nil {
		if errors.Is(err, ErrProfileNotFound) {
			return ErrCodeInvalid
		}
		return err
	}
	return nil
}

// BindInviter 仅在注册成功后由 auth 域调用：总是先为该用户生成自己的邀请画像/
// 邀请码（若尚不存在），邀请码非空时再绑定邀请关系。总开关关闭时整体跳过（不落库、
// 不报错）。用户不允许注册后自行补绑邀请人，因此不对外暴露 HTTP 接口。
func (s *Service) BindInviter(ctx context.Context, userID int, rawCode string) error {
	if !s.IsEnabled(ctx) {
		return nil
	}
	self, err := s.repo.EnsureProfile(ctx, userID)
	if err != nil {
		return err
	}

	code := normalizeCode(rawCode)
	if code == "" {
		return nil
	}
	if self.InviterID != nil {
		return ErrAlreadyBound
	}

	inviter, err := s.repo.GetByCode(ctx, code)
	if err != nil {
		if errors.Is(err, ErrProfileNotFound) {
			return ErrCodeInvalid
		}
		return err
	}
	if inviter.UserID == userID {
		return ErrSelfInvite
	}

	bound, err := s.repo.BindInviter(ctx, userID, inviter.UserID)
	if err != nil {
		return err
	}
	if !bound {
		return ErrAlreadyBound
	}
	return nil
}

// AccrueRebateForPayment 支付网关真实到账后调用：按邀请人有效比例计提返利。
// 无邀请关系、总开关关闭、金额非法均静默返回 0（不报错，不影响充值本身）。
func (s *Service) AccrueRebateForPayment(ctx context.Context, payerUserID int, amount float64, orderNo string) (float64, error) {
	if !s.IsEnabled(ctx) {
		return 0, nil
	}
	if payerUserID <= 0 || amount <= 0 || math.IsNaN(amount) || math.IsInf(amount, 0) || orderNo == "" {
		return 0, nil
	}

	payer, err := s.repo.GetProfile(ctx, payerUserID)
	if err != nil {
		if errors.Is(err, ErrProfileNotFound) {
			return 0, nil // 从未生成过邀请画像，必然没绑定邀请人
		}
		return 0, err
	}
	if payer.InviterID == nil {
		return 0, nil
	}

	inviter, err := s.repo.GetProfile(ctx, *payer.InviterID)
	if err != nil {
		if errors.Is(err, ErrProfileNotFound) {
			return 0, nil
		}
		return 0, err
	}

	rate := s.resolveRatePercent(ctx, inviter)
	rebate := roundTo(amount*rate/100, 8)
	if rebate <= 0 {
		return 0, nil
	}

	idempotencyKey := "invite-accrue:" + orderNo
	applied, err := s.repo.AccrueRebate(ctx, inviter.UserID, payerUserID, rebate, orderNo, idempotencyKey)
	if err != nil {
		return 0, err
	}
	if !applied {
		return 0, nil
	}
	return rebate, nil
}

// TransferToBalance 用户手动把返利余额转入可消费余额。
func (s *Service) TransferToBalance(ctx context.Context, userID int) (transferred, newBalance float64, err error) {
	return s.repo.TransferToBalance(ctx, userID)
}

// ListMyInvitees 用户端：我邀请的人。
func (s *Service) ListMyInvitees(ctx context.Context, inviterID int, f ListFilter) ([]InviteRelation, int64, error) {
	return s.repo.ListInvitees(ctx, inviterID, normalizeFilter(f))
}

// ListMyRebateLogs 用户端：我的返利流水。
func (s *Service) ListMyRebateLogs(ctx context.Context, userID int, f ListFilter) ([]RebateLog, int64, error) {
	return s.repo.ListRebateLogs(ctx, userID, normalizeFilter(f))
}

// ===================== 管理端 =====================

// AdminSetRateOverride 设置/清除用户专属返利比例。ratePercent==nil 表示清除（回退全局比例）。
func (s *Service) AdminSetRateOverride(ctx context.Context, userID int, ratePercent *float64) error {
	if ratePercent != nil {
		v := *ratePercent
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 100 {
			return ErrInvalidRate
		}
	}
	return s.repo.SetRateOverride(ctx, userID, ratePercent)
}

// AdminListOverrides 有专属比例覆盖的用户列表。
func (s *Service) AdminListOverrides(ctx context.Context, f ListFilter) ([]OverrideEntry, int64, error) {
	return s.repo.AdminListOverrides(ctx, normalizeFilter(f))
}

// AdminListInvitees 全量邀请关系。
func (s *Service) AdminListInvitees(ctx context.Context, f ListFilter) ([]InviteRelation, int64, error) {
	return s.repo.AdminListInvitees(ctx, normalizeFilter(f))
}

// AdminListRebateLogs 全量返利流水。
func (s *Service) AdminListRebateLogs(ctx context.Context, f ListFilter) ([]RebateLog, int64, error) {
	return s.repo.AdminListRebateLogs(ctx, normalizeFilter(f))
}

func normalizeFilter(f ListFilter) ListFilter {
	if f.Page <= 0 {
		f.Page = 1
	}
	if f.PageSize <= 0 || f.PageSize > 100 {
		f.PageSize = 20
	}
	f.Keyword = strings.TrimSpace(f.Keyword)
	return f
}

func normalizeCode(raw string) string {
	return strings.ToUpper(strings.TrimSpace(raw))
}

func roundTo(v float64, scale int) float64 {
	factor := math.Pow10(scale)
	return math.Round(v*factor) / factor
}
