package transfer

import (
	"context"
	"math"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

// What are the invariants and post conditions?
// Transfer(from, to, money, currency)
// - from is a valid account
// - to is a valid account
// - from != to
// - money is > 0
// - currency is in the set of supported currencies (USD, LBP only)
// - money is <= account balance of from
// - if a is from, b is to, (a + b) before = (a + b) after.
// - for all accounts X not in {from,to}, X before = X after

// Not in scope:
// We should assert for user facing error messages

func totalBalance(t *testing.T, store Store) float64 {
	t.Helper()
	accounts, err := store.AllAccounts(context.Background())
	if err != nil {
		t.Fatalf("AllAccounts failed: %v", err)
	}
	var sum float64
	for _, a := range accounts {
		sum += float64(a.Balance)
	}
	return sum
}

func TestTransferMoney_ValidRequest_UpdatesBalances(t *testing.T) {
	tests := []struct {
		name      string
		currency  string
		amount    float32
		wantAlice float32
		wantBob   float32
	}{
		{name: "USD partial", currency: "USD", amount: 25, wantAlice: 75, wantBob: 35},
		{name: "LBP partial", currency: "LBP", amount: 25, wantAlice: 75, wantBob: 35},
		{name: "USD full balance", currency: "USD", amount: 100, wantAlice: 0, wantBob: 110},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Given
			store := NewMemoryStore(
				Account{ID: "alice", Balance: 100, Currency: tc.currency},
				Account{ID: "bob", Balance: 10, Currency: tc.currency},
				Account{ID: "charlie", Balance: 50, Currency: tc.currency},
			)
			svc := NewService(store)
			req := TransferRequest{
				FromAccountID: "alice",
				ToAccountID:   "bob",
				Amount:        tc.amount,
				Currency:      tc.currency,
			}

			// When
			res, err := svc.TransferMoney(context.Background(), req)

			// Then
			if err != nil {
				t.Fatalf("expected nil error, got %v", err)
			}
			if res == nil || res.TransferID == "" {
				t.Fatalf("expected non-empty TransferID, got %+v", res)
			}
			alice, _ := store.GetAccount(context.Background(), "alice")
			bob, _ := store.GetAccount(context.Background(), "bob")
			charlie, _ := store.GetAccount(context.Background(), "charlie")
			if alice.Balance != tc.wantAlice {
				t.Errorf("alice balance = %v, want %v", alice.Balance, tc.wantAlice)
			}
			if bob.Balance != tc.wantBob {
				t.Errorf("bob balance = %v, want %v", bob.Balance, tc.wantBob)
			}
			if charlie.Balance != 50 {
				t.Errorf("charlie balance = %v, want 50", charlie.Balance)
			}
		})
	}
}

func TestTransferMoney_MixedTransfers_ConservesTotalMoney(t *testing.T) {
	// Given
	rng := rand.New(rand.NewSource(42))
	accounts := []Account{
		{ID: "a", Balance: float32(100 + rng.Intn(500)), Currency: "USD"},
		{ID: "b", Balance: float32(100 + rng.Intn(500)), Currency: "USD"},
		{ID: "c", Balance: float32(100 + rng.Intn(500)), Currency: "USD"},
		{ID: "d", Balance: float32(100 + rng.Intn(500)), Currency: "USD"},
	}
	store := NewMemoryStore(accounts...)
	svc := NewService(store)
	before := totalBalance(t, store)
	initialByID := make(map[string]float32, len(accounts))
	for _, a := range accounts {
		initialByID[a.ID] = a.Balance
	}
	ids := []string{"a", "b", "c", "d", "missing"}

	// When
	var succeeded int
	for i := 0; i < 50; i++ {
		src := ids[rng.Intn(len(ids))]
		dst := ids[rng.Intn(len(ids))]
		amount := float32(rng.Intn(300) - 50)
		_, err := svc.TransferMoney(context.Background(), TransferRequest{
			FromAccountID: src,
			ToAccountID:   dst,
			Amount:        amount,
			Currency:      "USD",
		})
		if err == nil {
			succeeded++
		}
	}

	// Then
	if succeeded == 0 {
		t.Fatalf("expected at least one valid transfer to succeed")
	}
	afterAccounts, _ := store.AllAccounts(context.Background())
	changed := false
	for _, a := range afterAccounts {
		if a.Balance != initialByID[a.ID] {
			changed = true
			break
		}
	}
	if !changed {
		t.Fatalf("expected at least one account balance to change after transfers")
	}
	after := totalBalance(t, store)
	if after != before {
		t.Fatalf("total money not conserved: before=%v, after=%v", before, after)
	}
}

