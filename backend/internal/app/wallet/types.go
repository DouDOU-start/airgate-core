// Package wallet 提供由 OAuth 外部应用发起的平台余额查询、扣款与退款。
package wallet

import "context"

const SourceOAuthWallet = "oauth_wallet"

// Change 是写入余额总账的标准变更。
type Change struct {
	Kind                 string
	UserID               int
	ClientID             string
	ExternalOrderNo      string
	AmountUnits          int64
	Subject              string
	TransactionID        string
	RelatedTransactionID string
	IdempotencyKey       string
}

// Transaction 是钱包交易结果。
type Transaction struct {
	LogID         int
	TransactionID string
	AmountUnits   int64
	BeforeBalance float64
	AfterBalance  float64
	Idempotent    bool
}

// Repository 定义钱包领域所需的原子持久化操作。
type Repository interface {
	Debit(ctx context.Context, change Change) (Transaction, error)
	Refund(ctx context.Context, change Change) (Transaction, error)
}
