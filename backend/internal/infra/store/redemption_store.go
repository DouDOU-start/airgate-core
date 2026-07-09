package store

import (
	"context"
	"fmt"
	"time"

	"github.com/DouDOU-start/airgate-core/ent"
	entbalancelog "github.com/DouDOU-start/airgate-core/ent/balancelog"
	"github.com/DouDOU-start/airgate-core/ent/predicate"
	entredemptioncode "github.com/DouDOU-start/airgate-core/ent/redemptioncode"
	entuser "github.com/DouDOU-start/airgate-core/ent/user"
	appredemption "github.com/DouDOU-start/airgate-core/internal/app/redemption"
)

// RedemptionStore 兑换码持久化实现。
type RedemptionStore struct {
	db *ent.Client
}

// NewRedemptionStore 创建兑换码存储。
func NewRedemptionStore(db *ent.Client) *RedemptionStore {
	return &RedemptionStore{db: db}
}

func toRedemptionCode(item *ent.RedemptionCode) appredemption.Code {
	return appredemption.Code{
		ID:          item.ID,
		Code:        item.Code,
		Value:       item.Value,
		Status:      item.Status,
		Remark:      item.Remark,
		UsedByID:    item.UsedByID,
		UsedByEmail: item.UsedByEmail,
		UsedAt:      item.UsedAt,
		ExpiresAt:   item.ExpiresAt,
		CreatedAt:   item.CreatedAt,
		UpdatedAt:   item.UpdatedAt,
	}
}

// CreateBatch 单事务批量写入。
func (s *RedemptionStore) CreateBatch(ctx context.Context, codes []appredemption.Code) ([]appredemption.Code, error) {
	builders := make([]*ent.RedemptionCodeCreate, 0, len(codes))
	for _, c := range codes {
		builder := s.db.RedemptionCode.Create().
			SetCode(c.Code).
			SetValue(c.Value).
			SetStatus(c.Status).
			SetRemark(c.Remark)
		if c.ExpiresAt != nil {
			builder = builder.SetExpiresAt(*c.ExpiresAt)
		}
		builders = append(builders, builder)
	}
	created, err := s.db.RedemptionCode.CreateBulk(builders...).Save(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]appredemption.Code, 0, len(created))
	for _, item := range created {
		result = append(result, toRedemptionCode(item))
	}
	return result, nil
}

// redemptionStatusPredicates 将筛选状态翻译为谓词；unused/expired 按 now 现算拆分。
func redemptionStatusPredicates(status string, now time.Time) []predicate.RedemptionCode {
	switch status {
	case appredemption.StatusUnused:
		return []predicate.RedemptionCode{
			entredemptioncode.StatusEQ(appredemption.StatusUnused),
			entredemptioncode.Or(
				entredemptioncode.ExpiresAtIsNil(),
				entredemptioncode.ExpiresAtGT(now),
			),
		}
	case appredemption.StatusExpired:
		return []predicate.RedemptionCode{
			entredemptioncode.StatusEQ(appredemption.StatusUnused),
			entredemptioncode.ExpiresAtNotNil(),
			entredemptioncode.ExpiresAtLTE(now),
		}
	case appredemption.StatusUsed, appredemption.StatusDisabled:
		return []predicate.RedemptionCode{entredemptioncode.StatusEQ(status)}
	default:
		return nil
	}
}

// List 分页列表（created_at desc）。
func (s *RedemptionStore) List(ctx context.Context, f appredemption.ListFilter, now time.Time) ([]appredemption.Code, int64, error) {
	query := s.db.RedemptionCode.Query()
	for _, p := range redemptionStatusPredicates(f.Status, now) {
		query = query.Where(p)
	}
	if f.Keyword != "" {
		query = query.Where(entredemptioncode.Or(
			entredemptioncode.CodeContains(f.Keyword),
			entredemptioncode.RemarkContains(f.Keyword),
		))
	}

	total, err := query.Clone().Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	items, err := query.
		Order(ent.Desc(entredemptioncode.FieldCreatedAt), ent.Desc(entredemptioncode.FieldID)).
		Offset((f.Page - 1) * f.PageSize).
		Limit(f.PageSize).
		All(ctx)
	if err != nil {
		return nil, 0, err
	}
	result := make([]appredemption.Code, 0, len(items))
	for _, item := range items {
		result = append(result, toRedemptionCode(item))
	}
	return result, int64(total), nil
}

