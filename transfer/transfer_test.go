package transfer

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	transferv1 "metroparadox/gen/metroparadox/transfer/v1"
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

var (
	testGRPCAddr    string
	testControlAddr string
	testGRPCClient  transferv1.TransferServiceClient
)

func TestMain(m *testing.M) {
	tmpDir, err := os.MkdirTemp("", "metroparadox-test-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create temp dir: %v\n", err)
		os.Exit(1)
	}

	binPath := filepath.Join(tmpDir, "server")
	buildCmd := exec.Command("go", "build", "-o", binPath, "../cmd/server")
	buildCmd.Stdout = os.Stdout
	buildCmd.Stderr = os.Stderr
	if err := buildCmd.Run(); err != nil {
		_ = os.RemoveAll(tmpDir)
		fmt.Fprintf(os.Stderr, "failed to build server binary: %v\n", err)
		os.Exit(1)
	}

	testGRPCAddr = freeLocalAddr()
	testControlAddr = freeLocalAddr()

	srvCmd := exec.Command(binPath, "-addr", testGRPCAddr, "-control-addr", testControlAddr)
	srvCmd.Stdout = os.Stdout
	srvCmd.Stderr = os.Stderr
	if err := srvCmd.Start(); err != nil {
		_ = os.RemoveAll(tmpDir)
		fmt.Fprintf(os.Stderr, "failed to start server process: %v\n", err)
		os.Exit(1)
	}

	if err := waitForPort(testGRPCAddr, 5*time.Second); err != nil {
		_ = srvCmd.Process.Kill()
		_ = os.RemoveAll(tmpDir)
		fmt.Fprintf(os.Stderr, "gRPC server did not become ready: %v\n", err)
		os.Exit(1)
	}
	if err := waitForPort(testControlAddr, 5*time.Second); err != nil {
		_ = srvCmd.Process.Kill()
		_ = os.RemoveAll(tmpDir)
		fmt.Fprintf(os.Stderr, "control server did not become ready: %v\n", err)
		os.Exit(1)
	}

	conn, err := grpc.NewClient(testGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		_ = srvCmd.Process.Kill()
		_ = os.RemoveAll(tmpDir)
		fmt.Fprintf(os.Stderr, "failed to create gRPC client: %v\n", err)
		os.Exit(1)
	}
	testGRPCClient = transferv1.NewTransferServiceClient(conn)

	code := m.Run()

	_ = conn.Close()
	_ = srvCmd.Process.Kill()
	_, _ = srvCmd.Process.Wait()
	_ = os.RemoveAll(tmpDir)
	os.Exit(code)
}

func freeLocalAddr() string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

func waitForPort(addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for %s", addr)
}

