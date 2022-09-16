package main

import (
	"context"
	pb "github.com/runopsio/hoop/proto"
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
	agent := agent{
		stream:      stream,
		ctx:         ctx,
		closeSignal: done,
	}

	go waitCloseSignal(&agent)
	go agent.listen()

	<-done
	log.Println("Server terminated connection... exiting...")
}

func waitCloseSignal(agent *agent) {
	<-agent.ctx.Done()
	if err := agent.ctx.Err(); err != nil {
		log.Printf("error message: %s", err.Error())
	}
	close(agent.closeSignal)
}