// Stats 全量统计；unused 剔除已过期码，过期单列。
func (s *RedemptionStore) Stats(ctx context.Context, now time.Time) (appredemption.Stats, error) {
	var rows []struct {
		Status string  `json:"status"`
		Count  int64   `json:"count"`
		Value  float64 `json:"value"`
	}
	err := s.db.RedemptionCode.Query().
		GroupBy(entredemptioncode.FieldStatus).
		Aggregate(
			ent.Count(),
			ent.As(ent.Sum(entredemptioncode.FieldValue), "value"),
		).
		Scan(ctx, &rows)
	if err != nil {
		return appredemption.Stats{}, err
	}

	var stats appredemption.Stats
	for _, row := range rows {
		stats.Total += row.Count
		switch row.Status {
		case appredemption.StatusUnused:
			stats.Unused += row.Count
			stats.UnusedValue += row.Value
		case appredemption.StatusUsed:
			stats.Used = row.Count
			stats.UsedValue = row.Value
		case appredemption.StatusDisabled:
			stats.Disabled = row.Count
		}
	}

	// 过期码是 unused 的子集，单独现算并从 unused 中扣除
	var expiredRows []struct {
		Count int64   `json:"count"`
		Value float64 `json:"value"`
	}
	err = s.db.RedemptionCode.Query().
		Where(
			entredemptioncode.StatusEQ(appredemption.StatusUnused),
			entredemptioncode.ExpiresAtNotNil(),
			entredemptioncode.ExpiresAtLTE(now),
		).
		Aggregate(
			ent.Count(),
			ent.As(ent.Sum(entredemptioncode.FieldValue), "value"),
		).
		Scan(ctx, &expiredRows)
	if err != nil {
		return appredemption.Stats{}, err
	}
	if len(expiredRows) > 0 {
		stats.Expired = expiredRows[0].Count
		stats.Unused -= expiredRows[0].Count
		stats.UnusedValue -= expiredRows[0].Value
	}
	return stats, nil
}

// FindByID 按 ID 查询。
func (s *RedemptionStore) FindByID(ctx context.Context, id int) (appredemption.Code, error) {
	item, err := s.db.RedemptionCode.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			return appredemption.Code{}, appredemption.ErrCodeNotFound
		}
		return appredemption.Code{}, err
	}
	return toRedemptionCode(item), nil
}

// UpdateStatus 条件更新状态：WHERE status=from 抢占，0 行受影响视为状态冲突。
func (s *RedemptionStore) UpdateStatus(ctx context.Context, id int, from, to string) error {
	affected, err := s.db.RedemptionCode.Update().
		Where(
			entredemptioncode.IDEQ(id),
			entredemptioncode.StatusEQ(from),
		).
		SetStatus(to).
		Save(ctx)
	if err != nil {
		return err
	}
	if affected == 0 {
		if _, err := s.db.RedemptionCode.Get(ctx, id); ent.IsNotFound(err) {
			return appredemption.ErrCodeNotFound
		}
		return appredemption.ErrCodeStateConflict
	}
	return nil
}

// Delete 硬删除。
func (s *RedemptionStore) Delete(ctx context.Context, id int) error {
	err := s.db.RedemptionCode.DeleteOneID(id).Exec(ctx)
	if ent.IsNotFound(err) {
		return appredemption.ErrCodeNotFound
	}
	return err
}

// Redeem 单事务入账：码 unused→used + 用户加余额 + 余额流水（幂等键兜底）。
//
// 防重三层：code 唯一约束 → 条件更新 WHERE status='unused' →
// balance_log idempotency_key 唯一索引。参照 PaymentStore.CreditPaidOrder。
func (s *RedemptionStore) Redeem(ctx context.Context, code string, userID int, now time.Time) (appredemption.RedeemResult, error) {
	var zero appredemption.RedeemResult
	tx, err := s.db.Tx(ctx)
	if err != nil {
		return zero, err
	}
	defer func() { _ = tx.Rollback() }()

	item, err := tx.RedemptionCode.Query().
		Where(entredemptioncode.CodeEQ(code)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return zero, appredemption.ErrCodeNotFound
		}
		return zero, err
	}
	switch item.Status {
	case appredemption.StatusUsed:
		return zero, appredemption.ErrCodeUsed
	case appredemption.StatusDisabled:
		return zero, appredemption.ErrCodeDisabled
	}
	if item.ExpiresAt != nil && !item.ExpiresAt.After(now) {
		return zero, appredemption.ErrCodeExpired
	}

	usr, err := tx.User.Query().Where(entuser.IDEQ(userID)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return zero, appredemption.ErrUserNotFound
		}
		return zero, err
	}

	// 条件更新抢占：同一码并发兑换只有一个能改成功
	affected, err := tx.RedemptionCode.Update().
		Where(
			entredemptioncode.IDEQ(item.ID),
			entredemptioncode.StatusEQ(appredemption.StatusUnused),
		).
		SetStatus(appredemption.StatusUsed).
		SetUsedByID(usr.ID).
		SetUsedByEmail(usr.Email).
		SetUsedAt(now).
		Save(ctx)
	if err != nil {
		return zero, err
	}
	if affected == 0 {
		return zero, appredemption.ErrCodeUsed // 另一次并发兑换已抢占
	}

	before := usr.Balance
	after := before + item.Value
	if _, err := tx.User.UpdateOneID(usr.ID).SetBalance(after).Save(ctx); err != nil {
		return zero, err
	}

	if _, err := tx.BalanceLog.Create().
		SetAction(entbalancelog.ActionAdd).
		SetAmount(item.Value).
		SetBeforeBalance(before).
		SetAfterBalance(after).
		SetRemark(fmt.Sprintf("兑换码充值（%s）", item.Code)).
		SetUserIDSnapshot(usr.ID).
		SetUserEmailSnapshot(usr.Email).
		SetUserID(usr.ID).
		SetIdempotencyKey("redeem:" + item.Code).
		Save(ctx); err != nil {
		if ent.IsConstraintError(err) {
			return zero, appredemption.ErrCodeUsed // 幂等键冲突：该码已入账过
		}
		return zero, err
	}

	if err := tx.Commit(); err != nil {
		return zero, err
	}
	return appredemption.RedeemResult{Value: item.Value, Balance: after}, nil
}
