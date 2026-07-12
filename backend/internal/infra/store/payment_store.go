package store

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/DouDOU-start/airgate-core/ent"
	entbalancelog "github.com/DouDOU-start/airgate-core/ent/balancelog"
	entpaymentorder "github.com/DouDOU-start/airgate-core/ent/paymentorder"
	entpaymentproviderconfig "github.com/DouDOU-start/airgate-core/ent/paymentproviderconfig"
	entuser "github.com/DouDOU-start/airgate-core/ent/user"
	apppayment "github.com/DouDOU-start/airgate-core/internal/app/payment"
)

// PaymentStore 支付域持久化实现（订单 + 服务商配置）。
type PaymentStore struct {
	db *ent.Client
}

// NewPaymentStore 创建支付存储。
func NewPaymentStore(db *ent.Client) *PaymentStore {
	return &PaymentStore{db: db}
}

func toPaymentOrder(item *ent.PaymentOrder) apppayment.Order {
	return apppayment.Order{
		ID:            item.ID,
		OutTradeNo:    item.OutTradeNo,
		UserID:        item.UserID,
		Method:        item.Method,
		ProviderID:    item.ProviderID,
		Amount:        item.Amount,
		Status:        item.Status,
		Subject:       item.Subject,
		PaymentURL:    item.PaymentURL,
		QRCodeContent: item.QrCodeContent,
		PaidAt:        item.PaidAt,
		ExpiresAt:     item.ExpiresAt,
		CreatedAt:     item.CreatedAt,
		UpdatedAt:     item.UpdatedAt,
	}
}

// CreateOrder 落库新订单。
func (s *PaymentStore) CreateOrder(ctx context.Context, o apppayment.Order) (apppayment.Order, error) {
	item, err := s.db.PaymentOrder.Create().
		SetOutTradeNo(o.OutTradeNo).
		SetUserID(o.UserID).
		SetMethod(o.Method).
		SetProviderID(o.ProviderID).
		SetAmount(o.Amount).
		SetStatus(o.Status).
		SetSubject(o.Subject).
		SetClientIP(o.ClientIP).
		SetPaymentURL(o.PaymentURL).
		SetQrCodeContent(o.QRCodeContent).
		SetExpiresAt(o.ExpiresAt).
		Save(ctx)
	if err != nil {
		return apppayment.Order{}, err
	}
	return toPaymentOrder(item), nil
}

// GetOrder 按商户订单号查单。
func (s *PaymentStore) GetOrder(ctx context.Context, outTradeNo string) (apppayment.Order, error) {
	item, err := s.db.PaymentOrder.Query().
		Where(entpaymentorder.OutTradeNoEQ(outTradeNo)).
		Only(ctx)
	if err != nil {
		return apppayment.Order{}, err
	}
	return toPaymentOrder(item), nil
}

// ListUserOrders 用户充值记录（按创建时间倒序，分页），并返回总数。
func (s *PaymentStore) ListUserOrders(ctx context.Context, f apppayment.UserOrderFilter) ([]apppayment.Order, int64, error) {
	query := s.db.PaymentOrder.Query().
		Where(entpaymentorder.UserIDEQ(f.UserID))

	total, err := query.Clone().Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	items, err := query.
		Order(ent.Desc(entpaymentorder.FieldCreatedAt)).
		Offset((f.Page - 1) * f.PageSize).
		Limit(f.PageSize).
		All(ctx)
	if err != nil {
		return nil, 0, err
	}
	out := make([]apppayment.Order, 0, len(items))
	for _, item := range items {
		out = append(out, toPaymentOrder(item))
	}
	return out, int64(total), nil
}

