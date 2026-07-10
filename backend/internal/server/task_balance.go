package server

import (
	"context"
	"errors"

	appuser "github.com/DouDOU-start/airgate-core/internal/app/user"
	"github.com/DouDOU-start/airgate-core/internal/relay/task"
)

// taskBalanceAdapter 把 appuser.Service 适配为 task.BalanceOps：
// 任务子系统禁止 import app 包，余额动账经此桥接（含哨兵错误映射）。
type taskBalanceAdapter struct {
	svc *appuser.Service
}

// Hold 预扣：action=subtract（store 层行锁校验余额，不足报 ErrInsufficientBalance）。
func (a taskBalanceAdapter) Hold(ctx context.Context, userID int, amount float64, remark string) error {
	_, err := a.svc.AdjustBalance(ctx, userID, appuser.BalanceChange{
		Action: "subtract",
		Amount: amount,
		Remark: remark,
	})
	if errors.Is(err, appuser.ErrInsufficientBalance) {
		return task.ErrInsufficientBalance
	}
	return err
}

// Adjust 结算/退款动账：action=add（amount 可为负，补扣允许扣穿——服务已消费）。
// 幂等键命中由 AdjustBalance 内部消化（返回当前状态、无错误），天然幂等。
func (a taskBalanceAdapter) Adjust(ctx context.Context, userID int, amount float64, remark, idemKey string) error {
	_, err := a.svc.AdjustBalance(ctx, userID, appuser.BalanceChange{
		Action:         "add",
		Amount:         amount,
		Remark:         remark,
		IdempotencyKey: idemKey,
	})
	return err
}
