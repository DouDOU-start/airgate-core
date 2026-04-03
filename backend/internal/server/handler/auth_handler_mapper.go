package handler

import (
	appauth "github.com/DouDOU-start/airgate-core/internal/app/auth"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
)

// userToResp 将认证域 User 转换为 DTO 响应。
func userToResp(user appauth.User) dto.UserResp {
	return dto.UserResp{
		ID:             int64(user.ID),
		Email:          user.Email,
		Username:       user.Username,
		Balance:        user.Balance,
		Role:           user.Role,
		MaxConcurrency: user.MaxConcurrency,
		GroupRates:     user.GroupRates,
		AllowedGroupIDs: append([]int64(nil),
			user.AllowedGroupIDs...),
		Status: user.Status,
		TimeMixin: dto.TimeMixin{
			CreatedAt: user.CreatedAt,
			UpdatedAt: user.UpdatedAt,
		},
	}
}
