package postgresproxy

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/grpc"
	"github.com/hoophq/hoop/common/keys"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/memory"
	pgtypes "github.com/hoophq/hoop/common/pgtypes"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	pbclient "github.com/hoophq/hoop/common/proto/client"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/proxyproto/grpckey"
)

var (
	instanceStore        = memory.New()
	instanceKey   string = "pg_server"
)

type PGServer struct {
	connectionStore memory.Store
	listener        net.Listener
	listenAddr      string
	tlsConfig       *tls.Config
}

// GetServerInstance returns the singleton instance of PGServer.
func GetServerInstance() *PGServer {
	if server, ok := instanceStore.Get(instanceKey).(*PGServer); ok {
		return server
	}
	server := &PGServer{}
	instanceStore.Set(instanceKey, server)
	return server
}

func (s *PGServer) Start(listenAddr string, tlsConfig *tls.Config) error {
	if _, ok := instanceStore.Get(instanceKey).(*PGServer); ok && s.listener != nil {
		return nil
	}

	log.Infof("starting postgres server proxy at %v", listenAddr)
	serverTLSConfig := tlsConfig.Clone()
	serverTLSConfig.NextProtos = []string{"postgresql"}

	// start new instance
	server, err := runPgProxyServer(listenAddr, serverTLSConfig)
	if err != nil {
		return err
	}
	instanceStore.Set(instanceKey, server)
	return nil
}

func (s *PGServer) Stop() error {
	if server, ok := instanceStore.Pop(instanceKey).(*PGServer); ok {
		if s.connectionStore == nil {
			return nil
		}
		for _, obj := range s.connectionStore.List() {
			if pgConn, ok := obj.(*postgresConn); ok {
				pgConn.cancelFn("proxy server is shutting down")
			}
		}

		if server.listener != nil {
			log.Infof("stopping postgres server proxy at %v", server.listener.Addr().String())
			_ = server.listener.Close()
		}
	}
	return nil
}

func (s *PGServer) ListenAddr() string { return s.listenAddr }

func runPgProxyServer(listenAddr string, tlsConfig *tls.Config) (*PGServer, error) {
	lis, err := net.Listen("tcp4", listenAddr)
	if err != nil {
		return nil, fmt.Errorf("failed listening to address %v, err=%v", listenAddr, err)
	}

	server := &PGServer{
		connectionStore: memory.New(),
		listener:        lis,
		listenAddr:      listenAddr,
		tlsConfig:       tlsConfig,
	}

	go func() {
		defer lis.Close()
		connectionID := 0
		for {
			connectionID++
			pgClient, err := lis.Accept()
			if err != nil {
				log.With("conn", connectionID).Warnf("failed obtaining postgres connection, err=%v", err)
				continue
			}

			sid := uuid.NewString()
			conn, err := newPostgresConnection(sid, strconv.Itoa(connectionID), pgClient, server.tlsConfig)
			if err != nil {
				if err == io.EOF {
					log.With("conn", connectionID).Debugf("failed creating new postgres connection, reason=EOF error")
					_ = pgClient.Close()
					continue
				}
				log.With("conn", connectionID).Warnf("failed creating new postgres connection, err=%v", err)
				_, _ = pgClient.Write(pgtypes.NewFatalError("failed creating new postgres connection, err=%v", err).Encode())
				_ = pgClient.Close()
				continue
			}

			server.connectionStore.Set(sid, conn)
			go func() {
				defer server.connectionStore.Del(sid)
				conn.handleTcpConnection()
			}()
		}
	}()
	return server, nil
}

type postgresConn struct {
	sid           string
	id            string
	ctx           context.Context
	cancelFn      func(msg string, a ...any)
	streamClient  pb.ClientTransport
	initialPacket []byte
	net.Conn
}

