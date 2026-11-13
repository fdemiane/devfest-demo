package main

import (
	"flag"
	"log"
	"net"

	"google.golang.org/grpc"
	"metroparadox/transfer"
)

func main() {
	addr := flag.String("addr", ":50051", "gRPC server listen address")
	controlAddr := flag.String("control-addr", "", "store control listen address")
	flag.Parse()

	store := transfer.NewMemoryStore(
		transfer.Account{ID: "alice", Balance: 100, Currency: "USD"},
		transfer.Account{ID: "bob", Balance: 50, Currency: "USD"},
	)

	if *controlAddr != "" {
		if _, err := store.ListenControl(*controlAddr); err != nil {
			log.Fatalf("control listen failed: %v", err)
		}
	}

	lis, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("listen failed: %v", err)
	}

	grpcServer := grpc.NewServer()
	_ = transfer.NewService(store)

	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("serve failed: %v", err)
	}
}
