package transfer

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/protobuf/proto"
)

var (
	ErrNotFound          = errors.New("account not found")
	ErrInsufficientFunds = errors.New("insufficient funds")
	ErrInvalidArgument   = errors.New("invalid argument")
	ErrInjectedFailure   = errors.New("injected storage failure")
)

type Account struct {
	ID      string
	Balance *money.Money
}

type TransferRequest struct {
	SourceAccount      string
	DestinationAccount string
	Amount             *money.Money
}

type TransferResponse struct {
	TransferID string
}

type Store interface {
	GetAccount(ctx context.Context, id string) (Account, error)
	AllAccounts(ctx context.Context) ([]Account, error)
	ExecuteTransfer(ctx context.Context, req TransferRequest, fn func(src, dst Account) (Account, Account, *TransferResponse, error)) (*TransferResponse, error)
}

type MemoryStore struct {
	mu       sync.Mutex
	accounts map[string]Account
	failNext bool
}

func cloneAccount(a Account) Account {
	if a.Balance != nil {
		a.Balance = proto.Clone(a.Balance).(*money.Money)
	}
	return a
}

func NewMemoryStore(accounts ...Account) *MemoryStore {
	m := make(map[string]Account, len(accounts))
	for _, a := range accounts {
		m[a.ID] = cloneAccount(a)
	}
	return &MemoryStore{
		accounts: m,
	}
}

func (s *MemoryStore) FailNextTransfer() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failNext = true
}

func (s *MemoryStore) GetAccount(_ context.Context, id string) (Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	acc, ok := s.accounts[id]
	if !ok {
		return Account{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return cloneAccount(acc), nil
}

func (s *MemoryStore) AllAccounts(_ context.Context) ([]Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Account, 0, len(s.accounts))
	for _, acc := range s.accounts {
		out = append(out, cloneAccount(acc))
	}
	return out, nil
}

func (s *MemoryStore) ExecuteTransfer(_ context.Context, req TransferRequest, fn func(src, dst Account) (Account, Account, *TransferResponse, error)) (*TransferResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	src, ok := s.accounts[req.SourceAccount]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, req.SourceAccount)
	}
	dst, ok := s.accounts[req.DestinationAccount]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, req.DestinationAccount)
	}

	newSrc, newDst, res, err := fn(cloneAccount(src), cloneAccount(dst))
	if err != nil {
		return nil, err
	}

	if s.failNext {
		s.failNext = false
		return nil, ErrInjectedFailure
	}

	s.accounts[src.ID] = cloneAccount(newSrc)
	s.accounts[dst.ID] = cloneAccount(newDst)
	return res, nil
}
