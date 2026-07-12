package store

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/DouDOU-start/airgate-core/ent"
	entbalancelog "github.com/DouDOU-start/airgate-core/ent/balancelog"
	apppayment "github.com/DouDOU-start/airgate-core/internal/app/payment"
	appredemption "github.com/DouDOU-start/airgate-core/internal/app/redemption"
	appuser "github.com/DouDOU-start/airgate-core/internal/app/user"
)

func createTestUserWithBalance(t *testing.T, db *ent.Client, email string, balance float64) *ent.User {
	t.Helper()
	user, err := db.User.Create().
		SetEmail(email).
		SetPasswordHash("hash").
		SetBalance(balance).
		Save(context.Background())
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return user
}

func userBalance(t *testing.T, db *ent.Client, id int) float64 {
	t.Helper()
	u, err := db.User.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	return u.Balance
}

func latestBalanceLog(t *testing.T, db *ent.Client, userID int) *ent.BalanceLog {
	t.Helper()
	item, err := db.BalanceLog.Query().
		Where(entbalancelog.UserIDSnapshotEQ(userID)).
		Order(ent.Desc(entbalancelog.FieldID)).
		First(context.Background())
	if err != nil {
		t.Fatalf("query balance log: %v", err)
	}
	return item
}

// TestUserStoreUpdateBalanceActions 校验 add/subtract/set 三种模式的余额与流水语义。
func TestUserStoreUpdateBalanceActions(t *testing.T) {
	db := enttestOpen(t)
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	s := NewUserStore(db)

	user := createTestUserWithBalance(t, db, "balance@example.com", 10)

	cases := []struct {
		action     string
		amount     float64
		wantAfter  float64
		wantBefore float64
	}{
		{"add", 5, 15, 10},
		{"subtract", 3, 12, 15},
		{"set", 100, 100, 12},
	}
	for _, tc := range cases {
		updated, err := s.UpdateBalance(ctx, user.ID, appuser.BalanceChange{
			Action: tc.action,
			Amount: tc.amount,
			Remark: "test-" + tc.action,
		})
		if err != nil {
			t.Fatalf("UpdateBalance(%s) error: %v", tc.action, err)
		}
		if math.Abs(updated.Balance-tc.wantAfter) > 1e-9 {
			t.Fatalf("UpdateBalance(%s) balance = %v, want %v", tc.action, updated.Balance, tc.wantAfter)
		}
		log := latestBalanceLog(t, db, user.ID)
		if math.Abs(log.BeforeBalance-tc.wantBefore) > 1e-9 || math.Abs(log.AfterBalance-tc.wantAfter) > 1e-9 {
			t.Fatalf("UpdateBalance(%s) log before/after = %v/%v, want %v/%v",
				tc.action, log.BeforeBalance, log.AfterBalance, tc.wantBefore, tc.wantAfter)
		}
	}
}

// TestUserStoreUpdateBalanceRejects 余额不足与非法 action 由 store 在事务内判定。
func TestUserStoreUpdateBalanceRejects(t *testing.T) {
	db := enttestOpen(t)
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	s := NewUserStore(db)

	user := createTestUserWithBalance(t, db, "reject@example.com", 5)

	if _, err := s.UpdateBalance(ctx, user.ID, appuser.BalanceChange{Action: "subtract", Amount: 10}); !errors.Is(err, appuser.ErrInsufficientBalance) {
		t.Fatalf("subtract 超额错误 = %v，期望 ErrInsufficientBalance", err)
	}
	if _, err := s.UpdateBalance(ctx, user.ID, appuser.BalanceChange{Action: "noop", Amount: 1}); !errors.Is(err, appuser.ErrInvalidBalanceAction) {
		t.Fatalf("非法 action 错误 = %v，期望 ErrInvalidBalanceAction", err)
	}
	if _, err := s.UpdateBalance(ctx, 99999, appuser.BalanceChange{Action: "add", Amount: 1}); !errors.Is(err, appuser.ErrUserNotFound) {
		t.Fatalf("不存在用户错误 = %v，期望 ErrUserNotFound", err)
	}
	if got := userBalance(t, db, user.ID); math.Abs(got-5) > 1e-9 {
		t.Fatalf("拒绝路径不应改动余额，balance = %v", got)
	}
}

// TestUserStoreUpdateBalanceIdempotency 同一幂等键只入账一次。
func TestUserStoreUpdateBalanceIdempotency(t *testing.T) {
	db := enttestOpen(t)
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	s := NewUserStore(db)

	user := createTestUserWithBalance(t, db, "idem@example.com", 0)
	change := appuser.BalanceChange{Action: "add", Amount: 7, IdempotencyKey: "test:key-1"}

	if _, err := s.UpdateBalance(ctx, user.ID, change); err != nil {
		t.Fatalf("首次入账失败: %v", err)
	}
	if _, err := s.UpdateBalance(ctx, user.ID, change); !errors.Is(err, appuser.ErrDuplicateBalanceChange) {
		t.Fatalf("重复入账错误 = %v，期望 ErrDuplicateBalanceChange", err)
	}
	if got := userBalance(t, db, user.ID); math.Abs(got-7) > 1e-9 {
		t.Fatalf("幂等键重复提交后 balance = %v，期望 7", got)
	}
}

