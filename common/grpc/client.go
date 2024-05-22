package grpc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/runopsio/hoop/common/appruntime"
	pb "github.com/runopsio/hoop/common/proto"
	pbgateway "github.com/runopsio/hoop/common/proto/gateway"
	"github.com/runopsio/hoop/common/version"
	"golang.org/x/oauth2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/credentials/oauth"
	"google.golang.org/grpc/metadata"
)

type (
	OptionKey     string
	ClientOptions struct {
		optionKey OptionKey
		optionVal string
	}
	mutexClient struct {
		grpcClient *grpc.ClientConn
		stream     pb.Transport_ConnectClient
		mutex      sync.RWMutex
	}
	// ClientConfig
	ClientConfig struct {
		// The server address to connect to (HOST:PORT)
		ServerAddress string
		Token         string
		// This is used to specify a different DNS name
		// when connecting via TLS
		TLSServerName string
		UserAgent     string
		// Insecure indicates if it will connect without TLS
		// It should only be used in secure networks!
		Insecure bool
	}
)

const (
	OptionConnectionName OptionKey = "connection-name"
	OptionUserInfo       OptionKey = "user-info"
	OptionConnectionInfo OptionKey = "connection-info"
	LocalhostAddr                  = "127.0.0.1:8010"

	MaxRecvMsgSize int = 1024 * 1024 * 16
)

func WithOption(optKey OptionKey, val string) *ClientOptions {
	return &ClientOptions{optionKey: optKey, optionVal: val}
}

func ConnectLocalhost(token, userAgent string, opts ...*ClientOptions) (pb.ClientTransport, error) {
	opts = append(opts, &ClientOptions{
		optionKey: "authorization",
		optionVal: fmt.Sprintf("Bearer %s", token),
	})
	return connectRPC(LocalhostAddr,
		[]grpc.DialOption{
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithUserAgent(userAgent)},
		opts...)
}

func PreConnectRPC(cc ClientConfig, req *pb.PreConnectRequest) (*pb.PreConnectResponse, error) {
	if cc.Insecure {
		dialOptions := []grpc.DialOption{
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithUserAgent(cc.UserAgent),
			grpc.WithDefaultCallOptions(
				grpc.MaxCallRecvMsgSize(MaxRecvMsgSize),
			),
		}
		contextOptions := []string{
			"authorization", fmt.Sprintf("Bearer %s", cc.Token),
		}
		requestCtx := metadata.AppendToOutgoingContext(context.Background(), contextOptions...)
		ctx, cancelFn := context.WithTimeout(context.Background(), time.Second*15)
		defer cancelFn()
		conn, err := grpc.DialContext(ctx, cc.ServerAddress, dialOptions...)
		if err != nil {
			return nil, fmt.Errorf("failed dialing to grpc server, err=%v", err)
		}
		return pb.NewTransportClient(conn).PreConnect(requestCtx, req)
	}
	// TODO: it's deprecated, use oauth.TokenSource
	rpcCred := oauth.NewOauthAccess(&oauth2.Token{AccessToken: cc.Token})
	tlsConfig := &tls.Config{ServerName: cc.TLSServerName}
	dialOptions := []grpc.DialOption{
		grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)),
		grpc.WithPerRPCCredentials(rpcCred),
		grpc.WithUserAgent(cc.UserAgent),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(MaxRecvMsgSize),
		),
	}
	ctx, cancelFn := context.WithTimeout(context.Background(), time.Second*15)
	defer cancelFn()
	conn, err := grpc.DialContext(ctx, cc.ServerAddress, dialOptions...)
	if err != nil {
		return nil, fmt.Errorf("failed dialing to grpc server, err=%v", err)
	}
	return pb.NewTransportClient(conn).PreConnect(context.Background(), req)
}

