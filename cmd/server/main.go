package main

import (
	"context"
	"log"

	"metroparadox/transfer"
)

func main() {
	store := transfer.NewMemoryStore(
		transfer.Account{ID: "alice", Balance: 100, Currency: "USD"},
		transfer.Account{ID: "bob", Balance: 50, Currency: "USD"},
	)
	svc := transfer.NewService(store)

	_, err := svc.TransferMoney(context.Background(), transfer.TransferRequest{
		FromAccountID: "alice",
		ToAccountID:   "bob",
		Amount:        25,
		Currency:      "USD",
	})
	if err != nil {
		log.Fatalf("transfer failed: %v", err)
	}
}
