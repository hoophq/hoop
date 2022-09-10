package main

import (
	"context"
	pb "github.com/runopsio/hoop/domain/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"log"
)

func main() {
	log.Println("starting hoop agent...")

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
	done := make(chan bool)
	agent := Agent{
		stream:      stream,
		ctx:         ctx,
		closeSignal: done,
	}

	go agent.listen()

	// if the server closes the connection
	go func() {
		<-ctx.Done()
		if err := ctx.Err(); err != nil {
			log.Printf("error message: %s", err.Error())
		}
		close(done)
	}()

	<-done
	log.Println("Server terminated connection... exiting...")
}