func newPostgresConnection(sid, connID string, conn net.Conn, tlsConfig *tls.Config) (*postgresConn, error) {
	pgpkt, err := pgtypes.Decode(conn)
	if err != nil {
		return nil, err
	}

	isFrontendTLSRequest, err := validateClientTLSConn(pgpkt, tlsConfig)
	if err != nil {
		return nil, err
	}

	// Handle SSL request
	if isFrontendTLSRequest {
		// Accept SSL request
		log.With("conn", connID).Infof("accepting SSL request")
		if _, err = conn.Write([]byte{pgtypes.ServerSSLSupported.Byte()}); err != nil {
			return nil, fmt.Errorf("failed writing ssl supported response, err=%v", err)
		}

		// Upgrade connection to TLS
		tlsConn := tls.Server(conn, tlsConfig)
		if err := tlsConn.Handshake(); err != nil {
			return nil, fmt.Errorf("failed performing TLS handshake, err=%v", err)
		}

		log.With("conn", connID).Infof("TLS handshake completed successfully")

		// Now read the actual startup packet over the encrypted connection
		pgpkt, err = pgtypes.Decode(tlsConn)
		if err != nil {
			return nil, fmt.Errorf("failed decoding startup packet after TLS, err=%v", err)
		}

		// Replace connection with TLS connection
		conn = tlsConn
	}

	pgConn := &postgresConn{sid: sid, id: connID, Conn: conn, initialPacket: pgpkt.Encode()}

	if pgpkt.IsCancelRequest() {
		// TODO(san): handle cancel request in the future
		return nil, fmt.Errorf("cancel request not implemented")
	}
	startupPkt := pgpkt.Frame()

	var parameters []string
	var param string
	for _, p := range startupPkt[4 : len(startupPkt)-1] {
		if p == 0x00 {
			parameters = append(parameters, param)
			param = ""
			continue
		}
		param += string(p)
	}

	var secretKey string
	for i, p := range parameters {
		if p == "user" && len(parameters) >= i+1 {
			secretKey = parameters[i+1]
		}
	}

	if secretKey == "" {
		return nil, fmt.Errorf("failed obtaining secret key from startup parameters")
	}

	secretKeyHash, err := keys.Hash256Key(secretKey)
	if err != nil {
		return nil, fmt.Errorf("failed hashing secret key: %v", err)
	}

	dba, err := models.GetValidConnectionCredentialsBySecretKey(pb.ConnectionTypePostgres.String(), secretKeyHash)
	if err != nil {
		if err == models.ErrNotFound {
			return nil, fmt.Errorf("invalid secret access key credentials")
		}
		return nil, fmt.Errorf("failed obtaining secret access key, reason=%v", err)
	}
	ctxDuration := dba.ExpireAt.Sub(time.Now().UTC())
	if dba.ExpireAt.Before(time.Now().UTC()) {
		return nil, fmt.Errorf("invalid secret access key credentials")
	}

	log.Infof("obtained db access by id, id=%v, subject=%v, connection=%v, expires-at=%v (in %v)",
		dba.ID, dba.UserSubject, dba.ConnectionName,
		dba.ExpireAt.Format(time.RFC3339), ctxDuration.Truncate(time.Second).String())

	ctx, cancelFn := context.WithCancelCause(context.Background())
	ctx, timeoutCancelFn := context.WithTimeoutCause(ctx, ctxDuration, fmt.Errorf("connection access expired (%v)", dba.ExpireAt.Format(time.RFC3339)))
	pgConn.cancelFn = func(msg string, a ...any) {
		cancelFn(fmt.Errorf(msg, a...))
		timeoutCancelFn()
	}
	pgConn.ctx = ctx
	client, err := grpc.Connect(grpc.ClientConfig{
		ServerAddress: grpc.LocalhostAddr,
		Token:         "", // it will use impersonate-auth-key as authentication
		UserAgent:     "postgres/grpc",
		Insecure:      appconfig.Get().GatewayUseTLS() == false,
		TLSCA:         appconfig.Get().GrpcClientTLSCa(),
		// it should be safe to skip verify here as we are connecting to localhost
		TLSSkipVerify: true,
	},
		grpc.WithOption(grpc.OptionConnectionName, dba.ConnectionName),
		grpc.WithOption(grpckey.ImpersonateAuthKeyHeaderKey, grpckey.ImpersonateSecretKey),
		grpc.WithOption(grpckey.ImpersonateUserSubjectHeaderKey, dba.UserSubject),
		grpc.WithOption("origin", pb.ConnectionOriginClient),
		grpc.WithOption("verb", pb.ClientVerbConnect),
		grpc.WithOption("session-id", sid),
	)
	if err != nil {
		return nil, fmt.Errorf("failed connecting to grpc server, err=%v", err)
	}
	pgConn.streamClient = client
	return pgConn, nil
}

