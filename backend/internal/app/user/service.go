package user

import (
	"context"
	"errors"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/DouDOU-start/airgate-core/internal/pkg/logx"
	"github.com/DouDOU-start/airgate-core/internal/pkg/pagination"
	"github.com/DouDOU-start/airgate-core/internal/pkg/timezone"
)

// BalanceAlertFunc 余额预警回调（异步调用，不阻塞主流程）。
type BalanceAlertFunc func(email string, balance float64, threshold float64)

// Service 用户应用服务。
type Service struct {
	repo           Repository
	onBalanceAlert BalanceAlertFunc
	concurrency    ConcurrencyReader
	rpm            RPMReader
}

// NewService 创建用户服务。
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// SetBalanceAlertCallback 设置余额预警回调。
func (s *Service) SetBalanceAlertCallback(fn BalanceAlertFunc) {
	s.onBalanceAlert = fn
}

// SetRuntimeStatsReaders 注入运行时指标读取器（server 装配阶段调用；nil 安全，
// 未注入时列表的并发/RPM 恒为 0）。
func (s *Service) SetRuntimeStatsReaders(concurrency ConcurrencyReader, rpm RPMReader) {
	s.concurrency = concurrency
	s.rpm = rpm
}

// Get 获取用户。
func (s *Service) Get(ctx context.Context, id int) (User, error) {
	return s.repo.FindByID(ctx, id, true)
}

// UpdateProfile 更新当前用户资料。
func (s *Service) UpdateProfile(ctx context.Context, id int, username string) (User, error) {
	logger := logx.LoggerFromContext(ctx)
	updated, err := s.repo.Update(ctx, id, Mutation{Username: &username})
	if err != nil {
		logger.Error("user_profile_update_failed",
			logx.LogFieldUserID, id,
			logx.LogFieldError, err,
		)
		return User{}, err
	}
	logger.Info("user_profile_updated", logx.LogFieldUserID, id)
	return updated, nil
}

// ChangePassword 修改当前用户密码。
func (s *Service) ChangePassword(ctx context.Context, id int, oldPassword, newPassword string) error {
	logger := logx.LoggerFromContext(ctx)
	item, err := s.repo.FindByID(ctx, id, false)
	if err != nil {
		logger.Error("user_lookup_failed",
			logx.LogFieldUserID, id,
			logx.LogFieldReason, "change_password",
			logx.LogFieldError, err,
		)
		return err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(item.PasswordHash), []byte(oldPassword)); err != nil {
		logger.Warn("user_password_change_rejected",
			logx.LogFieldUserID, id,
			logx.LogFieldReason, "old_password_mismatch",
		)
		return ErrOldPasswordMismatch
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		logger.Error("user_password_change_failed",
			logx.LogFieldUserID, id,
			logx.LogFieldReason, "password_hash",
			logx.LogFieldError, err,
		)
		return err
	}
	_, err = s.repo.Update(ctx, id, Mutation{PasswordHash: stringPtr(string(hash))})
	if err != nil {
		logger.Error("user_password_change_failed",
			logx.LogFieldUserID, id,
			logx.LogFieldReason, "persist",
			logx.LogFieldError, err,
		)
		return err
	}
	logger.Info("user_password_changed", logx.LogFieldUserID, id)
	return nil
}

// List 查询用户列表。
func (s *Service) List(ctx context.Context, filter ListFilter) (ListResult, error) {
	logger := logx.LoggerFromContext(ctx)
	page, pageSize := pagination.Normalize(filter.Page, filter.PageSize)
	filter.Page = page
	filter.PageSize = pageSize

	list, total, err := s.repo.List(ctx, filter)
	if err != nil {
		logger.Error("user_lookup_failed",
			logx.LogFieldReason, "list",
			logx.LogFieldError, err,
		)
		return ListResult{}, err
	}
	s.attachRuntimeStats(ctx, list)
	return ListResult{List: list, Total: total, Page: page, PageSize: pageSize}, nil
}

