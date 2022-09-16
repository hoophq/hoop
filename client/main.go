package main

import (
	"context"
	"fmt"
	pb "github.com/runopsio/hoop/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"log"
)

func main() {
	log.Println("starting hoop client...")

	done := make(chan bool)

	client, err := connectGrpc()
	if err != nil {
		log.Printf("exiting...error connecting with server  %v", err)
		close(done)
		return
	}

	client.closeSignal = done

	go waitCloseSignal(client)
	go client.listen()

	go sendDemoMessages(client) // remove later

	<-done
	log.Println("Server terminated connection... exiting...")
}

func connectGrpc() (*client, error) {
	// dial server
	conn, err := grpc.Dial(":9090", grpc.WithInsecure())
	if err != nil {
		return nil, err
	}

	// create stream
	requestCtx := metadata.AppendToOutgoingContext(context.Background(),
		"authorization", "Bearer x-hooper-test-token",
		"hostname", "localhost",
		"machine_id", "machine_my",
		"kernel_version", "who knows?",
		"connection_name", "my-conn")

	c := pb.NewTransportClient(conn)
	stream, err := c.Connect(requestCtx)
	if err != nil {
		return nil, err
	}

	ctx := stream.Context()
	client := client{
		stream: stream,
		ctx:    ctx,
	}

	return &client, nil
}

func waitCloseSignal(client *client) {
	<-client.ctx.Done()
	if err := client.ctx.Err(); err != nil {
		log.Printf("error message: %s", err.Error())
	}
	close(client.closeSignal)
}

func sendDemoMessages(client *client) {
	for i := 0; i < 3; i++ {
		client.stream.Send(&pb.Packet{
			Component: pb.PacketClientComponent,
			Type:      pb.PacketDataStreamType,
			Spec:      nil,
			Payload:   []byte(fmt.Sprintf("please process my request id [%d]", i)),
		})
	}
}