func Connect(clientConfig ClientConfig, opts ...*ClientOptions) (pb.ClientTransport, error) {
	if clientConfig.Insecure {
		opts = append(opts, &ClientOptions{
			optionKey: "authorization",
			optionVal: fmt.Sprintf("Bearer %s", clientConfig.Token),
		})
		return connectRPC(clientConfig.ServerAddress,
			[]grpc.DialOption{
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithUserAgent(clientConfig.UserAgent),
				grpc.WithDefaultCallOptions(
					grpc.MaxCallRecvMsgSize(MaxRecvMsgSize),
				),
			},
			opts...)
	}
	// TODO: it's deprecated, use oauth.TokenSource
	rpcCred := oauth.NewOauthAccess(&oauth2.Token{AccessToken: clientConfig.Token})
	tlsCred, err := loadTLSCredentials(clientConfig)
	if err != nil {
		return nil, err
	}
	// tlsConfig := &tls.Config{ServerName: clientConfig.TLSServerName}
	dialOptions := []grpc.DialOption{
		grpc.WithTransportCredentials(tlsCred),
		grpc.WithPerRPCCredentials(rpcCred),
		grpc.WithUserAgent(clientConfig.UserAgent),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(MaxRecvMsgSize),
		),
	}
	return connectRPC(clientConfig.ServerAddress, dialOptions, opts...)
}

func loadTLSCredentials(cc ClientConfig) (credentials.TransportCredentials, error) {
	pemServerCA, err := os.ReadFile(os.Getenv("TLS_CA"))
	if err != nil {
		return nil, err
	}

	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM(pemServerCA) {
		return nil, fmt.Errorf("failed to add server CA's certificate")
	}

	// Create the credentials and return it
	config := &tls.Config{
		RootCAs:    certPool,
		ServerName: cc.TLSServerName,
	}

	return credentials.NewTLS(config), nil
}

func connectRPC(serverAddress string, dialOptions []grpc.DialOption, opts ...*ClientOptions) (pb.ClientTransport, error) {
	ctx, cancelFn := context.WithTimeout(context.Background(), time.Second*15)
	defer cancelFn()
	conn, err := grpc.DialContext(ctx, serverAddress, dialOptions...)
	if err != nil {
		return nil, fmt.Errorf("failed dialing to grpc server, err=%v", err)
	}
	osmap := appruntime.OS()
	ver := version.Get()
	contextOptions := []string{
		"version", ver.Version,
		"go-version", ver.GoVersion,
		"compiler", ver.Compiler,
		"platform", ver.Platform,
		"hostname", osmap["hostname"],
		"machine-id", osmap["machine_id"],
		"kernel-version", osmap["kernel_version"],
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

	return &mutexClient{
		grpcClient: conn,
		stream:     stream,
		mutex:      sync.RWMutex{},
	}, nil
}

func (c *mutexClient) Send(pkt *pb.Packet) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	err := c.stream.Send(pkt)
	if err != nil && err != io.EOF {
		sentry.CaptureException(fmt.Errorf("failed sending msg, type=%v, err=%v", pkt.Type, err))
	}
	return err
}

func (c *mutexClient) Recv() (*pb.Packet, error) {
	return c.stream.Recv()
}

// Close tear down the stream and grpc connection
func (c *mutexClient) Close() (error, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	connCloseErr := c.grpcClient.Close()
	streamCloseErr := c.stream.CloseSend()
	return streamCloseErr, connCloseErr
}

func (c *mutexClient) StartKeepAlive() {
	go func() {
		for {
			proto := &pb.Packet{Type: pbgateway.KeepAlive}
			if err := c.Send(proto); err != nil {
				break
			}
			time.Sleep(pb.DefaultKeepAlive)
		}
	}()
}

func (c *mutexClient) StreamContext() context.Context {
	return c.stream.Context()
}

// ParseServerAddress parses addr to a HOST:PORT string.
// It validates if it's a valid server name HOST:PORT or
// a valid URL, usually SCHEME://HOST:PORT.
func ParseServerAddress(addr string) (string, error) {
	u, _ := url.Parse(addr)
	if u == nil {
		u = &url.URL{}
	}
	srvAddr := u.Host
	if srvAddr == "" {
		host, port, found := strings.Cut(addr, ":")
		if !found || host == "" {
			return "", fmt.Errorf("server address is in wrong format")
		}
		srvAddr = fmt.Sprintf("%s:%s", host, port)
	}
	return srvAddr, nil
}

// ShouldDebugGrpc return true if env LOG_GRPC=1 or LGO_GRPC=2
func ShouldDebugGrpc() bool {
	return os.Getenv("LOG_GRPC") == "1" || os.Getenv("LOG_GRPC") == "2"
}
