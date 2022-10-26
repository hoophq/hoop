package grpc

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/common/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type (
	OptionKey     string
	ClientOptions struct {
		optionKey OptionKey
		optionVal string
	}
	mutexClient struct {
		grpcClient  *grpc.ClientConn
		stream      pb.Transport_ConnectClient
		ctx         context.Context
		mutex       sync.RWMutex
		closeSignal chan struct{}
	}
)

const (
	OptionConnectionName OptionKey = "connection_name"
)

func WithOption(optKey OptionKey, val string) *ClientOptions {
	return &ClientOptions{optionKey: optKey, optionVal: val}
}

func Connect(serverAddress, token string, opts ...*ClientOptions) (pb.ClientTransport, error) {
	if serverAddress == "" {
		serverAddress = "127.0.0.1:8010"
	}
	conn, err := grpc.Dial(serverAddress, grpc.WithInsecure())
	if err != nil {
		return nil, fmt.Errorf("failed dialing to grpc server, err=%v", err)
	}

	osmap := runtime.OS()
	contextOptions := []string{
		"authorization", fmt.Sprintf("Bearer %s", token),
		"hostname", osmap["hostname"],
		"machine_id", osmap["machine_id"],
		"kernel_version", osmap["kernel_version"],
	}
	for _, opt := range opts {
		contextOptions = append(contextOptions, []string{
			string(opt.optionKey),
			opt.optionVal}...)
	}
	requestCtx := metadata.AppendToOutgoingContext(context.Background(), contextOptions...)

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
