package wallet

import (
	"context"
	"testing"
)

type captureRepo struct{ last Change }

func (r *captureRepo) Debit(_ context.Context, change Change) (Transaction, error) {
	r.last = change
	return Transaction{TransactionID: change.TransactionID, AmountUnits: change.AmountUnits}, nil
}
func (r *captureRepo) Refund(_ context.Context, change Change) (Transaction, error) {
	r.last = change
	return Transaction{TransactionID: change.TransactionID, AmountUnits: change.AmountUnits}, nil
}

func TestServiceBuildsStableDebitIdempotency(t *testing.T) {
	repo := &captureRepo{}
	svc := NewService(repo)
	if _, err := svc.Debit(context.Background(), 7, "ac_market", "MK001", "1.25", "商品"); err != nil {
		t.Fatalf("Debit: %v", err)
	}
	if repo.last.IdempotencyKey != "oauth-wallet:ac_market:debit:MK001" || repo.last.AmountUnits != 125_000_000 {
		t.Fatalf("change = %+v", repo.last)
	}
}

func TestServiceRejectsInvalidAmount(t *testing.T) {
	svc := NewService(&captureRepo{})
	for _, amount := range []string{"0", "-1", "1.000000001"} {
		if _, err := svc.Debit(context.Background(), 1, "ac", "order", amount, ""); err != ErrInvalidAmount {
			t.Fatalf("amount %q error = %v", amount, err)
		}
	}
}
