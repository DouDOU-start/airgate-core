package redemption

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"sync"
	"time"

	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"
)

const (
	maxBatchCount = 500
	maxRemarkLen  = 200

	// 兑换失败限流：单用户滑动窗口内最多 failLimit 次失败（防枚举兑换码）。
	failLimit  = 10
	failWindow = 10 * time.Minute
)

// Service 兑换码服务。
type Service struct {
	repo Repository

	// 兑换失败限流器（进程内滑动窗口）。本分支为单实例部署，无需分布式限流；
	// 码本身为 128-bit 随机 hex，限流仅作纵深防御。
	failMu  sync.Mutex
	fails   map[int][]time.Time
	nowFunc func() time.Time // 测试注入
}

// NewService 创建兑换码服务。
func NewService(repo Repository) *Service {
	return &Service{
		repo:    repo,
		fails:   make(map[int][]time.Time),
		nowFunc: time.Now,
	}
}

// Generate 批量生成兑换码（32 位随机 hex，同批次共享备注与过期时间）。
func (s *Service) Generate(ctx context.Context, input GenerateInput) ([]Code, error) {
	logger := sdk.LoggerFromContext(ctx)
	if input.Count < 1 || input.Count > maxBatchCount {
		return nil, ErrInvalidGenerateInput
	}
	if input.Value <= 0 {
		return nil, ErrInvalidGenerateInput
	}
	if len(input.Remark) > maxRemarkLen {
		return nil, ErrInvalidGenerateInput
	}
	if input.ExpiresAt != nil && !input.ExpiresAt.After(s.nowFunc()) {
		return nil, ErrInvalidGenerateInput
	}

	codes := make([]Code, 0, input.Count)
	for i := 0; i < input.Count; i++ {
		raw, err := randomCode()
		if err != nil {
			return nil, err
		}
		codes = append(codes, Code{
			Code:      raw,
			Value:     input.Value,
			Status:    StatusUnused,
			Remark:    input.Remark,
			ExpiresAt: input.ExpiresAt,
		})
	}
	created, err := s.repo.CreateBatch(ctx, codes)
	if err != nil {
		logger.Error("redemption_generate_failed", "count", input.Count, sdk.LogFieldError, err)
		return nil, err
	}
	logger.Info("redemption_codes_generated", "count", len(created), "value", input.Value)
	return created, nil
}

// List 管理端分页列表。
func (s *Service) List(ctx context.Context, f ListFilter) ([]Code, int64, error) {
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize < 1 || f.PageSize > 100 {
		f.PageSize = 20
	}
	return s.repo.List(ctx, f, s.nowFunc())
}

// Stats 管理端统计。
func (s *Service) Stats(ctx context.Context) (Stats, error) {
	return s.repo.Stats(ctx, s.nowFunc())
}

// SetDisabled 停用/恢复兑换码。仅 unused ↔ disabled 可流转，已使用的码不可操作。
func (s *Service) SetDisabled(ctx context.Context, id int, disabled bool) error {
	from, to := StatusUnused, StatusDisabled
	if !disabled {
		from, to = StatusDisabled, StatusUnused
	}
	return s.repo.UpdateStatus(ctx, id, from, to)
}

// Delete 删除兑换码。已使用的码留作兑换凭据，不可删除。
func (s *Service) Delete(ctx context.Context, id int) error {
	item, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return err
	}
	if item.Status == StatusUsed {
		return ErrCodeStateConflict
	}
	return s.repo.Delete(ctx, id)
}

// Redeem 用户兑换：失败限流 → 单事务入账（并发安全由 store 条件更新保证）。
func (s *Service) Redeem(ctx context.Context, userID int, rawCode string) (RedeemResult, error) {
	logger := sdk.LoggerFromContext(ctx)
	code := strings.TrimSpace(rawCode)
	if code == "" || len(code) > 64 {
		return RedeemResult{}, ErrCodeNotFound
	}
	if !s.allowAttempt(userID) {
		logger.Warn("redemption_rate_limited", sdk.LogFieldUserID, userID)
		return RedeemResult{}, ErrRedeemRateLimited
	}

	result, err := s.repo.Redeem(ctx, code, userID, s.nowFunc())
	if err != nil {
		// 仅"猜码类"失败计入限流窗口；系统错误不惩罚用户
		switch err {
		case ErrCodeNotFound, ErrCodeUsed, ErrCodeDisabled, ErrCodeExpired:
			s.recordFail(userID)
			logger.Info("redemption_redeem_rejected", sdk.LogFieldUserID, userID, sdk.LogFieldReason, err.Error())
		default:
			logger.Error("redemption_redeem_failed", sdk.LogFieldUserID, userID, sdk.LogFieldError, err)
		}
		return RedeemResult{}, err
	}
	logger.Info("redemption_redeemed", sdk.LogFieldUserID, userID, "value", result.Value)
	return result, nil
}

// allowAttempt 检查用户是否仍在失败限流窗口内。
func (s *Service) allowAttempt(userID int) bool {
	s.failMu.Lock()
	defer s.failMu.Unlock()
	cutoff := s.nowFunc().Add(-failWindow)
	recent := s.fails[userID][:0]
	for _, t := range s.fails[userID] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	if len(recent) == 0 {
		delete(s.fails, userID)
	} else {
		s.fails[userID] = recent
	}
	return len(recent) < failLimit
}

// recordFail 记录一次兑换失败。
func (s *Service) recordFail(userID int) {
	s.failMu.Lock()
	defer s.failMu.Unlock()
	s.fails[userID] = append(s.fails[userID], s.nowFunc())
}

// randomCode 生成 32 位随机 hex 兑换码（128-bit 熵，不可枚举）。
func randomCode() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