// attachRuntimeStats 为列表页用户批量填充运行时观测指标（在途并发 / 当前分钟 RPM）。
// 读取器未注入或 Redis 不可用时保持 0 值，不影响列表主流程。
func (s *Service) attachRuntimeStats(ctx context.Context, list []User) {
	if len(list) == 0 {
		return
	}
	ids := make([]int, len(list))
	for i, u := range list {
		ids[i] = u.ID
	}
	var counts, rpms map[int]int
	if s.concurrency != nil {
		counts = s.concurrency.GetUserCurrentCounts(ctx, ids)
	}
	if s.rpm != nil {
		rpms = s.rpm.GetUserRPMs(ctx, ids)
	}
	for i := range list {
		list[i].CurrentConcurrency = counts[list[i].ID]
		list[i].CurrentRPM = rpms[list[i].ID]
	}
}

// Create 创建用户。
func (s *Service) Create(ctx context.Context, input CreateInput) (User, error) {
	logger := logx.LoggerFromContext(ctx)
	exists, err := s.repo.EmailExists(ctx, input.Email)
	if err != nil {
		logger.Error("user_lookup_failed",
			logx.LogFieldReason, "email_check",
			logx.LogFieldError, err,
		)
		return User{}, err
	}
	if exists {
		logger.Warn("user_create_rejected", logx.LogFieldReason, "email_already_exists")
		return User{}, ErrEmailAlreadyExists
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		logger.Error("user_create_failed",
			logx.LogFieldReason, "password_hash",
			logx.LogFieldError, err,
		)
		return User{}, err
	}

	created, err := s.repo.Create(ctx, Mutation{
		Email:          &input.Email,
		PasswordHash:   stringPtr(string(hash)),
		Username:       &input.Username,
		Role:           &input.Role,
		MaxConcurrency: intPtrIfPositive(input.MaxConcurrency),
		GroupRates:     cloneGroupRates(input.GroupRates),
		HasGroupRates:  input.GroupRates != nil,
	})
	if err != nil {
		logger.Error("user_create_failed",
			logx.LogFieldReason, "persist",
			logx.LogFieldError, err,
		)
		return User{}, err
	}
	logger.Info("user_created", logx.LogFieldUserID, created.ID)
	return created, nil
}

// Update 更新用户。
func (s *Service) Update(ctx context.Context, id int, input UpdateInput) (User, error) {
	logger := logx.LoggerFromContext(ctx)
	if input.HasGroupRates {
		for _, v := range input.GroupRates {
			if v < 0 {
				logger.Warn("user_update_rejected",
					logx.LogFieldUserID, id,
					logx.LogFieldReason, "invalid_rate_multiplier",
				)
				return User{}, ErrInvalidRateMultiplier
			}
		}
	}
	mutation := Mutation{
		Username:           input.Username,
		Role:               input.Role,
		MaxConcurrency:     input.MaxConcurrency,
		GroupRates:         cloneGroupRates(input.GroupRates),
		HasGroupRates:      input.HasGroupRates,
		AllowedGroupIDs:    append([]int64(nil), input.AllowedGroupIDs...),
		HasAllowedGroupIDs: input.HasAllowedGroupIDs,
		Status:             input.Status,
	}
	if input.Password != nil {
		hash, err := bcrypt.GenerateFromPassword([]byte(*input.Password), bcrypt.DefaultCost)
		if err != nil {
			logger.Error("user_password_change_failed",
				logx.LogFieldUserID, id,
				logx.LogFieldReason, "password_hash",
				logx.LogFieldError, err,
			)
			return User{}, err
		}
		mutation.PasswordHash = stringPtr(string(hash))
	}
	updated, err := s.repo.Update(ctx, id, mutation)
	if err != nil {
		logger.Error("user_update_failed",
			logx.LogFieldUserID, id,
			logx.LogFieldError, err,
		)
		return User{}, err
	}
	if input.Password != nil {
		logger.Info("user_password_changed", logx.LogFieldUserID, id)
	}
	if input.Status != nil && *input.Status == "disabled" {
		logger.Info("user_disabled", logx.LogFieldUserID, id)
	}
	logger.Info("user_profile_updated", logx.LogFieldUserID, id)
	return updated, nil
}

