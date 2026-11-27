package transfer

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"

	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	transferv1 "metroparadox/gen/metroparadox/transfer/v1"
)

func mustGetAccount(t *testing.T, store *MemoryStore, id string) Account {
	t.Helper()
	acc, err := store.GetAccount(context.Background(), id)
	if err != nil {
		t.Fatalf("get account %q: %v", id, err)
	}
	return acc
}

func mustAllAccounts(t *testing.T, store *MemoryStore) []Account {
	t.Helper()
	accounts, err := store.AllAccounts(context.Background())
	if err != nil {
		t.Fatalf("all accounts: %v", err)
	}
	return accounts
}

func defaultTestAccounts() []Account {
	return []Account{
		{ID: "alice", Balance: &money.Money{CurrencyCode: "USD", Units: 100}},
		{ID: "bob", Balance: &money.Money{CurrencyCode: "USD", Units: 50}},
	}
}

func defaultValidRequest() *transferv1.CreateTransferRequest {
	return &transferv1.CreateTransferRequest{
		RequestId: "11111111-1111-4111-8111-111111111111",
		Transfer: &transferv1.Transfer{
			SourceAccount:      "alice",
			DestinationAccount: "bob",
			Amount:             &money.Money{CurrencyCode: "USD", Units: 10},
		},
	}
}

func TestSayHello_ValidRequest_ReturnsGreeting(t *testing.T) {
	// Given
	svc := NewService(NewMemoryStore())

	// When
	resp, err := svc.SayHello(context.Background(), &transferv1.SayHelloRequest{})

	// Then
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if resp.GetMessage() != "Hello Google Fest" {
		t.Fatalf("got message %q, want %q", resp.GetMessage(), "Hello Google Fest")
	}
}

func TestCreateTransfer_SupportedCurrencies_SucceedsAndUpdatesBalances(t *testing.T) {
	tests := []struct {
		name     string
		currency string
		srcUnits int64
		srcNanos int32
		dstUnits int64
		dstNanos int32
		amtUnits int64
		amtNanos int32
		wantSrc  *money.Money
		wantDst  *money.Money
	}{
		{
			name:     "USD transfer",
			currency: "USD",
			srcUnits: 100,
			dstUnits: 50,
			amtUnits: 40,
			wantSrc:  &money.Money{CurrencyCode: "USD", Units: 60},
			wantDst:  &money.Money{CurrencyCode: "USD", Units: 90},
		},
		{
			name:     "LBP transfer",
			currency: "LBP",
			srcUnits: 500000,
			dstUnits: 100000,
			amtUnits: 150000,
			wantSrc:  &money.Money{CurrencyCode: "LBP", Units: 350000},
			wantDst:  &money.Money{CurrencyCode: "LBP", Units: 250000},
		},
		{
			name:     "exact full balance transfer",
			currency: "USD",
			srcUnits: 100,
			dstUnits: 25,
			amtUnits: 100,
			wantSrc:  &money.Money{CurrencyCode: "USD", Units: 0},
			wantDst:  &money.Money{CurrencyCode: "USD", Units: 125},
		},
		{
			name:     "fractional nanos borrow and carry",
			currency: "USD",
			srcUnits: 100,
			srcNanos: 250_000_000,
			dstUnits: 50,
			dstNanos: 750_000_000,
			amtUnits: 0,
			amtNanos: 750_000_000,
			wantSrc:  &money.Money{CurrencyCode: "USD", Units: 99, Nanos: 500_000_000},
			wantDst:  &money.Money{CurrencyCode: "USD", Units: 51, Nanos: 500_000_000},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Given
			store := NewMemoryStore(
				Account{ID: "alice", Balance: &money.Money{CurrencyCode: tc.currency, Units: tc.srcUnits, Nanos: tc.srcNanos}},
				Account{ID: "bob", Balance: &money.Money{CurrencyCode: tc.currency, Units: tc.dstUnits, Nanos: tc.dstNanos}},
			)
			svc := NewService(store)
			req := &transferv1.CreateTransferRequest{
				RequestId: "11111111-1111-4111-8111-111111111111",
				Transfer: &transferv1.Transfer{
					SourceAccount:      "alice",
					DestinationAccount: "bob",
					Amount:             &money.Money{CurrencyCode: tc.currency, Units: tc.amtUnits, Nanos: tc.amtNanos},
				},
			}

			// When
			resp, err := svc.CreateTransfer(context.Background(), req)

			// Then
			if err != nil {
				t.Fatalf("expected nil error, got %v", err)
			}
			if resp.GetState() != transferv1.Transfer_COMPLETED {
				t.Fatalf("got state %v, want COMPLETED", resp.GetState())
			}
			if !strings.HasPrefix(resp.GetName(), "transfers/") {
				t.Fatalf("got name %q, want transfers/ prefix", resp.GetName())
			}
			if resp.GetCreateTime() == nil {
				t.Fatal("expected non-nil create_time")
			}
			alice := mustGetAccount(t, store, "alice")
			bob := mustGetAccount(t, store, "bob")
			if !proto.Equal(alice.Balance, tc.wantSrc) || !proto.Equal(bob.Balance, tc.wantDst) {
				t.Fatalf("got alice=%v bob=%v, want alice=%v bob=%v", alice.Balance, bob.Balance, tc.wantSrc, tc.wantDst)
			}
		})
	}
}