// AdminListOrders 管理端订单列表（email 子串过滤 + 状态过滤 + 分页），
// 并批量回填用户邮箱展示。
func (s *PaymentStore) AdminListOrders(ctx context.Context, f apppayment.AdminOrderFilter) ([]apppayment.Order, int64, error) {
	query := s.db.PaymentOrder.Query()
	if f.Status != "" && f.Status != "all" {
		query = query.Where(entpaymentorder.StatusEQ(f.Status))
	}
	if f.Email != "" {
		userIDs, err := s.db.User.Query().
			Where(entuser.EmailContainsFold(f.Email)).
			IDs(ctx)
		if err != nil {
			return nil, 0, err
		}
		if len(userIDs) == 0 {
			return []apppayment.Order{}, 0, nil
		}
		query = query.Where(entpaymentorder.UserIDIn(userIDs...))
	}

	total, err := query.Clone().Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	items, err := query.
		Order(ent.Desc(entpaymentorder.FieldCreatedAt)).
		Offset((f.Page - 1) * f.PageSize).
		Limit(f.PageSize).
		All(ctx)
	if err != nil {
		return nil, 0, err
	}

	// 批量取用户邮箱（订单无 user 边：用户删除后订单留存）
	idSet := make(map[int]struct{}, len(items))
	for _, item := range items {
		idSet[item.UserID] = struct{}{}
	}
	ids := make([]int, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}
	emails := map[int]string{}
	if len(ids) > 0 {
		users, err := s.db.User.Query().Where(entuser.IDIn(ids...)).All(ctx)
		if err != nil {
			return nil, 0, err
		}
		for _, u := range users {
			emails[u.ID] = u.Email
		}
	}

	out := make([]apppayment.Order, 0, len(items))
	for _, item := range items {
		o := toPaymentOrder(item)
		o.UserEmail = emails[item.UserID]
		out = append(out, o)
	}
	return out, int64(total), nil
}

// OrderStats 订单状态计数 + 已支付金额（累计/今日）。
func (s *PaymentStore) OrderStats(ctx context.Context, todayStart time.Time) (apppayment.OrderStats, error) {
	var stats apppayment.OrderStats

	var countRows []struct {
		Status string `json:"status"`
		Count  int64  `json:"count"`
	}
	if err := s.db.PaymentOrder.Query().
		GroupBy(entpaymentorder.FieldStatus).
		Aggregate(ent.Count()).
		Scan(ctx, &countRows); err != nil {
		return stats, err
	}
	for _, row := range countRows {
		stats.Total += row.Count
		switch row.Status {
		case apppayment.StatusPaid:
			stats.Paid = row.Count
		case apppayment.StatusPending:
			stats.Pending = row.Count
		case apppayment.StatusExpired:
			stats.Expired = row.Count
		}
	}

	var sumRows []struct {
		Status string  `json:"status"`
		Sum    float64 `json:"sum"`
	}
	if err := s.db.PaymentOrder.Query().
		Where(entpaymentorder.StatusEQ(apppayment.StatusPaid)).
		GroupBy(entpaymentorder.FieldStatus).
		Aggregate(ent.As(ent.Sum(entpaymentorder.FieldAmount), "sum")).
		Scan(ctx, &sumRows); err != nil {
		return stats, err
	}
	for _, row := range sumRows {
		stats.TotalAmount += row.Sum
	}

	var todayRows []struct {
		Status string  `json:"status"`
		Sum    float64 `json:"sum"`
	}
	if err := s.db.PaymentOrder.Query().
		Where(
			entpaymentorder.StatusEQ(apppayment.StatusPaid),
			entpaymentorder.PaidAtGTE(todayStart),
		).
		GroupBy(entpaymentorder.FieldStatus).
		Aggregate(ent.As(ent.Sum(entpaymentorder.FieldAmount), "sum")).
		Scan(ctx, &todayRows); err != nil {
		return stats, err
	}
	for _, row := range todayRows {
		stats.TodayAmount += row.Sum
	}
	return stats, nil
}

// PaidAmountSince 用户自 since 起累计已支付金额（单日限额校验）。
func (s *PaymentStore) PaidAmountSince(ctx context.Context, userID int, since time.Time) (float64, error) {
	var rows []struct {
		UserID int     `json:"user_id"`
		Sum    float64 `json:"sum"`
	}
	if err := s.db.PaymentOrder.Query().
		Where(
			entpaymentorder.UserIDEQ(userID),
			entpaymentorder.StatusEQ(apppayment.StatusPaid),
			entpaymentorder.PaidAtGTE(since),
		).
		GroupBy(entpaymentorder.FieldUserID).
		Aggregate(ent.As(ent.Sum(entpaymentorder.FieldAmount), "sum")).
		Scan(ctx, &rows); err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	return rows[0].Sum, nil
}