func TestTransferMoney_ConcurrentRequests_MatchesSequentialOrder(t *testing.T) {
	// Given
	initial := []Account{
		{ID: "a", Balance: 100, Currency: "USD"},
		{ID: "b", Balance: 80, Currency: "USD"},
		{ID: "c", Balance: 60, Currency: "USD"},
	}
	reqs := []TransferRequest{
		{FromAccountID: "a", ToAccountID: "b", Amount: 50, Currency: "USD"},
		{FromAccountID: "a", ToAccountID: "c", Amount: 60, Currency: "USD"},
		{FromAccountID: "b", ToAccountID: "c", Amount: 70, Currency: "USD"},
		{FromAccountID: "c", ToAccountID: "a", Amount: 40, Currency: "USD"},
		{FromAccountID: "b", ToAccountID: "a", Amount: 30, Currency: "USD"},
	}
	store := NewMemoryStore(initial...)
	svc := NewService(store)

	// When
	var wg sync.WaitGroup
	var mu sync.Mutex
	var succeeded int
	for _, r := range reqs {
		wg.Add(1)
		go func(req TransferRequest) {
			defer wg.Done()
			if _, err := svc.TransferMoney(context.Background(), req); err == nil {
				mu.Lock()
				succeeded++
				mu.Unlock()
			}
		}(r)
	}
	wg.Wait()

	// Then
	if succeeded == 0 {
		t.Fatalf("expected at least one parallel transfer to succeed")
	}
	finalAccounts, _ := store.AllAccounts(context.Background())
	finalMap := make(map[string]float32, len(finalAccounts))
	for _, a := range finalAccounts {
		finalMap[a.ID] = a.Balance
	}
	if !reachableSequentially(initial, reqs, finalMap) {
		t.Fatalf("parallel final balances %v are not reachable by any sequential order", finalMap)
	}
}

func reachableSequentially(initial []Account, reqs []TransferRequest, target map[string]float32) bool {
	perm := make([]int, len(reqs))
	for i := range perm {
		perm[i] = i
	}
	var found bool
	var permute func(int)
	permute = func(k int) {
		if found {
			return
		}
		if k == len(perm) {
			bal := make(map[string]float32, len(initial))
			for _, a := range initial {
				bal[a.ID] = a.Balance
			}
			for _, idx := range perm {
				r := reqs[idx]
				if bal[r.FromAccountID] >= r.Amount {
					bal[r.FromAccountID] -= r.Amount
					bal[r.ToAccountID] += r.Amount
				}
			}
			if reflect.DeepEqual(bal, target) {
				found = true
			}
			return
		}
		for i := k; i < len(perm); i++ {
			perm[k], perm[i] = perm[i], perm[k]
			permute(k + 1)
			perm[k], perm[i] = perm[i], perm[k]
		}
	}
	permute(0)
	return found
}

func TestTransferMoney_StorageFailureMidTransfer_LeavesBalancesUnchanged(t *testing.T) {
	// Given
	store := NewMemoryStore(
		Account{ID: "a", Balance: 100, Currency: "USD"},
		Account{ID: "b", Balance: 50, Currency: "USD"},
	)
	svc := NewService(store)
	store.FailNextTransfer()
	req := TransferRequest{
		FromAccountID: "a",
		ToAccountID:   "b",
		Amount:        30,
		Currency:      "USD",
	}

	// When
	_, err := svc.TransferMoney(context.Background(), req)

	// Then
	if err == nil {
		t.Fatalf("expected error when storage fails mid-transfer, got nil")
	}
	a, _ := store.GetAccount(context.Background(), "a")
	b, _ := store.GetAccount(context.Background(), "b")
	if a.Balance != 100 || b.Balance != 50 {
		t.Fatalf("atomicity violated: a=%v, b=%v (want 100, 50)", a.Balance, b.Balance)
	}
}