func TestCreateTransfer_ThirdPartyAccounts_RemainUnchanged(t *testing.T) {
	// Given
	charlieBefore := &money.Money{CurrencyCode: "USD", Units: 200, Nanos: 500_000_000}
	danaBefore := &money.Money{CurrencyCode: "LBP", Units: 900000}
	store := NewMemoryStore(
		Account{ID: "alice", Balance: &money.Money{CurrencyCode: "USD", Units: 100}},
		Account{ID: "bob", Balance: &money.Money{CurrencyCode: "USD", Units: 50}},
		Account{ID: "charlie", Balance: proto.Clone(charlieBefore).(*money.Money)},
		Account{ID: "dana", Balance: proto.Clone(danaBefore).(*money.Money)},
	)
	svc := NewService(store)
	req := defaultValidRequest()

	// When
	_, err := svc.CreateTransfer(context.Background(), req)

	// Then
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	charlie := mustGetAccount(t, store, "charlie")
	dana := mustGetAccount(t, store, "dana")
	if !proto.Equal(charlie.Balance, charlieBefore) {
		t.Fatalf("charlie balance changed: got %v, want %v", charlie.Balance, charlieBefore)
	}
	if !proto.Equal(dana.Balance, danaBefore) {
		t.Fatalf("dana balance changed: got %v, want %v", dana.Balance, danaBefore)
	}
}