func setupTestClient(t *testing.T, store *MemoryStore) transferv1.TransferServiceClient {
	t.Helper()
	if err := store.AttachRemote(testControlAddr); err != nil {
		t.Fatalf("AttachRemote failed: %v", err)
	}
	return testGRPCClient
}

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
			client := setupTestClient(t, store)
			req := &transferv1.TransferMoneyRequest{
				SourceAccountId:      "alice",
				DestinationAccountId: "bob",
				Amount:               tc.amount,
				CurrencyCode:         tc.currency,
			}

			// When
			res, err := client.TransferMoney(context.Background(), req)

			// Then
			if err != nil {
				t.Fatalf("expected nil error, got %v", err)
			}
			if res == nil || res.GetTransferId() == "" {
				t.Fatalf("expected non-empty TransferId, got %+v", res)
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
	client := setupTestClient(t, store)
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
		_, err := client.TransferMoney(context.Background(), &transferv1.TransferMoneyRequest{
			SourceAccountId:      src,
			DestinationAccountId: dst,
			Amount:               amount,
			CurrencyCode:         "USD",
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
	client := setupTestClient(t, store)

	// When
	var wg sync.WaitGroup
	var mu sync.Mutex
	var succeeded int
	for _, r := range reqs {
		wg.Add(1)
		go func(req TransferRequest) {
			defer wg.Done()
			_, err := client.TransferMoney(context.Background(), &transferv1.TransferMoneyRequest{
				SourceAccountId:      req.FromAccountID,
				DestinationAccountId: req.ToAccountID,
				Amount:               req.Amount,
				CurrencyCode:         req.Currency,
			})
			if err == nil {
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
	client := setupTestClient(t, store)
	store.FailNextTransfer()
	req := &transferv1.TransferMoneyRequest{
		SourceAccountId:      "a",
		DestinationAccountId: "b",
		Amount:               30,
		CurrencyCode:         "USD",
	}

	// When
	_, err := client.TransferMoney(context.Background(), req)

	// Then
	if err == nil {
		t.Fatalf("expected error when storage fails mid-transfer, got nil")
	}
	if status.Code(err) == codes.Unimplemented {
		t.Fatalf("service is unimplemented: %v", err)
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
		req  *transferv1.TransferMoneyRequest
	}{
		{
			name: "insufficient funds",
			req: &transferv1.TransferMoneyRequest{
				SourceAccountId:      "a",
				DestinationAccountId: "b",
				Amount:               500,
				CurrencyCode:         "USD",
			},
		},
		{
			name: "zero amount",
			req: &transferv1.TransferMoneyRequest{
				SourceAccountId:      "a",
				DestinationAccountId: "b",
				Amount:               0,
				CurrencyCode:         "USD",
			},
		},
		{
			name: "negative amount",
			req: &transferv1.TransferMoneyRequest{
				SourceAccountId:      "a",
				DestinationAccountId: "b",
				Amount:               -10,
				CurrencyCode:         "USD",
			},
		},
		{
			name: "empty currency",
			req: &transferv1.TransferMoneyRequest{
				SourceAccountId:      "a",
				DestinationAccountId: "b",
				Amount:               10,
				CurrencyCode:         "",
			},
		},
		{
			name: "currency mismatch with destination",
			req: &transferv1.TransferMoneyRequest{
				SourceAccountId:      "a",
				DestinationAccountId: "eur",
				Amount:               10,
				CurrencyCode:         "USD",
			},
		},
		{
			name: "currency mismatch with source",
			req: &transferv1.TransferMoneyRequest{
				SourceAccountId:      "a",
				DestinationAccountId: "b",
				Amount:               10,
				CurrencyCode:         "EUR",
			},
		},
		{
			name: "self transfer",
			req: &transferv1.TransferMoneyRequest{
				SourceAccountId:      "a",
				DestinationAccountId: "a",
				Amount:               10,
				CurrencyCode:         "USD",
			},
		},
		{
			name: "non-existent source account",
			req: &transferv1.TransferMoneyRequest{
				SourceAccountId:      "unknown",
				DestinationAccountId: "b",
				Amount:               10,
				CurrencyCode:         "USD",
			},
		},
		{
			name: "non-existent destination account",
			req: &transferv1.TransferMoneyRequest{
				SourceAccountId:      "a",
				DestinationAccountId: "unknown",
				Amount:               10,
				CurrencyCode:         "USD",
			},
		},
		{
			name: "infinite amount",
			req: &transferv1.TransferMoneyRequest{
				SourceAccountId:      "a",
				DestinationAccountId: "b",
				Amount:               float32(math.Inf(1)),
				CurrencyCode:         "USD",
			},
		},
		{
			name: "NaN amount",
			req: &transferv1.TransferMoneyRequest{
				SourceAccountId:      "a",
				DestinationAccountId: "b",
				Amount:               float32(math.NaN()),
				CurrencyCode:         "USD",
			},
		},
		{
			name: "unsupported currency",
			req: &transferv1.TransferMoneyRequest{
				SourceAccountId:      "eur",
				DestinationAccountId: "eur2",
				Amount:               10,
				CurrencyCode:         "EUR",
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
			client := setupTestClient(t, store)

			// When
			_, err := client.TransferMoney(context.Background(), tc.req)

			// Then
			if err == nil {
				t.Fatalf("expected error for %s, got nil", tc.name)
			}
			if status.Code(err) == codes.Unimplemented {
				t.Fatalf("service is unimplemented: %v", err)
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
		client := setupTestClient(t, store)
		before := totalBalance(t, store)

		// When
		_, err := client.TransferMoney(context.Background(), &transferv1.TransferMoneyRequest{
			SourceAccountId:      from,
			DestinationAccountId: to,
			Amount:               amount,
			CurrencyCode:         currency,
		})

		// Then
		if status.Code(err) == codes.Unimplemented {
			t.Fatalf("service is unimplemented: %v", err)
		}
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
