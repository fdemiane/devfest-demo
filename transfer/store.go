package transfer

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

var (
	ErrNotFound          = errors.New("account not found")
	ErrInsufficientFunds = errors.New("insufficient funds")
	ErrInvalidArgument   = errors.New("invalid argument")
	ErrInjectedFailure   = errors.New("injected storage failure")
)

type MemoryStore struct {
	mu       sync.Mutex
	accounts map[string]Account
	failNext bool
}

func NewMemoryStore(accounts ...Account) *MemoryStore {
	m := make(map[string]Account, len(accounts))
	for _, a := range accounts {
		m[a.ID] = a
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
	return acc, nil
}

func (s *MemoryStore) AllAccounts(_ context.Context) ([]Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Account, 0, len(s.accounts))
	for _, acc := range s.accounts {
		out = append(out, acc)
	}
	return out, nil
}

func (s *MemoryStore) ExecuteTransfer(_ context.Context, req TransferRequest, fn func(src, dst Account) (Account, Account, *TransferResponse, error)) (*TransferResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	src, ok := s.accounts[req.FromAccountID]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, req.FromAccountID)
	}
	dst, ok := s.accounts[req.ToAccountID]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, req.ToAccountID)
	}

	newSrc, newDst, res, err := fn(src, dst)
	if err != nil {
		return nil, err
	}

	if s.failNext {
		s.failNext = false
		return nil, ErrInjectedFailure
	}

	s.accounts[src.ID] = newSrc
	s.accounts[dst.ID] = newDst
	return res, nil
}