func TestCreateTransfer_InvalidRequests_ReturnsErrorAndLeavesAccountsUnchanged(t *testing.T) {
	tests := []struct {
		name     string
		accounts []Account
		nilReq   bool
		mutate   func(*transferv1.CreateTransferRequest)
		wantCode codes.Code
	}{
		{
			name:     "nil request",
			nilReq:   true,
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "nil transfer",
			mutate:   func(r *transferv1.CreateTransferRequest) { r.Transfer = nil },
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "missing request_id",
			mutate:   func(r *transferv1.CreateTransferRequest) { r.RequestId = "" },
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "transfer_id contains slash",
			mutate:   func(r *transferv1.CreateTransferRequest) { r.TransferId = "bad/id" },
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "empty source account",
			mutate:   func(r *transferv1.CreateTransferRequest) { r.Transfer.SourceAccount = "" },
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "empty destination account",
			mutate:   func(r *transferv1.CreateTransferRequest) { r.Transfer.DestinationAccount = "" },
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "same source and destination account",
			mutate:   func(r *transferv1.CreateTransferRequest) { r.Transfer.DestinationAccount = "alice" },
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "missing source account in store",
			accounts: []Account{{ID: "bob", Balance: &money.Money{CurrencyCode: "USD", Units: 50}}},
			mutate:   func(r *transferv1.CreateTransferRequest) { r.Transfer.SourceAccount = "unknown" },
			wantCode: codes.NotFound,
		},
		{
			name:     "missing destination account in store",
			accounts: []Account{{ID: "alice", Balance: &money.Money{CurrencyCode: "USD", Units: 100}}},
			mutate:   func(r *transferv1.CreateTransferRequest) { r.Transfer.DestinationAccount = "unknown" },
			wantCode: codes.NotFound,
		},
		{
			name:     "nil amount",
			mutate:   func(r *transferv1.CreateTransferRequest) { r.Transfer.Amount = nil },
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "zero amount",
			mutate:   func(r *transferv1.CreateTransferRequest) { r.Transfer.Amount = &money.Money{CurrencyCode: "USD"} },
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "negative units amount",
			mutate:   func(r *transferv1.CreateTransferRequest) { r.Transfer.Amount.Units = -10 },
			wantCode: codes.InvalidArgument,
		},
		{
			name: "negative nanos amount",
			mutate: func(r *transferv1.CreateTransferRequest) {
				r.Transfer.Amount = &money.Money{CurrencyCode: "USD", Units: 0, Nanos: -500}
			},
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "nanos out of range",
			mutate:   func(r *transferv1.CreateTransferRequest) { r.Transfer.Amount.Nanos = 1_000_000_000 },
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "sign mismatch between units and nanos",
			mutate:   func(r *transferv1.CreateTransferRequest) { r.Transfer.Amount.Nanos = -500 },
			wantCode: codes.InvalidArgument,
		},
		{
			name: "unsupported currency EUR",
			accounts: []Account{
				{ID: "alice", Balance: &money.Money{CurrencyCode: "EUR", Units: 100}},
				{ID: "bob", Balance: &money.Money{CurrencyCode: "EUR", Units: 50}},
			},
			mutate:   func(r *transferv1.CreateTransferRequest) { r.Transfer.Amount.CurrencyCode = "EUR" },
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "empty currency",
			mutate:   func(r *transferv1.CreateTransferRequest) { r.Transfer.Amount.CurrencyCode = "" },
			wantCode: codes.InvalidArgument,
		},
		{
			name: "source and destination currency mismatch",
			accounts: []Account{
				{ID: "alice", Balance: &money.Money{CurrencyCode: "USD", Units: 100}},
				{ID: "bob", Balance: &money.Money{CurrencyCode: "LBP", Units: 50}},
			},
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "request currency differs from account currency",
			mutate:   func(r *transferv1.CreateTransferRequest) { r.Transfer.Amount.CurrencyCode = "LBP" },
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "insufficient funds",
			mutate:   func(r *transferv1.CreateTransferRequest) { r.Transfer.Amount.Units = 101 },
			wantCode: codes.FailedPrecondition,
		},
		{
			name: "destination balance overflow",
			accounts: []Account{
				{ID: "alice", Balance: &money.Money{CurrencyCode: "LBP", Units: 100}},
				{ID: "bob", Balance: &money.Money{CurrencyCode: "LBP", Units: math.MaxInt64}},
			},
			mutate: func(r *transferv1.CreateTransferRequest) {
				r.Transfer.Amount = &money.Money{CurrencyCode: "LBP", Units: 1}
			},
			wantCode: codes.InvalidArgument,
		},
		{
			name: "corrupted nil stored balance",
			accounts: []Account{
				{ID: "alice", Balance: nil},
				{ID: "bob", Balance: &money.Money{CurrencyCode: "USD", Units: 50}},
			},
			wantCode: codes.InvalidArgument,
		},
		{
			name: "corrupted negative stored balance",
			accounts: []Account{
				{ID: "alice", Balance: &money.Money{CurrencyCode: "USD", Units: -10}},
				{ID: "bob", Balance: &money.Money{CurrencyCode: "USD", Units: 50}},
			},
			wantCode: codes.InvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Given
			accounts := tc.accounts
			if accounts == nil {
				accounts = defaultTestAccounts()
			}
			store := NewMemoryStore(accounts...)
			before := mustAllAccounts(t, store)
			svc := NewService(store)

			var req *transferv1.CreateTransferRequest
			if !tc.nilReq {
				req = defaultValidRequest()
				if tc.mutate != nil {
					tc.mutate(req)
				}
			}

			// When
			_, err := svc.CreateTransfer(context.Background(), req)

			// Then
			if status.Code(err) != tc.wantCode {
				t.Fatalf("got code %v (err=%v), want %v", status.Code(err), err, tc.wantCode)
			}
			for _, accBefore := range before {
				accAfter := mustGetAccount(t, store, accBefore.ID)
				if !proto.Equal(accAfter.Balance, accBefore.Balance) {
					t.Fatalf("account %s changed: got %v, want %v", accBefore.ID, accAfter.Balance, accBefore.Balance)
				}
			}
		})
	}
}

func TestCreateTransfer_CancelledContext_ReturnsCanceled(t *testing.T) {
	// Given
	store := NewMemoryStore(defaultTestAccounts()...)
	svc := NewService(store)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// When
	_, err := svc.CreateTransfer(ctx, defaultValidRequest())

	// Then
	if status.Code(err) != codes.Canceled {
		t.Fatalf("got code %v, want Canceled", status.Code(err))
	}
	alice := mustGetAccount(t, store, "alice")
	if alice.Balance.GetUnits() != 100 {
		t.Fatalf("got alice balance %d, want 100", alice.Balance.GetUnits())
	}
}

