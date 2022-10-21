package grpc

import (
	"context"
	"log"
	"os"
	"time"

	pb "github.com/runopsio/hoop/common/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type (
	Client struct {
		Stream      pb.Transport_ConnectClient
		Ctx         context.Context
		CloseSignal chan bool
		Close       func() error
	}
)

func ConnectGrpc(connectionName string, protocol pb.ProtocolType) (*Client, error) {
	// dial server
	addr := os.Getenv("API_URL")
	if addr == "" {
		addr = "127.0.0.1"
	}
	conn, err := grpc.Dial(addr+":8010", grpc.WithInsecure())
	if err != nil {
		return nil, err
	}

	// create stream
	requestCtx := metadata.AppendToOutgoingContext(context.Background(),
		"authorization", "Bearer x-hooper-test-token",
		"hostname", "localhost",
		"machine_id", "machine_my",
		"kernel_version", "who knows?",
		"protocol_name", string(protocol),
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
		Close:       conn.Close,
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
		time.Sleep(pb.DefaultKeepAlive)
		proto := &pb.Packet{Type: pb.PacketKeepAliveType.String()}
		if err := c.Stream.Send(proto); err != nil {
			if err != nil {
				log.Printf("failed sending keep alive command, err=%v", err)
				break
			}
		}
	}
}
