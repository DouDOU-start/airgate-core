package wallet

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/DouDOU-start/airgate-core/internal/pkg/money"
)

const transactionPrefix = "wtx_"

// Service 编排 OAuth 钱包扣款与退款。
type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service { return &Service{repo: repo} }

// Debit 按 client + 外部订单号幂等扣款。
func (s *Service) Debit(ctx context.Context, userID int, clientID, externalOrderNo, amount, subject string) (Transaction, error) {
	units, err := parsePositiveAmount(amount)
	if err != nil {
		return Transaction{}, err
	}
	externalOrderNo = strings.TrimSpace(externalOrderNo)
	if externalOrderNo == "" || len(externalOrderNo) > 128 {
		return Transaction{}, ErrIdempotencyConflict
	}
	txID, err := randomTransactionID()
	if err != nil {
		return Transaction{}, err
	}
	return s.repo.Debit(ctx, Change{
		Kind:            "debit",
		UserID:          userID,
		ClientID:        clientID,
		ExternalOrderNo: externalOrderNo,
		AmountUnits:     units,
		Subject:         truncate(strings.TrimSpace(subject), 500),
		TransactionID:   txID,
		IdempotencyKey:  fmt.Sprintf("oauth-wallet:%s:debit:%s", clientID, externalOrderNo),
	})
}

// Refund 关联原扣款幂等退款，仓储层保证累计退款不超过原交易金额。
func (s *Service) Refund(ctx context.Context, userID int, clientID, externalRefundNo, relatedTransactionID, amount, reason string) (Transaction, error) {
	units, err := parsePositiveAmount(amount)
	if err != nil {
		return Transaction{}, err
	}
	externalRefundNo = strings.TrimSpace(externalRefundNo)
	relatedTransactionID = strings.TrimSpace(relatedTransactionID)
	if externalRefundNo == "" || len(externalRefundNo) > 128 || relatedTransactionID == "" {
		return Transaction{}, ErrIdempotencyConflict
	}
	txID, err := randomTransactionID()
	if err != nil {
		return Transaction{}, err
	}
	return s.repo.Refund(ctx, Change{
		Kind:                 "refund",
		UserID:               userID,
		ClientID:             clientID,
		ExternalOrderNo:      externalRefundNo,
		AmountUnits:          units,
		Subject:              truncate(strings.TrimSpace(reason), 500),
		TransactionID:        txID,
		RelatedTransactionID: relatedTransactionID,
		IdempotencyKey:       fmt.Sprintf("oauth-wallet:%s:refund:%s", clientID, externalRefundNo),
	})
}

func parsePositiveAmount(raw string) (int64, error) {
	units, err := money.Parse(raw)
	if err != nil || units <= 0 {
		return 0, ErrInvalidAmount
	}
	return units, nil
}

func randomTransactionID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return transactionPrefix + hex.EncodeToString(buf), nil
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
}
