package main

import (
	"context"
	"fmt"
	"log"

	"github.com/runopsio/hoop/agent"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/common/version"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func main() {
	fmt.Println(string(version.JSON()))

	// dail server
	conn, err := grpc.Dial(":9090", grpc.WithInsecure())
	if err != nil {
		log.Fatalf("can not connect with server %v", err)
	}

	// create stream
	requestCtx := metadata.AppendToOutgoingContext(context.Background(),
		"authorization", "Bearer x-agt-test-token",
		"hostname", "localhost",
		"machine_id", "machine_my",
		"kernel_version", "who knows?")

	client := pb.NewTransportClient(conn)
	stream, err := client.Connect(requestCtx)
	if err != nil {
		log.Fatalf("server rejected the connection: %v", err)
	}

	ctx := stream.Context()
	done := make(chan struct{})
	agt := agent.New(ctx, stream, done)
	defer agt.Close()

	go agt.Run()
	<-agt.Context().Done()
	if err := agt.Context().Err(); err != nil {
		log.Printf("error: %s", err.Error())
	}
	log.Println("Server terminated connection... exiting...")
}
