package store

import (
	"context"
	"math"

	"github.com/DouDOU-start/airgate-core/ent"
	entbalancelog "github.com/DouDOU-start/airgate-core/ent/balancelog"
	entuser "github.com/DouDOU-start/airgate-core/ent/user"
	appwallet "github.com/DouDOU-start/airgate-core/internal/app/wallet"
	"github.com/DouDOU-start/airgate-core/internal/pkg/money"
)

// WalletStore 实现 OAuth 钱包的原子扣款、退款和总账写入。
type WalletStore struct {
	db *ent.Client
}

func NewWalletStore(db *ent.Client) *WalletStore { return &WalletStore{db: db} }

// Debit 对用户行加锁后扣款，并在同一事务写入可审计的 BalanceLog。
func (s *WalletStore) Debit(ctx context.Context, change appwallet.Change) (appwallet.Transaction, error) {
	tx, err := s.db.Tx(ctx)
	if err != nil {
		return appwallet.Transaction{}, err
	}
	defer func() { _ = tx.Rollback() }()

	if existing, ok, err := findWalletIdempotency(ctx, tx, change); err != nil {
		return appwallet.Transaction{}, err
	} else if ok {
		return existing, nil
	}
	usr, err := tx.User.Query().Where(entuser.IDEQ(change.UserID)).Where(forUpdateLock).Only(ctx)
	if err != nil {
		return appwallet.Transaction{}, err
	}
	// 获取用户行锁后再查一次，覆盖同一用户并发请求在首次查询后的提交窗口。
	if existing, ok, err := findWalletIdempotency(ctx, tx, change); err != nil {
		return appwallet.Transaction{}, err
	} else if ok {
		return existing, nil
	}
	amount := money.UnitsToFloat(change.AmountUnits)
	if money.FloatToUnits(usr.Balance) < change.AmountUnits {
		return appwallet.Transaction{}, appwallet.ErrInsufficientBalance
	}
	after := usr.Balance - amount
	if _, err := tx.User.UpdateOneID(usr.ID).AddBalance(-amount).Save(ctx); err != nil {
		return appwallet.Transaction{}, err
	}
	log, err := createWalletLog(ctx, tx, usr, change, entbalancelog.ActionSubtract, usr.Balance, after, 0)
	if err != nil {
		return appwallet.Transaction{}, err
	}
	if err := tx.Commit(); err != nil {
		return appwallet.Transaction{}, err
	}
	return mapWalletTransaction(log, false), nil
}

