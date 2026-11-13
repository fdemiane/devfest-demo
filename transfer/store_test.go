package transfer

import (
	"context"
	"errors"
	"testing"
)

func TestGetAccount_ExistingID_ReturnsAccount(t *testing.T) {
	// Given
	want := Account{ID: "alice", Balance: 100, Currency: "USD"}
	store := NewMemoryStore(want)

	// When
	got, err := store.GetAccount(context.Background(), "alice")

	// Then
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestGetAccount_MissingID_ReturnsNotFoundError(t *testing.T) {
	// Given
	store := NewMemoryStore(Account{ID: "alice", Balance: 100, Currency: "USD"})

	// When
	_, err := store.GetAccount(context.Background(), "unknown")

	// Then
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestAllAccounts_PopulatedStore_ReturnsAllAccounts(t *testing.T) {
	// Given
	store := NewMemoryStore(
		Account{ID: "alice", Balance: 100, Currency: "USD"},
		Account{ID: "bob", Balance: 50, Currency: "USD"},
	)

	// When
	got, err := store.AllAccounts(context.Background())

	// Then
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d accounts, want 2", len(got))
	}
}

func TestAllAccounts_EmptyStore_ReturnsEmptySlice(t *testing.T) {
	// Given
	store := NewMemoryStore()

	// When
	got, err := store.AllAccounts(context.Background())

	// Then
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d accounts, want 0", len(got))
	}
}

func TestExecuteTransfer_ValidCallback_UpdatesAccountsAndReturnsResponse(t *testing.T) {
	// Given
	store := NewMemoryStore(
		Account{ID: "alice", Balance: 100, Currency: "USD"},
		Account{ID: "bob", Balance: 50, Currency: "USD"},
	)
	req := TransferRequest{FromAccountID: "alice", ToAccountID: "bob", Amount: 25, Currency: "USD"}

	// When
	res, err := store.ExecuteTransfer(context.Background(), req, func(src, dst Account) (Account, Account, *TransferResponse, error) {
		src.Balance -= req.Amount
		dst.Balance += req.Amount
		return src, dst, &TransferResponse{TransferID: "tx-1"}, nil
	})

	// Then
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if res == nil || res.TransferID != "tx-1" {
		t.Fatalf("got response %+v, want TransferID tx-1", res)
	}
	alice, _ := store.GetAccount(context.Background(), "alice")
	bob, _ := store.GetAccount(context.Background(), "bob")
	if alice.Balance != 75 || bob.Balance != 75 {
		t.Fatalf("got balances alice=%v bob=%v, want 75, 75", alice.Balance, bob.Balance)
	}
}

func TestExecuteTransfer_FailureCases_LeavesAccountsUnchanged(t *testing.T) {
	callbackErr := errors.New("callback failed")

	tests := []struct {
		name       string
		req        TransferRequest
		failNext   bool
		fn         func(src, dst Account) (Account, Account, *TransferResponse, error)
		wantErr    error
	}{
		{
			name:    "missing source account",
			req:     TransferRequest{FromAccountID: "missing", ToAccountID: "bob", Amount: 10, Currency: "USD"},
			fn:      func(src, dst Account) (Account, Account, *TransferResponse, error) { return src, dst, &TransferResponse{}, nil },
			wantErr: ErrNotFound,
		},
		{
			name:    "missing destination account",
			req:     TransferRequest{FromAccountID: "alice", ToAccountID: "missing", Amount: 10, Currency: "USD"},
			fn:      func(src, dst Account) (Account, Account, *TransferResponse, error) { return src, dst, &TransferResponse{}, nil },
			wantErr: ErrNotFound,
		},
		{
			name: "callback returns error",
			req:  TransferRequest{FromAccountID: "alice", ToAccountID: "bob", Amount: 10, Currency: "USD"},
			fn: func(src, dst Account) (Account, Account, *TransferResponse, error) {
				src.Balance -= 10
				dst.Balance += 10
				return src, dst, nil, callbackErr
			},
			wantErr: callbackErr,
		},
		{
			name:     "injected storage failure",
			req:      TransferRequest{FromAccountID: "alice", ToAccountID: "bob", Amount: 10, Currency: "USD"},
			failNext: true,
			fn: func(src, dst Account) (Account, Account, *TransferResponse, error) {
				src.Balance -= 10
				dst.Balance += 10
				return src, dst, &TransferResponse{TransferID: "tx-fail"}, nil
			},
			wantErr: ErrInjectedFailure,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Given
			store := NewMemoryStore(
				Account{ID: "alice", Balance: 100, Currency: "USD"},
				Account{ID: "bob", Balance: 50, Currency: "USD"},
			)
			if tc.failNext {
				store.FailNextTransfer()
			}

			// When
			_, err := store.ExecuteTransfer(context.Background(), tc.req, tc.fn)

			// Then
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("got error %v, want %v", err, tc.wantErr)
			}
			alice, _ := store.GetAccount(context.Background(), "alice")
			bob, _ := store.GetAccount(context.Background(), "bob")
			if alice.Balance != 100 || bob.Balance != 50 {
				t.Fatalf("balances changed: alice=%v bob=%v, want 100, 50", alice.Balance, bob.Balance)
			}
		})
	}
}

func TestExecuteTransfer_AfterInjectedFailure_NextTransferSucceeds(t *testing.T) {
	// Given
	store := NewMemoryStore(
		Account{ID: "alice", Balance: 100, Currency: "USD"},
		Account{ID: "bob", Balance: 50, Currency: "USD"},
	)
	store.FailNextTransfer()
	req := TransferRequest{FromAccountID: "alice", ToAccountID: "bob", Amount: 10, Currency: "USD"}
	cb := func(src, dst Account) (Account, Account, *TransferResponse, error) {
		src.Balance -= req.Amount
		dst.Balance += req.Amount
		return src, dst, &TransferResponse{TransferID: "tx-ok"}, nil
	}
	_, _ = store.ExecuteTransfer(context.Background(), req, cb)

	// When
	res, err := store.ExecuteTransfer(context.Background(), req, cb)

	// Then
	if err != nil {
		t.Fatalf("expected nil error on retry after injected failure, got %v", err)
	}
	if res == nil || res.TransferID != "tx-ok" {
		t.Fatalf("got response %+v, want TransferID tx-ok", res)
	}
}

