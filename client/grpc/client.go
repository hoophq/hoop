package grpc

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	pb "github.com/runopsio/hoop/common/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type (
	mutexClient struct {
		grpcClient  *grpc.ClientConn
		stream      pb.Transport_ConnectClient
		ctx         context.Context
		mutex       sync.RWMutex
		closeSignal chan struct{}
	}
)

func Connect(token string, kv ...string) (pb.ClientTransport, error) {
	addr := os.Getenv("API_URL")
	if addr == "" {
		addr = "127.0.0.1"
	}
	conn, err := grpc.Dial(addr+":8010", grpc.WithInsecure())
	if err != nil {
		return nil, fmt.Errorf("failed dialing to grpc server, err=%v", err)
	}

	// x-hooper-test-token
	kv = append(kv,
		"authorization", fmt.Sprintf("Bearer %s", token),
		"hostname", "localhost",
		"machine_id", "machine_my",
		"kernel_version", "who knows?")
	// create stream
	requestCtx := metadata.AppendToOutgoingContext(context.Background(), kv...)

	c := pb.NewTransportClient(conn)
	stream, err := c.Connect(requestCtx)
	if err != nil {
		return nil, fmt.Errorf("failed connecting to streaming RPC server, err=%v", err)
	}

	done := make(chan struct{})
	ctx := stream.Context()

	return &mutexClient{
		grpcClient:  conn,
		stream:      stream,
		ctx:         ctx,
		closeSignal: done,
		mutex:       sync.RWMutex{},
	}, nil
}

func (c *mutexClient) Send(pkt *pb.Packet) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.stream.Send(pkt)
}

func (c *mutexClient) Recv() (*pb.Packet, error) {
	return c.stream.Recv()
}

// Close tear down the stream and grpc connection
func (c *mutexClient) Close() (error, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	streamCloseErr := c.stream.CloseSend()
	connCloseErr := c.grpcClient.Close()
	return streamCloseErr, connCloseErr
}

func (c *mutexClient) StartKeepAlive() {
	go func() {
		for {
			proto := &pb.Packet{Type: pb.PacketKeepAliveType.String()}
			if err := c.Send(proto); err != nil {
				if err != nil {
					log.Printf("failed sending keep alive command, err=%v", err)
					break
				}
			}
			time.Sleep(pb.DefaultKeepAlive)
		}
	}()
}

func (c *mutexClient) StreamContext() context.Context {
	return c.stream.Context()
}

// func ConnectGrpc(connectionName string) (*Client, error) {
// 	// dial server
// 	addr := os.Getenv("API_URL")
// 	if addr == "" {
// 		addr = "127.0.0.1"
// 	}
// 	conn, err := grpc.Dial(addr+":8010", grpc.WithInsecure())
// 	if err != nil {
// 		return nil, err
// 	}

// 	// create stream
// 	requestCtx := metadata.AppendToOutgoingContext(context.Background(),
// 		"authorization", "Bearer x-hooper-test-token",
// 		"hostname", "localhost",
// 		"machine_id", "machine_my",
// 		"kernel_version", "who knows?",
// 		"connection_name", connectionName)

// 	c := pb.NewTransportClient(conn)
// 	stream, err := c.Connect(requestCtx)
// 	if err != nil {
// 		return nil, err
// 	}

// 	done := make(chan bool)
// 	ctx := stream.Context()
// 	client := &Client{
// 		Stream:      stream,
// 		Ctx:         ctx,
// 		CloseSignal: done,
// 		Close:       conn.Close,
// 	}

// 	return client, nil
// }

// func (c *Client) WaitCloseSignal() {
// 	<-c.Ctx.Done()
// 	if err := c.Ctx.Err(); err != nil {
// 		log.Printf("error message: %s", err.Error())
// 	}
// 	close(c.CloseSignal)
// }

// func (c *Client) StartKeepAlive() {
// 	for {
// 		time.Sleep(pb.DefaultKeepAlive)
// 		proto := &pb.Packet{Type: pb.PacketKeepAliveType.String()}
// 		if err := c.Stream.Send(proto); err != nil {
// 			if err != nil {
// 				log.Printf("failed sending keep alive command, err=%v", err)
// 				break
// 			}
// 		}
// 	}
// }