func (c *postgresConn) handleTcpConnection() {
	go c.handleClientWrite()
	go c.handleServerWrite()

	<-c.ctx.Done()

	ctxErr := context.Cause(c.ctx)
	log.With("sid", c.sid, "conn", c.id).Infof("postgres connection closed, reason=%v", ctxErr)
	err := c.streamClient.Send(&pb.Packet{
		Type: pbagent.SessionClose,
		Spec: map[string][]byte{
			pb.SpecGatewaySessionID: []byte(c.sid),
		},
	})
	if err != nil {
		log.With("sid", c.sid, "conn", c.id).Warnf("failed sending session close packet, err=%v", err)
	}

	// propagate any errors to the underline client connection
	if ctxErr != nil {
		_, _ = c.Write(pgtypes.NewFatalError(ctxErr.Error()).Encode())
	}

	// wait 2 seconds for cleaning up session gracefully
	time.Sleep(time.Second * 2)
	_, _ = c.streamClient.Close()
	_ = c.Conn.Close()
}

func (c *postgresConn) handleClientWrite() {
	openSessionPacket := &pb.Packet{
		Type: pbagent.SessionOpen,
		Spec: map[string][]byte{
			pb.SpecGatewaySessionID:   []byte(c.sid),
			pb.SpecClientConnectionID: []byte(c.id),
		},
	}

	if err := c.streamClient.Send(openSessionPacket); err != nil {
		c.cancelFn("failed sending open session packet, err=%v", err)
		return
	}
	for {
		pkt, err := c.streamClient.Recv()
		if err != nil {
			c.cancelFn("received error processing stream client, err=%v", err)
			return
		}
		if pkt == nil {
			c.cancelFn("received nil packet, closing connection")
			return
		}

		switch pb.PacketType(pkt.Type) {
		case pbclient.SessionOpenOK:
			log.With("sid", c.sid, "conn", c.id).Infof("session opened successfully")
			connectionType := pb.ConnectionType(pkt.Spec[pb.SpecConnectionType])
			if connectionType != pb.ConnectionTypePostgres {
				c.cancelFn("unsupported connection type, got=%v", connectionType)
				return
			}
			err = c.streamClient.Send(&pb.Packet{
				Type:    pbagent.PGConnectionWrite,
				Payload: c.initialPacket,
				Spec: map[string][]byte{
					pb.SpecGatewaySessionID:   []byte(c.sid),
					pb.SpecClientConnectionID: []byte(c.id),
				},
			})
			if err != nil {
				c.cancelFn("failed sending postgres packet to stream client, err=%v", err)
				return
			}
		case pbclient.PGConnectionWrite:
			if _, err := c.Write(pkt.Payload); err != nil {
				c.cancelFn("failed writing postgres packet to client, err=%v", err)
				return
			}
		case pbclient.TCPConnectionClose, pbclient.SessionClose:
			log.With("sid", c.sid, "conn", c.id).Infof("closing session")
			c.cancelFn("connection closed by server, payload=%v", string(pkt.Payload))
			return
		default:
			c.cancelFn("received invalid packet type %T", pkt.Type)
			return
		}
	}
}

func (c *postgresConn) handleServerWrite() {
	for {
		pkt, err := pgtypes.Decode(c.Conn)
		if err != nil {
			defer c.cancelFn("received error processing server write, err=%v", err)
			if err == io.EOF {
				return
			}
			return
		}
		err = c.streamClient.Send(&pb.Packet{
			Type:    pbagent.PGConnectionWrite,
			Payload: pkt.Encode(),
			Spec: map[string][]byte{
				pb.SpecGatewaySessionID:   []byte(c.sid),
				pb.SpecClientConnectionID: []byte(c.id),
			},
		})
		if err != nil {
			c.cancelFn("failed sending packet to stream client, err=%v", err)
			return
		}
	}
}

// validateClientTLSConn checks if the client connection requests TLS
// and whether it aligns with the server's TLS configuration.
func validateClientTLSConn(pkt *pgtypes.Packet, tlsConfig *tls.Config) (bool, error) {
	isFrontendTLSRequest := pkt.IsFrontendSSLRequest()
	if tlsConfig == nil && isFrontendTLSRequest {
		return isFrontendTLSRequest, fmt.Errorf("hoop server proxy is not enforcing TLS. The client must establish connection without TLS")
	}

	if tlsConfig != nil && !isFrontendTLSRequest {
		return isFrontendTLSRequest, fmt.Errorf("hoop server proxy is enforcing TLS. The client must establish connection with TLS")
	}
	return isFrontendTLSRequest, nil
}
