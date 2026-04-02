package user

import (
	"context"

	"golang.org/x/crypto/bcrypt"
)

const (
	defaultPage     = 1
	defaultPageSize = 20
)

// Service 用户应用服务。
type Service struct {
	repo Repository
}

// NewService 创建用户服务。
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// Get 获取用户。
func (s *Service) Get(ctx context.Context, id int) (User, error) {
	return s.repo.FindByID(ctx, id, true)
}

// UpdateProfile 更新当前用户资料。
func (s *Service) UpdateProfile(ctx context.Context, id int, username string) (User, error) {
	return s.repo.Update(ctx, id, Mutation{Username: &username})
}

// ChangePassword 修改当前用户密码。
func (s *Service) ChangePassword(ctx context.Context, id int, oldPassword, newPassword string) error {
	item, err := s.repo.FindByID(ctx, id, false)
	if err != nil {
		return err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(item.PasswordHash), []byte(oldPassword)); err != nil {
		return ErrOldPasswordMismatch
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = s.repo.Update(ctx, id, Mutation{PasswordHash: stringPtr(string(hash))})
	return err
}

// List 查询用户列表。
func (s *Service) List(ctx context.Context, filter ListFilter) (ListResult, error) {
	page, pageSize := normalizePage(filter.Page, filter.PageSize)
	filter.Page = page
	filter.PageSize = pageSize

	list, total, err := s.repo.List(ctx, filter)
	if err != nil {
		return ListResult{}, err
	}
	return ListResult{List: list, Total: total, Page: page, PageSize: pageSize}, nil
}

// Create 创建用户。
func (s *Service) Create(ctx context.Context, input CreateInput) (User, error) {
	exists, err := s.repo.EmailExists(ctx, input.Email)
	if err != nil {
		return User{}, err
	}
	if exists {
		return User{}, ErrEmailAlreadyExists
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		return User{}, err
	}

	return s.repo.Create(ctx, Mutation{
		Email:          &input.Email,
		PasswordHash:   stringPtr(string(hash)),
		Username:       &input.Username,
		Role:           &input.Role,
		MaxConcurrency: intPtrIfPositive(input.MaxConcurrency),
		GroupRates:     cloneGroupRates(input.GroupRates),
		HasGroupRates:  input.GroupRates != nil,
	})
}

// Update 更新用户。
func (s *Service) Update(ctx context.Context, id int, input UpdateInput) (User, error) {
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
			return User{}, err
		}
		mutation.PasswordHash = stringPtr(string(hash))
	}
	return s.repo.Update(ctx, id, mutation)
}

// AdjustBalance 调整用户余额。
func (s *Service) AdjustBalance(ctx context.Context, id int, change BalanceChange) (User, error) {
	item, err := s.repo.FindByID(ctx, id, false)
	if err != nil {
		return User{}, err
	}

	beforeBalance := item.Balance
	var afterBalance float64
	switch change.Action {
	case "set":
		afterBalance = change.Amount
	case "add":
		afterBalance = beforeBalance + change.Amount
	case "subtract":
		if beforeBalance < change.Amount {
			return User{}, ErrInsufficientBalance
		}
		afterBalance = beforeBalance - change.Amount
	default:
		return User{}, ErrInvalidBalanceAction
	}

	return s.repo.UpdateBalance(ctx, id, BalanceUpdate{
		Action:        change.Action,
		Amount:        change.Amount,
		BeforeBalance: beforeBalance,
		AfterBalance:  afterBalance,
		Remark:        change.Remark,
	})
}

// Delete 删除用户。
func (s *Service) Delete(ctx context.Context, id int) error {
	item, err := s.repo.FindByID(ctx, id, false)
	if err != nil {
		return err
	}
	if item.Role == "admin" {
		return ErrDeleteAdminForbidden
	}
	return s.repo.Delete(ctx, id)
}

// ToggleStatus 切换用户状态。
func (s *Service) ToggleStatus(ctx context.Context, id int) (ToggleResult, error) {
	item, err := s.repo.FindByID(ctx, id, false)
	if err != nil {
		return ToggleResult{}, err
	}
	newStatus := "disabled"
	if item.Status == "disabled" {
		newStatus = "active"
	}
	updated, err := s.repo.Update(ctx, id, Mutation{Status: &newStatus})
	if err != nil {
		return ToggleResult{}, err
	}
	return ToggleResult{ID: updated.ID, Status: updated.Status}, nil
}

// ListBalanceLogs 查询用户余额历史。
func (s *Service) ListBalanceLogs(ctx context.Context, userID, page, pageSize int) (BalanceLogList, error) {
	page, pageSize = normalizePage(page, pageSize)
	list, total, err := s.repo.ListBalanceLogs(ctx, userID, page, pageSize)
	if err != nil {
		return BalanceLogList{}, err
	}
	return BalanceLogList{List: list, Total: total, Page: page, PageSize: pageSize}, nil
}

// ListAPIKeys 查询指定用户的 API Key 列表。
func (s *Service) ListAPIKeys(ctx context.Context, userID, page, pageSize int) (APIKeyList, error) {
	page, pageSize = normalizePage(page, pageSize)
	list, total, err := s.repo.ListAPIKeys(ctx, userID, page, pageSize)
	if err != nil {
		return APIKeyList{}, err
	}
	return APIKeyList{List: list, Total: total, Page: page, PageSize: pageSize}, nil
}

func normalizePage(page, pageSize int) (int, int) {
	if page <= 0 {
		page = defaultPage
	}
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}
	return page, pageSize
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
