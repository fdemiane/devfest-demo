package transfer

import "context"

type Account struct {
	ID       string
	Balance  float32
	Currency string
}

type TransferRequest struct {
	FromAccountID string
	ToAccountID   string
	Amount        float32
	Currency      string
}

type TransferResponse struct {
	TransferID string
}

type Store interface {
	GetAccount(ctx context.Context, id string) (Account, error)
	AllAccounts(ctx context.Context) ([]Account, error)
	ExecuteTransfer(ctx context.Context, req TransferRequest, fn func(src, dst Account) (Account, Account, *TransferResponse, error)) (*TransferResponse, error)
}