// CreditPaidOrder 单事务入账：订单 pending→paid + 用户加余额 + 余额流水（幂等键兜底）。
//
// 幂等/防重四层：out_trade_no 唯一约束 → 条件更新 WHERE status='pending' →
// balance_log idempotency_key 唯一索引 → 已 paid 幂等短路。
func (s *PaymentStore) CreditPaidOrder(ctx context.Context, in apppayment.CreditInput) (bool, error) {
	tx, err := s.db.Tx(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()

	order, err := tx.PaymentOrder.Query().
		Where(entpaymentorder.OutTradeNoEQ(in.OutTradeNo)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return false, apppayment.ErrOrderNotFound
		}
		return false, err
	}
	if order.Status == apppayment.StatusPaid {
		return true, nil // 幂等：平台重发回调
	}
	if order.Status != apppayment.StatusPending {
		return false, fmt.Errorf("%w: %s", apppayment.ErrOrderStateConflict, order.Status)
	}
	// 防假回调：渠道告知金额与订单金额差超 1 分钱拒绝入账
	if math.Abs(order.Amount-in.Amount) > 0.01 {
		return false, fmt.Errorf("%w: 订单 %.2f / 回调 %.2f", apppayment.ErrAmountMismatch, order.Amount, in.Amount)
	}

	// 条件更新抢占：并发重复回调只有一个能改成功
	affected, err := tx.PaymentOrder.Update().
		Where(
			entpaymentorder.IDEQ(order.ID),
			entpaymentorder.StatusEQ(apppayment.StatusPending),
		).
		SetStatus(apppayment.StatusPaid).
		SetPaidAt(time.Now()).
		SetNotifyPayload(in.NotifyPayload).
		Save(ctx)
	if err != nil {
		return false, err
	}
	if affected == 0 {
		return true, nil // 另一条并发回调已入账
	}

	// 行锁重读余额（Postgres FOR UPDATE），保证流水 before/after 与真实变更一致；
	// 入账本身走增量 UPDATE，与计费侧 AddBalance(-cost) 并发时不会丢失更新。
	usr, err := tx.User.Query().
		Where(entuser.IDEQ(order.UserID)).
		Where(forUpdateLock).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			// 用户已被删除：订单标记支付但无处入账，回滚保持 pending 供人工处理
			return false, fmt.Errorf("订单归属用户 %d 不存在", order.UserID)
		}
		return false, err
	}
	before := usr.Balance
	after := before + order.Amount
	if _, err := tx.User.UpdateOneID(usr.ID).AddBalance(order.Amount).Save(ctx); err != nil {
		return false, err
	}

	if _, err := tx.BalanceLog.Create().
		SetAction(entbalancelog.ActionAdd).
		SetAmount(order.Amount).
		SetBeforeBalance(before).
		SetAfterBalance(after).
		SetRemark(in.Remark).
		SetUserIDSnapshot(usr.ID).
		SetUserEmailSnapshot(usr.Email).
		SetUserID(usr.ID).
		SetIdempotencyKey("epay:" + in.OutTradeNo).
		Save(ctx); err != nil {
		if ent.IsConstraintError(err) {
			return true, nil // 幂等键冲突：历史插件时代已入账过同一订单
		}
		return false, err
	}

	if err := tx.Commit(); err != nil {
		return false, err
	}
	return false, nil
}

// ExpirePendingOrders 过期清理。
func (s *PaymentStore) ExpirePendingOrders(ctx context.Context, now time.Time) (int, error) {
	return s.db.PaymentOrder.Update().
		Where(
			entpaymentorder.StatusEQ(apppayment.StatusPending),
			entpaymentorder.ExpiresAtLT(now),
		).
		SetStatus(apppayment.StatusExpired).
		Save(ctx)
}

// ===================== 服务商配置 =====================

func toProviderConfig(item *ent.PaymentProviderConfig) apppayment.ProviderConfig {
	return apppayment.ProviderConfig{
		ProviderKey: item.ProviderKey,
		Kind:        item.Kind,
		Enabled:     item.Enabled,
		Config:      item.Config,
		CreatedAt:   item.CreatedAt,
		UpdatedAt:   item.UpdatedAt,
	}
}

