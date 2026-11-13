package transfer

import (
	"context"
	"fmt"
	"net"
	"net/rpc"
	"sync"
)

type StoreState struct {
	Accounts map[string]Account
	FailNext bool
}

type StoreControl struct {
	store *MemoryStore
}

func (c *StoreControl) Reset(state StoreState, _ *struct{}) error {
	c.store.mu.Lock()
	defer c.store.mu.Unlock()
	m := make(map[string]Account, len(state.Accounts))
	for k, v := range state.Accounts {
		m[k] = v
	}
	c.store.accounts = m
	c.store.failNext = state.FailNext
	return nil
}

func (c *StoreControl) FailNext(_ struct{}, _ *struct{}) error {
	c.store.FailNextTransfer()
	return nil
}

func (c *StoreControl) GetAccount(id string, out *Account) error {
	acc, err := c.store.GetAccount(context.Background(), id)
	if err != nil {
		return err
	}
	*out = acc
	return nil
}

func (c *StoreControl) AllAccounts(_ struct{}, out *[]Account) error {
	accs, err := c.store.AllAccounts(context.Background())
	if err != nil {
		return err
	}
	*out = accs
	return nil
}

func (s *MemoryStore) ListenControl(addr string) (net.Listener, error) {
	srv := rpc.NewServer()
	if err := srv.RegisterName("StoreControl", &StoreControl{store: s}); err != nil {
		return nil, err
	}
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	go srv.Accept(lis)
	return lis, nil
}

var (
	clientMu sync.Mutex
	clients  = make(map[string]*rpcClient)
)

func getRPCClient(addr string) (*rpcClient, error) {
	clientMu.Lock()
	defer clientMu.Unlock()
	if rc, ok := clients[addr]; ok {
		return rc, nil
	}
	c, err := rpc.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}
	rc := &rpcClient{client: c}
	clients[addr] = rc
	return rc, nil
}

func (s *MemoryStore) AttachRemote(addr string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rc, err := getRPCClient(addr)
	if err != nil {
		return err
	}
	if err := rc.reset(StoreState{Accounts: s.accounts, FailNext: s.failNext}); err != nil {
		return err
	}
	s.remote = rc
	return nil
}

type rpcClient struct {
	client *rpc.Client
}

func (r *rpcClient) reset(state StoreState) error {
	return r.client.Call("StoreControl.Reset", state, &struct{}{})
}

func (r *rpcClient) failNext() error {
	return r.client.Call("StoreControl.FailNext", struct{}{}, &struct{}{})
}

func (r *rpcClient) getAccount(id string) (Account, error) {
	var acc Account
	if err := r.client.Call("StoreControl.GetAccount", id, &acc); err != nil {
		return Account{}, fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	return acc, nil
}

func (r *rpcClient) allAccounts() ([]Account, error) {
	var accs []Account
	if err := r.client.Call("StoreControl.AllAccounts", struct{}{}, &accs); err != nil {
		return nil, err
	}
	return accs, nil
}