// TestUserStoreUpdateBalanceIncrementSemantics 增量语义：add 走
// balance = balance + amount，不覆盖并发变更后的最新值。
// 模拟并发：读出用户后、调整余额前，计费侧先扣了一笔（直接 AddBalance 落库）。
func TestUserStoreUpdateBalanceIncrementSemantics(t *testing.T) {
	db := enttestOpen(t)
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	s := NewUserStore(db)

	user := createTestUserWithBalance(t, db, "increment@example.com", 100)

	// 模拟计费侧原子扣费 30（sqlite 单写者下无法真正并发持锁，这里验证
	// UpdateBalance 基于最新库值做增量，而不是覆盖旧快照）
	if err := db.User.UpdateOneID(user.ID).AddBalance(-30).Exec(ctx); err != nil {
		t.Fatalf("模拟扣费失败: %v", err)
	}

	updated, err := s.UpdateBalance(ctx, user.ID, appuser.BalanceChange{Action: "add", Amount: 10})
	if err != nil {
		t.Fatalf("UpdateBalance error: %v", err)
	}
	if math.Abs(updated.Balance-80) > 1e-9 {
		t.Fatalf("balance = %v，期望 80（100-30+10，计费扣减不被覆盖）", updated.Balance)
	}
	log := latestBalanceLog(t, db, user.ID)
	if math.Abs(log.BeforeBalance-70) > 1e-9 || math.Abs(log.AfterBalance-80) > 1e-9 {
		t.Fatalf("log before/after = %v/%v，期望 70/80", log.BeforeBalance, log.AfterBalance)
	}
}

func createTestPaymentOrder(t *testing.T, db *ent.Client, userID int, outTradeNo string, amount float64) *ent.PaymentOrder {
	t.Helper()
	order, err := db.PaymentOrder.Create().
		SetOutTradeNo(outTradeNo).
		SetUserID(userID).
		SetMethod("alipay").
		SetProviderID("epay_1").
		SetAmount(amount).
		SetExpiresAt(time.Now().Add(30 * time.Minute)).
		Save(context.Background())
	if err != nil {
		t.Fatalf("create payment order: %v", err)
	}
	return order
}

// TestPaymentStoreCreditPaidOrder 支付入账：金额校验、状态流转、余额增量、幂等。
func TestPaymentStoreCreditPaidOrder(t *testing.T) {
	db := enttestOpen(t)
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	s := NewPaymentStore(db)

	user := createTestUserWithBalance(t, db, "pay@example.com", 50)
	createTestPaymentOrder(t, db, user.ID, "AG-TEST-1", 20)

	// 金额不符拒绝入账
	if _, err := s.CreditPaidOrder(ctx, apppayment.CreditInput{OutTradeNo: "AG-TEST-1", Amount: 19}); !errors.Is(err, apppayment.ErrAmountMismatch) {
		t.Fatalf("金额不符错误 = %v，期望 ErrAmountMismatch", err)
	}
	if got := userBalance(t, db, user.ID); math.Abs(got-50) > 1e-9 {
		t.Fatalf("金额不符时不应入账，balance = %v", got)
	}

	// 正常入账
	credit, err := s.CreditPaidOrder(ctx, apppayment.CreditInput{OutTradeNo: "AG-TEST-1", Amount: 20, Remark: "在线充值"})
	if err != nil || credit.AlreadyPaid {
		t.Fatalf("入账结果 = (%+v, %v)，期望 (AlreadyPaid=false, nil)", credit, err)
	}
	// 首次入账返回用户/金额/入账后余额，供充值成功通知
	if credit.Email == "" || math.Abs(credit.Amount-20) > 1e-9 || math.Abs(credit.BalanceAfter-70) > 1e-9 {
		t.Fatalf("入账结果字段 = %+v，期望 email 非空、amount=20、balance=70", credit)
	}
	if got := userBalance(t, db, user.ID); math.Abs(got-70) > 1e-9 {
		t.Fatalf("入账后 balance = %v，期望 70", got)
	}
	order, err := s.GetOrder(ctx, "AG-TEST-1")
	if err != nil || order.Status != apppayment.StatusPaid {
		t.Fatalf("订单状态 = %v(err=%v)，期望 paid", order.Status, err)
	}
	log := latestBalanceLog(t, db, user.ID)
	if log.IdempotencyKey == nil || *log.IdempotencyKey != "epay:AG-TEST-1" {
		t.Fatalf("流水幂等键 = %v，期望 epay:AG-TEST-1", log.IdempotencyKey)
	}
	if math.Abs(log.BeforeBalance-50) > 1e-9 || math.Abs(log.AfterBalance-70) > 1e-9 {
		t.Fatalf("流水 before/after = %v/%v，期望 50/70", log.BeforeBalance, log.AfterBalance)
	}

	// 平台重发回调：已 paid 幂等短路，不重复入账
	credit, err = s.CreditPaidOrder(ctx, apppayment.CreditInput{OutTradeNo: "AG-TEST-1", Amount: 20})
	if err != nil || !credit.AlreadyPaid {
		t.Fatalf("重复回调结果 = (%+v, %v)，期望 (AlreadyPaid=true, nil)", credit, err)
	}
	if got := userBalance(t, db, user.ID); math.Abs(got-70) > 1e-9 {
		t.Fatalf("重复回调后 balance = %v，期望仍为 70", got)
	}

	// 不存在的订单
	if _, err := s.CreditPaidOrder(ctx, apppayment.CreditInput{OutTradeNo: "AG-MISSING", Amount: 1}); !errors.Is(err, apppayment.ErrOrderNotFound) {
		t.Fatalf("查无订单错误 = %v，期望 ErrOrderNotFound", err)
	}
}