func TestCreateTransfer_SameRequestID_IsIdempotent(t *testing.T) {
	// Given
	store := NewMemoryStore(defaultTestAccounts()...)
	svc := NewService(store)
	req := defaultValidRequest()
	req.TransferId = "tx-custom"
	req.Transfer.Amount.Units = 25
	first, err := svc.CreateTransfer(context.Background(), req)
	if err != nil {
		t.Fatalf("first call failed: %v", err)
	}

	// When
	second, err := svc.CreateTransfer(context.Background(), req)

	// Then
	if err != nil {
		t.Fatalf("second call failed: %v", err)
	}
	if !proto.Equal(first, second) {
		t.Fatalf("responses differ: first=%v second=%v", first, second)
	}
	if first.GetName() != "transfers/tx-custom" {
		t.Fatalf("got name %q, want transfers/tx-custom", first.GetName())
	}
	alice := mustGetAccount(t, store, "alice")
	bob := mustGetAccount(t, store, "bob")
	if alice.Balance.GetUnits() != 75 || bob.Balance.GetUnits() != 75 {
		t.Fatalf("transfer executed twice: alice=%d bob=%d", alice.Balance.GetUnits(), bob.Balance.GetUnits())
	}
}

func TestCreateTransfer_SameRequestIDDifferentArgs_ReturnsInvalidArgument(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*transferv1.CreateTransferRequest)
	}{
		{
			name:   "different amount",
			mutate: func(r *transferv1.CreateTransferRequest) { r.Transfer.Amount.Units = 30 },
		},
		{
			name:   "different destination account",
			mutate: func(r *transferv1.CreateTransferRequest) { r.Transfer.DestinationAccount = "charlie" },
		},
		{
			name:   "different source account",
			mutate: func(r *transferv1.CreateTransferRequest) { r.Transfer.SourceAccount = "charlie" },
		},
		{
			name:   "different transfer_id",
			mutate: func(r *transferv1.CreateTransferRequest) { r.TransferId = "different-tx" },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Given
			store := NewMemoryStore(
				Account{ID: "alice", Balance: &money.Money{CurrencyCode: "USD", Units: 100}},
				Account{ID: "bob", Balance: &money.Money{CurrencyCode: "USD", Units: 50}},
				Account{ID: "charlie", Balance: &money.Money{CurrencyCode: "USD", Units: 50}},
			)
			svc := NewService(store)
			req1 := defaultValidRequest()
			req1.Transfer.Amount.Units = 20
			if _, err := svc.CreateTransfer(context.Background(), req1); err != nil {
				t.Fatalf("first call failed: %v", err)
			}
			req2 := defaultValidRequest()
			req2.Transfer.Amount.Units = 20
			tc.mutate(req2)

			// When
			_, err := svc.CreateTransfer(context.Background(), req2)

			// Then
			if status.Code(err) != codes.InvalidArgument {
				t.Fatalf("got code %v (err=%v), want InvalidArgument", status.Code(err), err)
			}
			alice := mustGetAccount(t, store, "alice")
			bob := mustGetAccount(t, store, "bob")
			if alice.Balance.GetUnits() != 80 || bob.Balance.GetUnits() != 70 {
				t.Fatalf("balances changed after rejected request: alice=%d bob=%d", alice.Balance.GetUnits(), bob.Balance.GetUnits())
			}
		})
	}
}

func TestCreateTransfer_StorageFailure_LeavesAccountsUnchanged(t *testing.T) {
	// Given
	store := NewMemoryStore(defaultTestAccounts()...)
	store.FailNextTransfer()
	svc := NewService(store)

	// When
	_, err := svc.CreateTransfer(context.Background(), defaultValidRequest())

	// Then
	if status.Code(err) != codes.Internal {
		t.Fatalf("got code %v, want Internal", status.Code(err))
	}
	alice := mustGetAccount(t, store, "alice")
	bob := mustGetAccount(t, store, "bob")
	if alice.Balance.GetUnits() != 100 || bob.Balance.GetUnits() != 50 {
		t.Fatalf("partial update occurred: alice=%d bob=%d", alice.Balance.GetUnits(), bob.Balance.GetUnits())
	}
}

func TestCreateTransfer_AfterStorageFailure_RetrySucceeds(t *testing.T) {
	// Given
	store := NewMemoryStore(defaultTestAccounts()...)
	store.FailNextTransfer()
	svc := NewService(store)
	req := defaultValidRequest()
	if _, err := svc.CreateTransfer(context.Background(), req); err == nil {
		t.Fatal("expected initial transfer to fail")
	}

	// When
	resp, err := svc.CreateTransfer(context.Background(), req)

	// Then
	if err != nil {
		t.Fatalf("retry after failure failed: %v", err)
	}
	if resp.GetState() != transferv1.Transfer_COMPLETED {
		t.Fatalf("got state %v, want COMPLETED", resp.GetState())
	}
}

