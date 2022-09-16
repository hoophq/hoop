package grpc

import (
	"context"
	pb "github.com/runopsio/hoop/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"log"
	"time"
)

type (
	Client struct {
		Stream      pb.Transport_ConnectClient
		Ctx         context.Context
		CloseSignal chan bool
	}
)

func ConnectGrpc(connectionName string) (*Client, error) {
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
		"connection_name", connectionName)

	c := pb.NewTransportClient(conn)
	stream, err := c.Connect(requestCtx)
	if err != nil {
		return nil, err
	}

	done := make(chan bool)
	ctx := stream.Context()
	client := &Client{
		Stream:      stream,
		Ctx:         ctx,
		CloseSignal: done,
	}

	return client, nil
}

func (c *Client) WaitCloseSignal() {
	<-c.Ctx.Done()
	if err := c.Ctx.Err(); err != nil {
		log.Printf("error message: %s", err.Error())
	}
	close(c.CloseSignal)
}

func (c *Client) StartKeepAlive() {
	for {
		proto := &pb.Packet{
			Component: pb.PacketClientComponent,
			Type:      pb.PacketKeepAliveType,
		}
		log.Println("sending keep alive command")
		if err := c.Stream.Send(proto); err != nil {
			if err != nil {
				log.Printf("failed sending keep alive command, err=%v", err)
				break
			}
		}
		time.Sleep(pb.DefaultKeepAlive)
	}
}