// AdjustBalance 调整用户余额。before/after 由 store 在事务内以行锁重读现算，
// 与计费侧的原子扣减并发安全（不再无锁读余额后覆盖写回）。
func (s *Service) AdjustBalance(ctx context.Context, id int, change BalanceChange) (User, error) {
	logger := logx.LoggerFromContext(ctx)
	switch change.Action {
	case "set", "add", "subtract":
	default:
		logger.Warn("user_balance_change_rejected",
			logx.LogFieldUserID, id,
			logx.LogFieldReason, "invalid_action",
			"action", change.Action,
		)
		return User{}, ErrInvalidBalanceAction
	}

	updated, err := s.repo.UpdateBalance(ctx, id, change)
	if err != nil {
		// 幂等命中：同一键的变更已入账过，返回当前状态、不重复变更
		if errors.Is(err, ErrDuplicateBalanceChange) {
			logger.Info("user_balance_change_idempotent_hit",
				logx.LogFieldUserID, id,
				"idempotency_key", change.IdempotencyKey,
			)
			return s.repo.FindByID(ctx, id, true)
		}
		if errors.Is(err, ErrInsufficientBalance) {
			logger.Warn("user_balance_change_rejected",
				logx.LogFieldUserID, id,
				logx.LogFieldReason, "insufficient_balance",
				"action", change.Action,
			)
			return User{}, err
		}
		logger.Error("user_balance_change_failed",
			logx.LogFieldUserID, id,
			"action", change.Action,
			logx.LogFieldError, err,
		)
		return User{}, err
	}

	logger.Info("user_balance_changed",
		logx.LogFieldUserID, id,
		"action", change.Action,
		"amount", change.Amount,
		"after", updated.Balance,
		logx.LogFieldReason, change.Remark,
	)

	// 余额预警检查
	s.checkBalanceAlert(ctx, updated)

	return updated, nil
}

// UpdateBalanceAlert 更新余额预警阈值（0 表示关闭）。
func (s *Service) UpdateBalanceAlert(ctx context.Context, userID int, threshold float64) error {
	return s.repo.UpdateBalanceAlert(ctx, userID, threshold)
}

// checkBalanceAlert 检查余额是否低于预警阈值，触发通知。
func (s *Service) checkBalanceAlert(ctx context.Context, user User) {
	threshold := user.BalanceAlertThreshold
	if threshold <= 0 || s.onBalanceAlert == nil {
		return
	}
	// 余额从高于阈值降到低于阈值，且尚未通知过
	if user.Balance < threshold && !user.BalanceAlertNotified {
		_ = s.repo.SetBalanceAlertNotified(ctx, user.ID, true)
		go s.onBalanceAlert(user.Email, user.Balance, threshold)
	}
	// 余额回到阈值以上（充值），重置通知状态
	if user.Balance >= threshold && user.BalanceAlertNotified {
		_ = s.repo.SetBalanceAlertNotified(ctx, user.ID, false)
	}
}

// ListGroupRateOverrides 返回指定分组下所有设置了专属倍率的用户。
func (s *Service) ListGroupRateOverrides(ctx context.Context, groupID int64) ([]GroupRateOverride, error) {
	return s.repo.ListWithGroupRateOverride(ctx, groupID)
}

// SetGroupRate 为用户在指定分组下设置/更新专属倍率（rate 必须 > 0）。
//
// 读 - 改 - 写：先拉出用户当前的 group_rates map，修改单个条目，再整体写回。
// 并发场景下存在理论上的写丢失窗口，但后台管理单用户操作可以接受。
func (s *Service) SetGroupRate(ctx context.Context, userID int, groupID int64, rate float64) (GroupRateOverride, error) {
	if rate <= 0 {
		return GroupRateOverride{}, ErrInvalidRateMultiplier
	}
	u, err := s.repo.FindByID(ctx, userID, false)
	if err != nil {
		return GroupRateOverride{}, err
	}
	rates := cloneGroupRates(u.GroupRates)
	if rates == nil {
		rates = make(map[int64]float64)
	}
	rates[groupID] = rate
	updated, err := s.repo.Update(ctx, userID, Mutation{
		GroupRates:    rates,
		HasGroupRates: true,
	})
	if err != nil {
		return GroupRateOverride{}, err
	}
	return GroupRateOverride{
		UserID:   updated.ID,
		Email:    updated.Email,
		Username: updated.Username,
		Rate:     rate,
	}, nil
}

// DeleteGroupRate 删除用户在指定分组下的专属倍率。
func (s *Service) DeleteGroupRate(ctx context.Context, userID int, groupID int64) error {
	u, err := s.repo.FindByID(ctx, userID, false)
	if err != nil {
		return err
	}
	if _, hasRate := u.GroupRates[groupID]; !hasRate {
		return nil // 幂等：本来就没有，直接成功
	}
	rates := cloneGroupRates(u.GroupRates)
	delete(rates, groupID)
	_, err = s.repo.Update(ctx, userID, Mutation{
		GroupRates:    rates,
		HasGroupRates: true,
	})
	return err
}