func TestCreateTransfer_ConcurrentSameRequestID_ExecutesOnce(t *testing.T) {
	// Given
	store := NewMemoryStore(defaultTestAccounts()...)
	svc := NewService(store)
	req := defaultValidRequest()
	req.Transfer.Amount.Units = 25

	// When
	const workers = 10
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.CreateTransfer(context.Background(), req)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)

	// Then
	for err := range errs {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	alice := mustGetAccount(t, store, "alice")
	bob := mustGetAccount(t, store, "bob")
	if alice.Balance.GetUnits() != 75 || bob.Balance.GetUnits() != 75 {
		t.Fatalf("got alice=%d bob=%d, want 75, 75", alice.Balance.GetUnits(), bob.Balance.GetUnits())
	}
}

func TestCreateTransfer_ConcurrentContendingTransfers_PreventsOverdraftAndConservesSum(t *testing.T) {
	// Given
	store := NewMemoryStore(defaultTestAccounts()...)
	svc := NewService(store)

	// When
	const workers = 10
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := range workers {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			req := &transferv1.CreateTransferRequest{
				RequestId: fmt.Sprintf("req-concurrent-%d", idx),
				Transfer: &transferv1.Transfer{
					SourceAccount:      "alice",
					DestinationAccount: "bob",
					Amount:             &money.Money{CurrencyCode: "USD", Units: 20},
				},
			}
			_, err := svc.CreateTransfer(context.Background(), req)
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)

	// Then
	var succeeded, failedPrecondition int
	for err := range errs {
		switch status.Code(err) {
		case codes.OK:
			succeeded++
		case codes.FailedPrecondition:
			failedPrecondition++
		default:
			t.Fatalf("unexpected error code %v: %v", status.Code(err), err)
		}
	}
	if succeeded != 5 || failedPrecondition != 5 {
		t.Fatalf("got succeeded=%d failedPrecondition=%d, want 5 and 5", succeeded, failedPrecondition)
	}
	alice := mustGetAccount(t, store, "alice")
	bob := mustGetAccount(t, store, "bob")
	if alice.Balance.GetUnits() != 0 || bob.Balance.GetUnits() != 150 {
		t.Fatalf("got alice=%d bob=%d, want 0 and 150", alice.Balance.GetUnits(), bob.Balance.GetUnits())
	}
}

func TestCreateTransfer_ConcurrentBidirectionalTransfers_ConservesTotalBalance(t *testing.T) {
	// Given
	store := NewMemoryStore(
		Account{ID: "alice", Balance: &money.Money{CurrencyCode: "USD", Units: 100}},
		Account{ID: "bob", Balance: &money.Money{CurrencyCode: "USD", Units: 100}},
	)
	svc := NewService(store)

	// When
	const pairs = 10
	errs := make(chan error, pairs*2)
	var wg sync.WaitGroup
	for i := range pairs {
		wg.Add(2)
		go func(idx int) {
			defer wg.Done()
			_, err := svc.CreateTransfer(context.Background(), &transferv1.CreateTransferRequest{
				RequestId: fmt.Sprintf("a-to-b-%d", idx),
				Transfer: &transferv1.Transfer{
					SourceAccount:      "alice",
					DestinationAccount: "bob",
					Amount:             &money.Money{CurrencyCode: "USD", Units: 5},
				},
			})
			errs <- err
		}(i)
		go func(idx int) {
			defer wg.Done()
			_, err := svc.CreateTransfer(context.Background(), &transferv1.CreateTransferRequest{
				RequestId: fmt.Sprintf("b-to-a-%d", idx),
				Transfer: &transferv1.Transfer{
					SourceAccount:      "bob",
					DestinationAccount: "alice",
					Amount:             &money.Money{CurrencyCode: "USD", Units: 5},
				},
			})
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)

	// Then
	for err := range errs {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	alice := mustGetAccount(t, store, "alice")
	bob := mustGetAccount(t, store, "bob")
	if alice.Balance.GetUnits()+bob.Balance.GetUnits() != 200 {
		t.Fatalf("total balance changed: alice=%d bob=%d", alice.Balance.GetUnits(), bob.Balance.GetUnits())
	}
}