func createTestRedemptionCode(t *testing.T, db *ent.Client, code string, value float64, status string, expiresAt *time.Time) *ent.RedemptionCode {
	t.Helper()
	builder := db.RedemptionCode.Create().
		SetCode(code).
		SetValue(value).
		SetStatus(status)
	if expiresAt != nil {
		builder = builder.SetExpiresAt(*expiresAt)
	}
	item, err := builder.Save(context.Background())
	if err != nil {
		t.Fatalf("create redemption code: %v", err)
	}
	return item
}

// TestRedemptionStoreRedeem 兑换入账：状态流转、余额增量、重复兑换与过期/停用拒绝。
func TestRedemptionStoreRedeem(t *testing.T) {
	db := enttestOpen(t)
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	s := NewRedemptionStore(db)
	now := time.Now()

	user := createTestUserWithBalance(t, db, "redeem@example.com", 1)
	createTestRedemptionCode(t, db, "CODE-OK", 9, appredemption.StatusUnused, nil)

	result, err := s.Redeem(ctx, "CODE-OK", user.ID, now)
	if err != nil {
		t.Fatalf("Redeem error: %v", err)
	}
	if math.Abs(result.Value-9) > 1e-9 || math.Abs(result.Balance-10) > 1e-9 {
		t.Fatalf("Redeem 结果 = %+v，期望 value 9 / balance 10", result)
	}
	if got := userBalance(t, db, user.ID); math.Abs(got-10) > 1e-9 {
		t.Fatalf("兑换后 balance = %v，期望 10", got)
	}
	log := latestBalanceLog(t, db, user.ID)
	if log.IdempotencyKey == nil || *log.IdempotencyKey != "redeem:CODE-OK" {
		t.Fatalf("流水幂等键 = %v，期望 redeem:CODE-OK", log.IdempotencyKey)
	}

	// 同一码重复兑换拒绝且不再入账
	if _, err := s.Redeem(ctx, "CODE-OK", user.ID, now); !errors.Is(err, appredemption.ErrCodeUsed) {
		t.Fatalf("重复兑换错误 = %v，期望 ErrCodeUsed", err)
	}
	if got := userBalance(t, db, user.ID); math.Abs(got-10) > 1e-9 {
		t.Fatalf("重复兑换后 balance = %v，期望仍为 10", got)
	}

	// 过期 / 停用 / 不存在
	expired := now.Add(-time.Hour)
	createTestRedemptionCode(t, db, "CODE-EXPIRED", 5, appredemption.StatusUnused, &expired)
	if _, err := s.Redeem(ctx, "CODE-EXPIRED", user.ID, now); !errors.Is(err, appredemption.ErrCodeExpired) {
		t.Fatalf("过期码错误 = %v，期望 ErrCodeExpired", err)
	}
	createTestRedemptionCode(t, db, "CODE-DISABLED", 5, appredemption.StatusDisabled, nil)
	if _, err := s.Redeem(ctx, "CODE-DISABLED", user.ID, now); !errors.Is(err, appredemption.ErrCodeDisabled) {
		t.Fatalf("停用码错误 = %v，期望 ErrCodeDisabled", err)
	}
	if _, err := s.Redeem(ctx, "CODE-MISSING", user.ID, now); !errors.Is(err, appredemption.ErrCodeNotFound) {
		t.Fatalf("查无码错误 = %v，期望 ErrCodeNotFound", err)
	}
	if _, err := s.Redeem(ctx, "CODE-OK", 99999, now); err == nil {
		t.Fatal("已用码 + 不存在用户应报错")
	}
}

// TestRedeemRejectsMissingUser 用户不存在时兑换整体回滚，码保持 unused。
func TestRedeemRejectsMissingUser(t *testing.T) {
	db := enttestOpen(t)
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	s := NewRedemptionStore(db)

	createTestRedemptionCode(t, db, "CODE-NOUSER", 5, appredemption.StatusUnused, nil)
	if _, err := s.Redeem(ctx, "CODE-NOUSER", 42424242, time.Now()); !errors.Is(err, appredemption.ErrUserNotFound) {
		t.Fatalf("错误 = %v，期望 ErrUserNotFound", err)
	}
	item, err := db.RedemptionCode.Query().Only(ctx)
	if err != nil {
		t.Fatalf("query code: %v", err)
	}
	if item.Status != appredemption.StatusUnused {
		t.Fatalf("回滚后码状态 = %v，期望 unused", item.Status)
	}
}
