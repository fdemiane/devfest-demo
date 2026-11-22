//go:build ignore

package main

import (
	"context"
	"log"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	transferv1 "metroparadox/gen/metroparadox/transfer/v1"
)

func main() {
	conn, err := grpc.NewClient("localhost:50051", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("connect failed: %v", err)
	}
	defer conn.Close()

	// Here we create the client, from the generated code (remember the slides?)
	client := transferv1.NewTransferServiceClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Here we invoke the SayHello rpc method
	res, err := client.SayHello(ctx, &transferv1.SayHelloRequest{})
	if err != nil {
		log.Fatalf("SayHello failed: %v", err)
	}

	log.Printf("response: %s", res.GetMessage())
}