// Refund 锁定原扣款流水，校验应用、用户和累计退款额度后原子入账。
func (s *WalletStore) Refund(ctx context.Context, change appwallet.Change) (appwallet.Transaction, error) {
	tx, err := s.db.Tx(ctx)
	if err != nil {
		return appwallet.Transaction{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if existing, ok, err := findWalletIdempotency(ctx, tx, change); err != nil {
		return appwallet.Transaction{}, err
	} else if ok {
		return existing, nil
	}
	original, err := tx.BalanceLog.Query().
		Where(
			entbalancelog.TransactionIDEQ(change.RelatedTransactionID),
			entbalancelog.SourceEQ(appwallet.SourceOAuthWallet),
			entbalancelog.OauthClientIDEQ(change.ClientID),
			entbalancelog.UserIDSnapshotEQ(change.UserID),
			entbalancelog.ActionEQ(entbalancelog.ActionSubtract),
		).
		Where(forUpdateLock).
		Only(ctx)
	if ent.IsNotFound(err) {
		return appwallet.Transaction{}, appwallet.ErrTransactionNotFound
	}
	if err != nil {
		return appwallet.Transaction{}, err
	}
	refunds, err := tx.BalanceLog.Query().
		Where(
			entbalancelog.RelatedLogIDEQ(original.ID),
			entbalancelog.SourceEQ(appwallet.SourceOAuthWallet),
			entbalancelog.ActionEQ(entbalancelog.ActionAdd),
		).
		All(ctx)
	if err != nil {
		return appwallet.Transaction{}, err
	}
	refundedUnits := int64(0)
	for _, item := range refunds {
		refundedUnits += money.FloatToUnits(item.Amount)
	}
	if refundedUnits+change.AmountUnits > money.FloatToUnits(original.Amount) {
		return appwallet.Transaction{}, appwallet.ErrRefundExceeded
	}
	usr, err := tx.User.Query().Where(entuser.IDEQ(change.UserID)).Where(forUpdateLock).Only(ctx)
	if err != nil {
		return appwallet.Transaction{}, err
	}
	amount := money.UnitsToFloat(change.AmountUnits)
	after := usr.Balance + amount
	if _, err := tx.User.UpdateOneID(usr.ID).AddBalance(amount).Save(ctx); err != nil {
		return appwallet.Transaction{}, err
	}
	log, err := createWalletLog(ctx, tx, usr, change, entbalancelog.ActionAdd, usr.Balance, after, original.ID)
	if err != nil {
		return appwallet.Transaction{}, err
	}
	if err := tx.Commit(); err != nil {
		return appwallet.Transaction{}, err
	}
	return mapWalletTransaction(log, false), nil
}

func findWalletIdempotency(ctx context.Context, tx *ent.Tx, change appwallet.Change) (appwallet.Transaction, bool, error) {
	item, err := tx.BalanceLog.Query().Where(entbalancelog.IdempotencyKeyEQ(change.IdempotencyKey)).Only(ctx)
	if ent.IsNotFound(err) {
		return appwallet.Transaction{}, false, nil
	}
	if err != nil {
		return appwallet.Transaction{}, false, err
	}
	wantAction := entbalancelog.ActionSubtract
	if change.Kind == "refund" {
		wantAction = entbalancelog.ActionAdd
	}
	if item.Source != appwallet.SourceOAuthWallet || item.OauthClientID != change.ClientID ||
		item.ExternalOrderNo != change.ExternalOrderNo || item.UserIDSnapshot != change.UserID ||
		item.Action != wantAction || money.FloatToUnits(item.Amount) != change.AmountUnits {
		return appwallet.Transaction{}, false, appwallet.ErrIdempotencyConflict
	}
	if change.Kind == "refund" {
		related, err := tx.BalanceLog.Query().Where(entbalancelog.IDEQ(item.RelatedLogID)).Only(ctx)
		if err != nil || related.TransactionID == nil || *related.TransactionID != change.RelatedTransactionID {
			return appwallet.Transaction{}, false, appwallet.ErrIdempotencyConflict
		}
	}
	return mapWalletTransaction(item, true), true, nil
}

func createWalletLog(ctx context.Context, tx *ent.Tx, usr *ent.User, change appwallet.Change,
	action entbalancelog.Action, before, after float64, relatedLogID int) (*ent.BalanceLog, error) {
	return tx.BalanceLog.Create().
		SetAction(action).
		SetAmount(money.UnitsToFloat(change.AmountUnits)).
		SetBeforeBalance(before).
		SetAfterBalance(after).
		SetRemark(change.Subject).
		SetUserIDSnapshot(usr.ID).
		SetUserEmailSnapshot(usr.Email).
		SetIdempotencyKey(change.IdempotencyKey).
		SetTransactionID(change.TransactionID).
		SetSource(appwallet.SourceOAuthWallet).
		SetOauthClientID(change.ClientID).
		SetExternalOrderNo(change.ExternalOrderNo).
		SetRelatedLogID(relatedLogID).
		SetUserID(usr.ID).
		Save(ctx)
}

func mapWalletTransaction(item *ent.BalanceLog, idempotent bool) appwallet.Transaction {
	txID := ""
	if item.TransactionID != nil {
		txID = *item.TransactionID
	}
	return appwallet.Transaction{
		LogID:         item.ID,
		TransactionID: txID,
		AmountUnits:   money.FloatToUnits(item.Amount),
		BeforeBalance: normalizeBalance(item.BeforeBalance),
		AfterBalance:  normalizeBalance(item.AfterBalance),
		Idempotent:    idempotent,
	}
}

func normalizeBalance(value float64) float64 {
	return math.Round(value*float64(money.Scale)) / float64(money.Scale)
}

var _ appwallet.Repository = (*WalletStore)(nil)
