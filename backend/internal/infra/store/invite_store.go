package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/DouDOU-start/airgate-core/ent"
	entbalancelog "github.com/DouDOU-start/airgate-core/ent/balancelog"
	entinviteprofile "github.com/DouDOU-start/airgate-core/ent/inviteprofile"
	entinviterebatelog "github.com/DouDOU-start/airgate-core/ent/inviterebatelog"
	entuser "github.com/DouDOU-start/airgate-core/ent/user"
	appinvite "github.com/DouDOU-start/airgate-core/internal/app/invite"
)

// InviteStore 邀请返利域持久化实现。
type InviteStore struct {
	db *ent.Client
}

// NewInviteStore 创建邀请返利存储。
func NewInviteStore(db *ent.Client) *InviteStore {
	return &InviteStore{db: db}
}

func toInviteProfile(item *ent.InviteProfile) appinvite.Profile {
	return appinvite.Profile{
		UserID:             item.UserID,
		InviteCode:         item.InviteCode,
		InviterID:          item.InviterID,
		RebateRateOverride: item.RebateRateOverride,
		InvitedCount:       item.InvitedCount,
		RebateBalance:      item.RebateBalance,
		RebateTotal:        item.RebateTotal,
		CreatedAt:          item.CreatedAt,
		UpdatedAt:          item.UpdatedAt,
	}
}

// randomInviteCode 生成 10 位大写 hex 邀请码（40-bit 熵，冲突概率可忽略）。
func randomInviteCode() (string, error) {
	b := make([]byte, 5)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return strings.ToUpper(hex.EncodeToString(b)), nil
}

// GetProfile 查询画像，不存在返回 ErrProfileNotFound（不自动创建）。
func (s *InviteStore) GetProfile(ctx context.Context, userID int) (appinvite.Profile, error) {
	item, err := s.db.InviteProfile.Query().Where(entinviteprofile.UserIDEQ(userID)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return appinvite.Profile{}, appinvite.ErrProfileNotFound
		}
		return appinvite.Profile{}, err
	}
	return toInviteProfile(item), nil
}

// GetByCode 按邀请码查画像，不存在返回 ErrProfileNotFound。
func (s *InviteStore) GetByCode(ctx context.Context, code string) (appinvite.Profile, error) {
	item, err := s.db.InviteProfile.Query().Where(entinviteprofile.InviteCodeEQ(code)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return appinvite.Profile{}, appinvite.ErrProfileNotFound
		}
		return appinvite.Profile{}, err
	}
	return toInviteProfile(item), nil
}

// EnsureProfile get-or-create：不存在则生成随机邀请码并创建，
// invite_code 唯一约束冲突时重试新码；user_id 唯一约束冲突（并发创建）时直接查回。
func (s *InviteStore) EnsureProfile(ctx context.Context, userID int) (appinvite.Profile, error) {
	if p, err := s.GetProfile(ctx, userID); err == nil {
		return p, nil
	} else if err != appinvite.ErrProfileNotFound {
		return appinvite.Profile{}, err
	}

	for attempt := 0; attempt < 5; attempt++ {
		code, err := randomInviteCode()
		if err != nil {
			return appinvite.Profile{}, err
		}
		item, err := s.db.InviteProfile.Create().
			SetUserID(userID).
			SetInviteCode(code).
			Save(ctx)
		if err == nil {
			return toInviteProfile(item), nil
		}
		if ent.IsConstraintError(err) {
			if p, gerr := s.GetProfile(ctx, userID); gerr == nil {
				return p, nil // 并发创建：另一路已建好，直接返回
			}
			continue // invite_code 冲突：换码重试
		}
		return appinvite.Profile{}, err
	}
	return appinvite.Profile{}, fmt.Errorf("ensure invite profile: 邀请码生成重试次数超限")
}