// ListProviderConfigs 全量服务商实例（按 key 字典序，保证 Pick 顺序稳定）。
func (s *PaymentStore) ListProviderConfigs(ctx context.Context) ([]apppayment.ProviderConfig, error) {
	items, err := s.db.PaymentProviderConfig.Query().
		Order(ent.Asc(entpaymentproviderconfig.FieldProviderKey)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]apppayment.ProviderConfig, 0, len(items))
	for _, item := range items {
		out = append(out, toProviderConfig(item))
	}
	return out, nil
}

// GetProviderConfig 按实例 key 查询。
func (s *PaymentStore) GetProviderConfig(ctx context.Context, providerKey string) (apppayment.ProviderConfig, error) {
	item, err := s.db.PaymentProviderConfig.Query().
		Where(entpaymentproviderconfig.ProviderKeyEQ(providerKey)).
		Only(ctx)
	if err != nil {
		return apppayment.ProviderConfig{}, err
	}
	return toProviderConfig(item), nil
}

// UpsertProviderConfig 新增或整体更新服务商实例配置。
func (s *PaymentStore) UpsertProviderConfig(ctx context.Context, c apppayment.ProviderConfig) error {
	existing, err := s.db.PaymentProviderConfig.Query().
		Where(entpaymentproviderconfig.ProviderKeyEQ(c.ProviderKey)).
		Only(ctx)
	if err != nil {
		if !ent.IsNotFound(err) {
			return err
		}
		_, err = s.db.PaymentProviderConfig.Create().
			SetProviderKey(c.ProviderKey).
			SetKind(c.Kind).
			SetEnabled(c.Enabled).
			SetConfig(c.Config).
			Save(ctx)
		return err
	}
	_, err = s.db.PaymentProviderConfig.UpdateOneID(existing.ID).
		SetKind(c.Kind).
		SetEnabled(c.Enabled).
		SetConfig(c.Config).
		Save(ctx)
	return err
}

// RenameProviderConfig 改实例 key，事务内同步更新历史订单 provider_id 引用。
func (s *PaymentStore) RenameProviderConfig(ctx context.Context, oldKey, newKey string) error {
	tx, err := s.db.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	item, err := tx.PaymentProviderConfig.Query().
		Where(entpaymentproviderconfig.ProviderKeyEQ(oldKey)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return apppayment.ErrProviderNotFound
		}
		return err
	}
	if _, err := tx.PaymentProviderConfig.UpdateOneID(item.ID).
		SetProviderKey(newKey).
		Save(ctx); err != nil {
		return err
	}
	if _, err := tx.PaymentOrder.Update().
		Where(entpaymentorder.ProviderIDEQ(oldKey)).
		SetProviderID(newKey).
		Save(ctx); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteProviderConfig 删除服务商实例（历史订单保留 provider_id 字符串引用）。
func (s *PaymentStore) DeleteProviderConfig(ctx context.Context, providerKey string) error {
	n, err := s.db.PaymentProviderConfig.Delete().
		Where(entpaymentproviderconfig.ProviderKeyEQ(providerKey)).
		Exec(ctx)
	if err != nil {
		return err
	}
	if n == 0 {
		return apppayment.ErrProviderNotFound
	}
	return nil
}

// NextProviderKeyForKind 生成 {kind}_{N} 自增实例 key（N 取现存最大序号 + 1）。
func (s *PaymentStore) NextProviderKeyForKind(ctx context.Context, kind string) (string, error) {
	items, err := s.db.PaymentProviderConfig.Query().
		Where(entpaymentproviderconfig.KindEQ(kind)).
		All(ctx)
	if err != nil {
		return "", err
	}
	max := 0
	prefix := kind + "_"
	for _, item := range items {
		if !strings.HasPrefix(item.ProviderKey, prefix) {
			continue
		}
		if n, err := strconv.Atoi(strings.TrimPrefix(item.ProviderKey, prefix)); err == nil && n > max {
			max = n
		}
	}
	return prefix + strconv.Itoa(max+1), nil
}