func TestTransferMoney_InvalidRequest_ReturnsError(t *testing.T) {
	tests := []struct {
		name string
		req  TransferRequest
	}{
		{
			name: "insufficient funds",
			req: TransferRequest{
				FromAccountID: "a",
				ToAccountID:   "b",
				Amount:        500,
				Currency:      "USD",
			},
		},
		{
			name: "zero amount",
			req: TransferRequest{
				FromAccountID: "a",
				ToAccountID:   "b",
				Amount:        0,
				Currency:      "USD",
			},
		},
		{
			name: "negative amount",
			req: TransferRequest{
				FromAccountID: "a",
				ToAccountID:   "b",
				Amount:        -10,
				Currency:      "USD",
			},
		},
		{
			name: "empty currency",
			req: TransferRequest{
				FromAccountID: "a",
				ToAccountID:   "b",
				Amount:        10,
				Currency:      "",
			},
		},
		{
			name: "currency mismatch with destination",
			req: TransferRequest{
				FromAccountID: "a",
				ToAccountID:   "eur",
				Amount:        10,
				Currency:      "USD",
			},
		},
		{
			name: "currency mismatch with source",
			req: TransferRequest{
				FromAccountID: "a",
				ToAccountID:   "b",
				Amount:        10,
				Currency:      "EUR",
			},
		},
		{
			name: "self transfer",
			req: TransferRequest{
				FromAccountID: "a",
				ToAccountID:   "a",
				Amount:        10,
				Currency:      "USD",
			},
		},
		{
			name: "non-existent source account",
			req: TransferRequest{
				FromAccountID: "unknown",
				ToAccountID:   "b",
				Amount:        10,
				Currency:      "USD",
			},
		},
		{
			name: "non-existent destination account",
			req: TransferRequest{
				FromAccountID: "a",
				ToAccountID:   "unknown",
				Amount:        10,
				Currency:      "USD",
			},
		},
		{
			name: "infinite amount",
			req: TransferRequest{
				FromAccountID: "a",
				ToAccountID:   "b",
				Amount:        float32(math.Inf(1)),
				Currency:      "USD",
			},
		},
		{
			name: "NaN amount",
			req: TransferRequest{
				FromAccountID: "a",
				ToAccountID:   "b",
				Amount:        float32(math.NaN()),
				Currency:      "USD",
			},
		},
		{
			name: "unsupported currency",
			req: TransferRequest{
				FromAccountID: "eur",
				ToAccountID:   "eur2",
				Amount:        10,
				Currency:      "EUR",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Given
			store := NewMemoryStore(
				Account{ID: "a", Balance: 100, Currency: "USD"},
				Account{ID: "b", Balance: 50, Currency: "USD"},
				Account{ID: "eur", Balance: 50, Currency: "EUR"},
				Account{ID: "eur2", Balance: 50, Currency: "EUR"},
			)
			svc := NewService(store)

			// When
			_, err := svc.TransferMoney(context.Background(), tc.req)

			// Then
			if err == nil {
				t.Fatalf("expected error for %s, got nil", tc.name)
			}
			a, _ := store.GetAccount(context.Background(), "a")
			b, _ := store.GetAccount(context.Background(), "b")
			eur, _ := store.GetAccount(context.Background(), "eur")
			eur2, _ := store.GetAccount(context.Background(), "eur2")
			if a.Balance != 100 || b.Balance != 50 || eur.Balance != 50 || eur2.Balance != 50 {
				t.Fatalf("balances changed on invalid request: a=%v, b=%v, eur=%v, eur2=%v", a.Balance, b.Balance, eur.Balance, eur2.Balance)
			}
		})
	}
}

func FuzzTransferMoney_RandomInputs_ConservesTotalMoney(f *testing.F) {
	f.Add("a", "b", float32(10.5), "USD")
	f.Add("a", "b", float32(0.1), "USD")
	f.Add("a", "a", float32(10), "USD")
	f.Add("a", "missing", float32(10), "USD")
	f.Add("a", "b", float32(-5), "USD")
	f.Add("a", "b", float32(0), "USD")
	f.Add("a", "b", float32(math.NaN()), "USD")
	f.Add("a", "b", float32(math.Inf(1)), "USD")
	f.Add("a", "b", float32(10), "EUR")

	f.Fuzz(func(t *testing.T, from, to string, amount float32, currency string) {
		// Given
		store := NewMemoryStore(
			Account{ID: "a", Balance: 100, Currency: "USD"},
			Account{ID: "b", Balance: 50, Currency: "USD"},
		)
		svc := NewService(store)
		before := totalBalance(t, store)

		// When
		_, _ = svc.TransferMoney(context.Background(), TransferRequest{
			FromAccountID: from,
			ToAccountID:   to,
			Amount:        amount,
			Currency:      currency,
		})

		// Then
		after := totalBalance(t, store)
		if math.IsNaN(after) || after != before {
			t.Fatalf("total money not conserved: before=%v, after=%v", before, after)
		}
		accounts, _ := store.AllAccounts(context.Background())
		for _, a := range accounts {
			if a.Balance < 0 {
				t.Fatalf("account %s has negative balance: %v", a.ID, a.Balance)
			}
		}
	})
}