// BindInviter 条件更新 WHERE inviter_id IS NULL 抢占绑定，成功后邀请人 invited_count+1。
func (s *InviteStore) BindInviter(ctx context.Context, userID, inviterID int) (bool, error) {
	tx, err := s.db.Tx(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()

	affected, err := tx.InviteProfile.Update().
		Where(entinviteprofile.UserIDEQ(userID), entinviteprofile.InviterIDIsNil()).
		SetInviterID(inviterID).
		Save(ctx)
	if err != nil {
		return false, err
	}
	if affected == 0 {
		return false, nil // 已绑定过
	}

	if _, err := tx.InviteProfile.Update().
		Where(entinviteprofile.UserIDEQ(inviterID)).
		AddInvitedCount(1).
		Save(ctx); err != nil {
		return false, err
	}

	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// AccrueRebate 单事务：邀请人 rebate_balance/rebate_total 累加 + 写 accrue 流水。
// 幂等键唯一索引冲突视为该笔已计提过，返回 applied=false（非错误）。
func (s *InviteStore) AccrueRebate(ctx context.Context, inviterID, sourceUserID int, amount float64, sourceOrderNo, idempotencyKey string) (bool, error) {
	if amount <= 0 {
		return false, nil
	}
	tx, err := s.db.Tx(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()

	affected, err := tx.InviteProfile.Update().
		Where(entinviteprofile.UserIDEQ(inviterID)).
		AddRebateBalance(amount).
		AddRebateTotal(amount).
		Save(ctx)
	if err != nil {
		return false, err
	}
	if affected == 0 {
		return false, nil // 邀请人画像不存在（防御性处理，理论不应发生）
	}

	logCreate := tx.InviteRebateLog.Create().
		SetUserID(inviterID).
		SetAction(entinviterebatelog.ActionAccrue).
		SetAmount(amount).
		SetSourceUserID(sourceUserID)
	if sourceOrderNo != "" {
		logCreate = logCreate.SetSourceOrderNo(sourceOrderNo)
	}
	if idempotencyKey != "" {
		logCreate = logCreate.SetIdempotencyKey(idempotencyKey)
	}
	if _, err := logCreate.Save(ctx); err != nil {
		if ent.IsConstraintError(err) {
			return false, nil // 幂等键冲突：该笔已计提过
		}
		return false, err
	}

	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// TransferToBalance 单事务：行锁读 rebate_balance → 清零 → 用户加余额 + BalanceLog + transfer 流水。
// 行锁 + 清零后置零校验天然防并发重复转入，无需额外幂等键。
func (s *InviteStore) TransferToBalance(ctx context.Context, userID int) (float64, float64, error) {
	tx, err := s.db.Tx(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = tx.Rollback() }()

	profile, err := tx.InviteProfile.Query().
		Where(entinviteprofile.UserIDEQ(userID)).
		Where(forUpdateLock).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return 0, 0, appinvite.ErrProfileNotFound
		}
		return 0, 0, err
	}
	if profile.RebateBalance <= 0 {
		return 0, 0, appinvite.ErrRebateBalanceEmpty
	}
	transferAmount := profile.RebateBalance

	if _, err := tx.InviteProfile.UpdateOneID(profile.ID).
		SetRebateBalance(0).
		Save(ctx); err != nil {
		return 0, 0, err
	}

	usr, err := tx.User.Query().
		Where(entuser.IDEQ(userID)).
		Where(forUpdateLock).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return 0, 0, appinvite.ErrUserNotFound
		}
		return 0, 0, err
	}
	before := usr.Balance
	after := before + transferAmount
	if _, err := tx.User.UpdateOneID(usr.ID).AddBalance(transferAmount).Save(ctx); err != nil {
		return 0, 0, err
	}

	if _, err := tx.BalanceLog.Create().
		SetAction(entbalancelog.ActionAdd).
		SetAmount(transferAmount).
		SetBeforeBalance(before).
		SetAfterBalance(after).
		SetRemark("邀请返利转入余额").
		SetUserIDSnapshot(usr.ID).
		SetUserEmailSnapshot(usr.Email).
		SetUserID(usr.ID).
		Save(ctx); err != nil {
		return 0, 0, err
	}

	if _, err := tx.InviteRebateLog.Create().
		SetUserID(userID).
		SetAction(entinviterebatelog.ActionTransfer).
		SetAmount(transferAmount).
		SetBalanceAfter(after).
		Save(ctx); err != nil {
		return 0, 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	return transferAmount, after, nil
}

// SetRateOverride 设置/清除用户专属返利比例（先 EnsureProfile，兼容管理员对从未
// 访问过邀请页的用户设置专属比例）。
func (s *InviteStore) SetRateOverride(ctx context.Context, userID int, ratePercent *float64) error {
	if _, err := s.EnsureProfile(ctx, userID); err != nil {
		return err
	}
	upd := s.db.InviteProfile.Update().Where(entinviteprofile.UserIDEQ(userID))
	if ratePercent == nil {
		upd = upd.ClearRebateRateOverride()
	} else {
		upd = upd.SetRebateRateOverride(*ratePercent)
	}
	_, err := upd.Save(ctx)
	return err
}

// batchUsers 按 ID 批量查用户（邮箱/用户名展示用），无边可查故单独取。
func (s *InviteStore) batchUsers(ctx context.Context, ids []int) (map[int]*ent.User, error) {
	out := make(map[int]*ent.User, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	users, err := s.db.User.Query().Where(entuser.IDIn(ids...)).All(ctx)
	if err != nil {
		return nil, err
	}
	for _, u := range users {
		out[u.ID] = u
	}
	return out, nil
}

// accruedRebateSum 某邀请人从某下线处累计计提的返利总额（含已转入余额部分）。
func (s *InviteStore) accruedRebateSum(ctx context.Context, inviterID, inviteeID int) (float64, error) {
	var rows []struct {
		Sum float64 `json:"sum"`
	}
	err := s.db.InviteRebateLog.Query().
		Where(
			entinviterebatelog.ActionEQ(entinviterebatelog.ActionAccrue),
			entinviterebatelog.UserIDEQ(inviterID),
			entinviterebatelog.SourceUserIDEQ(inviteeID),
		).
		Aggregate(ent.As(ent.Sum(entinviterebatelog.FieldAmount), "sum")).
		Scan(ctx, &rows)
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	return rows[0].Sum, nil
}

// ListInvitees 用户端：inviterID 邀请的人（分页，按最近绑定/更新时间倒序）。
func (s *InviteStore) ListInvitees(ctx context.Context, inviterID int, f appinvite.ListFilter) ([]appinvite.InviteRelation, int64, error) {
	query := s.db.InviteProfile.Query().Where(entinviteprofile.InviterIDEQ(inviterID))
	total, err := query.Clone().Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	items, err := query.
		Order(ent.Desc(entinviteprofile.FieldUpdatedAt), ent.Desc(entinviteprofile.FieldID)).
		Offset((f.Page - 1) * f.PageSize).
		Limit(f.PageSize).
		All(ctx)
	if err != nil {
		return nil, 0, err
	}

	var inviterEmail, inviterUsername string
	if inviter, err := s.db.User.Query().Where(entuser.IDEQ(inviterID)).Only(ctx); err == nil {
		inviterEmail, inviterUsername = inviter.Email, inviter.Username
	}

	inviteeIDs := make([]int, 0, len(items))
	for _, item := range items {
		inviteeIDs = append(inviteeIDs, item.UserID)
	}
	users, err := s.batchUsers(ctx, inviteeIDs)
	if err != nil {
		return nil, 0, err
	}

	result := make([]appinvite.InviteRelation, 0, len(items))
	for _, item := range items {
		rebate, err := s.accruedRebateSum(ctx, inviterID, item.UserID)
		if err != nil {
			return nil, 0, err
		}
		rel := appinvite.InviteRelation{
			InviterID:       inviterID,
			InviterEmail:    inviterEmail,
			InviterUsername: inviterUsername,
			InviteeID:       item.UserID,
			CreatedAt:       item.UpdatedAt,
			TotalRebate:     rebate,
		}
		if u := users[item.UserID]; u != nil {
			rel.InviteeEmail, rel.InviteeUsername = u.Email, u.Username
		}
		result = append(result, rel)
	}
	return result, int64(total), nil
}

// AdminListInvitees 管理端：全量邀请关系（分页，keyword 匹配邀请人/被邀请人邮箱或用户名）。
func (s *InviteStore) AdminListInvitees(ctx context.Context, f appinvite.ListFilter) ([]appinvite.InviteRelation, int64, error) {
	query := s.db.InviteProfile.Query().Where(entinviteprofile.InviterIDNotNil())
	if f.Keyword != "" {
		userIDs, err := s.db.User.Query().
			Where(entuser.Or(entuser.EmailContainsFold(f.Keyword), entuser.UsernameContainsFold(f.Keyword))).
			IDs(ctx)
		if err != nil {
			return nil, 0, err
		}
		if len(userIDs) == 0 {
			return []appinvite.InviteRelation{}, 0, nil
		}
		query = query.Where(entinviteprofile.Or(
			entinviteprofile.UserIDIn(userIDs...),
			entinviteprofile.InviterIDIn(userIDs...),
		))
	}

	total, err := query.Clone().Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	items, err := query.
		Order(ent.Desc(entinviteprofile.FieldUpdatedAt), ent.Desc(entinviteprofile.FieldID)).
		Offset((f.Page - 1) * f.PageSize).
		Limit(f.PageSize).
		All(ctx)
	if err != nil {
		return nil, 0, err
	}

	idSet := make(map[int]struct{}, len(items)*2)
	for _, item := range items {
		idSet[item.UserID] = struct{}{}
		if item.InviterID != nil {
			idSet[*item.InviterID] = struct{}{}
		}
	}
	ids := make([]int, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}
	users, err := s.batchUsers(ctx, ids)
	if err != nil {
		return nil, 0, err
	}

	result := make([]appinvite.InviteRelation, 0, len(items))
	for _, item := range items {
		if item.InviterID == nil {
			continue
		}
		rebate, err := s.accruedRebateSum(ctx, *item.InviterID, item.UserID)
		if err != nil {
			return nil, 0, err
		}
		rel := appinvite.InviteRelation{
			InviterID:   *item.InviterID,
			InviteeID:   item.UserID,
			CreatedAt:   item.UpdatedAt,
			TotalRebate: rebate,
		}
		if u := users[item.UserID]; u != nil {
			rel.InviteeEmail, rel.InviteeUsername = u.Email, u.Username
		}
		if u := users[*item.InviterID]; u != nil {
			rel.InviterEmail, rel.InviterUsername = u.Email, u.Username
		}
		result = append(result, rel)
	}
	return result, int64(total), nil
}

// ListRebateLogs 用户端：userID 自己的流水（分页倒序）。
func (s *InviteStore) ListRebateLogs(ctx context.Context, userID int, f appinvite.ListFilter) ([]appinvite.RebateLog, int64, error) {
	return s.listRebateLogs(ctx, s.db.InviteRebateLog.Query().Where(entinviterebatelog.UserIDEQ(userID)), f)
}

// AdminListRebateLogs 管理端：全量流水（分页倒序，keyword 匹配归属人邮箱/用户名）。
func (s *InviteStore) AdminListRebateLogs(ctx context.Context, f appinvite.ListFilter) ([]appinvite.RebateLog, int64, error) {
	query := s.db.InviteRebateLog.Query()
	if f.Keyword != "" {
		userIDs, err := s.db.User.Query().
			Where(entuser.Or(entuser.EmailContainsFold(f.Keyword), entuser.UsernameContainsFold(f.Keyword))).
			IDs(ctx)
		if err != nil {
			return nil, 0, err
		}
		if len(userIDs) == 0 {
			return []appinvite.RebateLog{}, 0, nil
		}
		query = query.Where(entinviterebatelog.UserIDIn(userIDs...))
	}
	return s.listRebateLogs(ctx, query, f)
}

func (s *InviteStore) listRebateLogs(ctx context.Context, query *ent.InviteRebateLogQuery, f appinvite.ListFilter) ([]appinvite.RebateLog, int64, error) {
	total, err := query.Clone().Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	items, err := query.
		Order(ent.Desc(entinviterebatelog.FieldCreatedAt), ent.Desc(entinviterebatelog.FieldID)).
		Offset((f.Page - 1) * f.PageSize).
		Limit(f.PageSize).
		All(ctx)
	if err != nil {
		return nil, 0, err
	}

	idSet := make(map[int]struct{}, len(items)*2)
	for _, item := range items {
		idSet[item.UserID] = struct{}{}
		if item.SourceUserID != nil {
			idSet[*item.SourceUserID] = struct{}{}
		}
	}
	ids := make([]int, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}
	users, err := s.batchUsers(ctx, ids)
	if err != nil {
		return nil, 0, err
	}

	result := make([]appinvite.RebateLog, 0, len(items))
	for _, item := range items {
		rl := appinvite.RebateLog{
			ID:            item.ID,
			UserID:        item.UserID,
			Action:        item.Action.String(),
			Amount:        item.Amount,
			SourceUserID:  item.SourceUserID,
			SourceOrderNo: item.SourceOrderNo,
			BalanceAfter:  item.BalanceAfter,
			CreatedAt:     item.CreatedAt,
		}
		if u := users[item.UserID]; u != nil {
			rl.UserEmail = u.Email
		}
		if item.SourceUserID != nil {
			if u := users[*item.SourceUserID]; u != nil {
				rl.SourceUserEmail = u.Email
			}
		}
		result = append(result, rl)
	}
	return result, int64(total), nil
}

// AdminListOverrides 管理端：有专属比例覆盖的用户列表（分页）。
func (s *InviteStore) AdminListOverrides(ctx context.Context, f appinvite.ListFilter) ([]appinvite.OverrideEntry, int64, error) {
	query := s.db.InviteProfile.Query().Where(entinviteprofile.RebateRateOverrideNotNil())
	if f.Keyword != "" {
		userIDs, err := s.db.User.Query().
			Where(entuser.Or(entuser.EmailContainsFold(f.Keyword), entuser.UsernameContainsFold(f.Keyword))).
			IDs(ctx)
		if err != nil {
			return nil, 0, err
		}
		if len(userIDs) == 0 {
			return []appinvite.OverrideEntry{}, 0, nil
		}
		query = query.Where(entinviteprofile.UserIDIn(userIDs...))
	}

	total, err := query.Clone().Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	items, err := query.
		Order(ent.Desc(entinviteprofile.FieldUpdatedAt)).
		Offset((f.Page - 1) * f.PageSize).
		Limit(f.PageSize).
		All(ctx)
	if err != nil {
		return nil, 0, err
	}

	ids := make([]int, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.UserID)
	}
	users, err := s.batchUsers(ctx, ids)
	if err != nil {
		return nil, 0, err
	}

	result := make([]appinvite.OverrideEntry, 0, len(items))
	for _, item := range items {
		entry := appinvite.OverrideEntry{
			UserID:             item.UserID,
			InviteCode:         item.InviteCode,
			RebateRateOverride: item.RebateRateOverride,
			InvitedCount:       item.InvitedCount,
		}
		if u := users[item.UserID]; u != nil {
			entry.Email, entry.Username = u.Email, u.Username
		}
		result = append(result, entry)
	}
	return result, int64(total), nil
}

var _ appinvite.Repository = (*InviteStore)(nil)