// Delete 删除用户。
func (s *Service) Delete(ctx context.Context, id int) error {
	logger := logx.LoggerFromContext(ctx)
	item, err := s.repo.FindByID(ctx, id, false)
	if err != nil {
		logger.Error("user_lookup_failed",
			logx.LogFieldUserID, id,
			logx.LogFieldReason, "delete",
			logx.LogFieldError, err,
		)
		return err
	}
	if item.Role == "admin" {
		logger.Warn("user_delete_rejected",
			logx.LogFieldUserID, id,
			logx.LogFieldReason, "delete_admin_forbidden",
		)
		return ErrDeleteAdminForbidden
	}
	if err := s.repo.Delete(ctx, id); err != nil {
		logger.Error("user_delete_failed",
			logx.LogFieldUserID, id,
			logx.LogFieldError, err,
		)
		return err
	}
	logger.Info("user_deleted", logx.LogFieldUserID, id)
	return nil
}

// ToggleStatus 切换用户状态。
func (s *Service) ToggleStatus(ctx context.Context, id int) (ToggleResult, error) {
	logger := logx.LoggerFromContext(ctx)
	item, err := s.repo.FindByID(ctx, id, false)
	if err != nil {
		logger.Error("user_lookup_failed",
			logx.LogFieldUserID, id,
			logx.LogFieldReason, "toggle_status",
			logx.LogFieldError, err,
		)
		return ToggleResult{}, err
	}
	newStatus := "disabled"
	if item.Status == "disabled" {
		newStatus = "active"
	}
	updated, err := s.repo.Update(ctx, id, Mutation{Status: &newStatus})
	if err != nil {
		logger.Error("user_update_failed",
			logx.LogFieldUserID, id,
			logx.LogFieldReason, "toggle_status",
			logx.LogFieldError, err,
		)
		return ToggleResult{}, err
	}
	if newStatus == "disabled" {
		logger.Info("user_disabled", logx.LogFieldUserID, id)
	} else {
		logger.Info("user_enabled", logx.LogFieldUserID, id)
	}
	return ToggleResult{ID: updated.ID, Status: updated.Status}, nil
}

// ListBalanceLogs 查询用户余额历史。
func (s *Service) ListBalanceLogs(ctx context.Context, userID, page, pageSize int) (BalanceLogList, error) {
	page, pageSize = pagination.Normalize(page, pageSize)
	list, total, err := s.repo.ListBalanceLogs(ctx, userID, page, pageSize)
	if err != nil {
		return BalanceLogList{}, err
	}
	return BalanceLogList{List: list, Total: total, Page: page, PageSize: pageSize}, nil
}

// GetAPIKeyInfo 获取 API Key 概要信息。
func (s *Service) GetAPIKeyInfo(ctx context.Context, keyID int) (APIKeyBrief, error) {
	return s.repo.GetAPIKeyInfo(ctx, keyID)
}

// ListAPIKeys 查询指定用户的 API Key 列表。
// tz 决定每个 key 的"今日成本"起点；为空时回退到服务器本地时区。
func (s *Service) ListAPIKeys(ctx context.Context, userID, page, pageSize int, tz string) (APIKeyList, error) {
	page, pageSize = pagination.Normalize(page, pageSize)
	loc := timezone.Resolve(tz)
	todayStart := timezone.StartOfDay(time.Now().In(loc))
	list, total, err := s.repo.ListAPIKeys(ctx, userID, page, pageSize, todayStart)
	if err != nil {
		return APIKeyList{}, err
	}
	return APIKeyList{List: list, Total: total, Page: page, PageSize: pageSize}, nil
}

func cloneGroupRates(input map[int64]float64) map[int64]float64 {
	if input == nil {
		return nil
	}
	cloned := make(map[int64]float64, len(input))
	for key, value := range input {
		cloned[key] = value
	}
	return cloned
}

func stringPtr(value string) *string {
	return &value
}

func intPtrIfPositive(value int) *int {
	if value <= 0 {
		return nil
	}
	return &value
}
