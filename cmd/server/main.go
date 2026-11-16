package main

import (
	"log"
	"net"

	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
	transferv1 "metroparadox/gen/metroparadox/transfer/v1"
	"metroparadox/transfer"
)

func main() {
	store := transfer.NewMemoryStore(
		transfer.Account{ID: "alice", Balance: &money.Money{CurrencyCode: "USD", Units: 100}},
		transfer.Account{ID: "bob", Balance: &money.Money{CurrencyCode: "USD", Units: 50}},
	)

	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatalf("listen failed: %v", err)
	}

	grpcServer := grpc.NewServer()
	transferv1.RegisterTransferServiceServer(grpcServer, transfer.NewService(store))
	reflection.Register(grpcServer)

	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("serve failed: %v", err)
	}
}
