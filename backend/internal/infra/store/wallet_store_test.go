package store

import (
	"context"
	"errors"
	"testing"

	appwallet "github.com/DouDOU-start/airgate-core/internal/app/wallet"
	"github.com/DouDOU-start/airgate-core/internal/pkg/money"
)

func TestWalletStoreDebitIdempotencyAndRefund(t *testing.T) {
	db := enttestOpen(t)
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	user := createTestUserWithBalance(t, db, "wallet@example.com", 20)
	s := NewWalletStore(db)

	debitChange := appwallet.Change{
		Kind:            "debit",
		UserID:          user.ID,
		ClientID:        "ac_market",
		ExternalOrderNo: "MK001",
		AmountUnits:     5 * money.Scale,
		Subject:         "购买商品",
		TransactionID:   "wtx_debit",
		IdempotencyKey:  "oauth-wallet:ac_market:debit:MK001",
	}
	debit, err := s.Debit(ctx, debitChange)
	if err != nil {
		t.Fatalf("Debit: %v", err)
	}
	if debit.Idempotent || money.FloatToUnits(debit.AfterBalance) != 15*money.Scale {
		t.Fatalf("Debit result = %+v", debit)
	}
	duplicate, err := s.Debit(ctx, debitChange)
	if err != nil || !duplicate.Idempotent || duplicate.TransactionID != debit.TransactionID {
		t.Fatalf("duplicate Debit = %+v, %v", duplicate, err)
	}
	if got := money.FloatToUnits(userBalance(t, db, user.ID)); got != 15*money.Scale {
		t.Fatalf("重复扣款后余额 = %d", got)
	}

	refundChange := appwallet.Change{
		Kind:                 "refund",
		UserID:               user.ID,
		ClientID:             "ac_market",
		ExternalOrderNo:      "RF001",
		AmountUnits:          2 * money.Scale,
		Subject:              "补偿退款",
		TransactionID:        "wtx_refund",
		RelatedTransactionID: debit.TransactionID,
		IdempotencyKey:       "oauth-wallet:ac_market:refund:RF001",
	}
	refund, err := s.Refund(ctx, refundChange)
	if err != nil {
		t.Fatalf("Refund: %v", err)
	}
	if money.FloatToUnits(refund.AfterBalance) != 17*money.Scale {
		t.Fatalf("Refund result = %+v", refund)
	}
	duplicateRefund, err := s.Refund(ctx, refundChange)
	if err != nil || !duplicateRefund.Idempotent {
		t.Fatalf("duplicate Refund = %+v, %v", duplicateRefund, err)
	}
	if got := money.FloatToUnits(userBalance(t, db, user.ID)); got != 17*money.Scale {
		t.Fatalf("重复退款后余额 = %d", got)
	}
}

func TestWalletStoreRejectsInvalidMoneyFlow(t *testing.T) {
	db := enttestOpen(t)
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	user := createTestUserWithBalance(t, db, "wallet-reject@example.com", 3)
	s := NewWalletStore(db)

	change := appwallet.Change{
		Kind: "debit", UserID: user.ID, ClientID: "ac_market", ExternalOrderNo: "MK002",
		AmountUnits: 4 * money.Scale, TransactionID: "wtx_too_much", IdempotencyKey: "debit-too-much",
	}
	if _, err := s.Debit(ctx, change); !errors.Is(err, appwallet.ErrInsufficientBalance) {
		t.Fatalf("余额不足错误 = %v", err)
	}

	change.AmountUnits = 2 * money.Scale
	change.TransactionID = "wtx_original"
	change.IdempotencyKey = "debit-original"
	debit, err := s.Debit(ctx, change)
	if err != nil {
		t.Fatalf("原扣款失败: %v", err)
	}
	refund := appwallet.Change{
		Kind: "refund", UserID: user.ID, ClientID: "ac_market", ExternalOrderNo: "RF002",
		AmountUnits: 3 * money.Scale, TransactionID: "wtx_over_refund",
		RelatedTransactionID: debit.TransactionID, IdempotencyKey: "refund-too-much",
	}
	if _, err := s.Refund(ctx, refund); !errors.Is(err, appwallet.ErrRefundExceeded) {
		t.Fatalf("超额退款错误 = %v", err)
	}
}

func TestWalletStoreRejectsIdempotencyParameterChange(t *testing.T) {
	db := enttestOpen(t)
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	user := createTestUserWithBalance(t, db, "wallet-conflict@example.com", 10)
	s := NewWalletStore(db)
	change := appwallet.Change{
		Kind: "debit", UserID: user.ID, ClientID: "ac_market", ExternalOrderNo: "MK003",
		AmountUnits: money.Scale, TransactionID: "wtx_one", IdempotencyKey: "same-key",
	}
	if _, err := s.Debit(ctx, change); err != nil {
		t.Fatalf("首次扣款: %v", err)
	}
	change.AmountUnits = 2 * money.Scale
	if _, err := s.Debit(ctx, change); !errors.Is(err, appwallet.ErrIdempotencyConflict) {
		t.Fatalf("参数冲突错误 = %v", err)
	}
}
