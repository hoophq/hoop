package main

import (
	"context"
	"fmt"
	pb "github.com/runopsio/hoop/domain/proto"
	"google.golang.org/grpc"
	"log"
	"math/rand"
	"time"
)

func main() {
	fmt.Println("starting hoop agent...")
	rand.Seed(time.Now().Unix())

	// dail server
	conn, err := grpc.Dial(":9090", grpc.WithInsecure())
	if err != nil {
		log.Fatalf("can not connect with server %v", err)
	}

	// create stream
	client := pb.NewTransportClient(conn)
	stream, err := client.Connect(context.Background())
	if err != nil {
		log.Fatalf("openn stream error %v", err)
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
		log.Println("Server closed the connection... exiting...")
		if err := ctx.Err(); err != nil {
			log.Println(err)
		}
		close(done)
	}()

	<-done
	log.Println("Server terminated connection... exiting...")
}
