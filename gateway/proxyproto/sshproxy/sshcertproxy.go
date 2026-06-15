package sshproxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/grpc"
	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/proxyproto/grpckey"
	"github.com/hoophq/hoop/gateway/proxyproto/sshproxy/clientproto"
	"golang.org/x/crypto/ssh"
)

type certServer struct {
	listenAddress       string
	connectionStore     sync.Map
	pendingCertSessions sync.Map
	listener            net.Listener
	hostKey             ssh.Signer
	certChecker         *ssh.CertChecker
	userMapping         UserMapping
}

func (s *certServer) stop() error {
	s.connectionStore.Range(func(key, value any) bool {
		if conn, ok := value.(*certConnection); ok {
			conn.cancelFn("proxy server is shutting down")
		}
		return true
	})
	if s.listener != nil {
		log.Infof("stopping ssh cert server at %v", s.listener.Addr().String())
		_ = s.listener.Close()
	}
	return nil
}

func runCertServer(listenAddr string, hostKey ssh.Signer, certChecker *ssh.CertChecker, userMapping UserMapping) (*certServer, error) {
	lis, err := net.Listen("tcp4", listenAddr)
	if err != nil {
		return nil, fmt.Errorf("failed listening to address %v, err=%v", listenAddr, err)
	}

	log.Infof("starting ssh cert server at %v", listenAddr)

	server := &certServer{
		listenAddress: listenAddr,
		listener:      lis,
		hostKey:       hostKey,
		certChecker:   certChecker,
		userMapping:   userMapping,
	}

	go func() {
		defer lis.Close()
		connectionCounter := 0
		for {
			connectionCounter++
			connectionID := strconv.Itoa(connectionCounter)

			netConn, err := lis.Accept()
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					log.Info("cert ssh proxy listener closed")
					return
				}
				log.With("conn", connectionID).Warnf("failed obtaining tcp connection, err=%v", err)
				break
			}

			sessionID := uuid.NewString()
			conn, err := newCertConnection(sessionID, connectionID, netConn, server)
			if err != nil {
				if err == io.EOF {
					log.With("sid", sessionID, "conn", connectionID).
						Debugf("failed creating new SSH connection, reason=%v", err)
					_ = netConn.Close()
					continue
				}
				log.With("sid", sessionID, "conn", connectionID).
					Warnf("failed creating new cert SSH connection, reason=%v", err)
				_ = netConn.Close()
				continue
			}

			server.connectionStore.Store(sessionID, conn)
			go func() {
				defer server.connectionStore.Delete(sessionID)
				conn.handle()
			}()
		}
	}()

	return server, nil
}

type certConnection struct {
	id           string
	sid          string
	ctx          context.Context
	cancelFn     func(msg string, a ...any)
	proto        *clientproto.Session
	certGrpcOnce sync.Once
	certSession  *certSession
	newChannelCh <-chan ssh.NewChannel
	sshConn      *ssh.ServerConn
}

func newCertConnection(sid, connID string, conn net.Conn, server *certServer) (*certConnection, error) {
	sshCfg := &ssh.ServerConfig{
		PublicKeyCallback: func(c ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			cert, ok := key.(*ssh.Certificate)
			if !ok {
				return nil, fmt.Errorf("only certificate-based public key authentication is accepted")
			}

			if _, err := server.certChecker.Authenticate(c, cert); err != nil {
				log.With("sid", sid).Infof("cert auth failed for user=%v: %v", c.User(), err)
				return nil, fmt.Errorf("certificate verification failed: %w", err)
			}

			if len(cert.ValidPrincipals) == 0 && server.userMapping.CertAttr != "key_id" {
				return nil, fmt.Errorf("certificate has no principals")
			}

			log.With("sid", sid).Infof("cert auth accepted: user=%v key-id=%q serial=%d principals=%v",
				c.User(), cert.KeyId, cert.Serial, cert.ValidPrincipals)

			server.pendingCertSessions.Store(string(c.SessionID()), &certSession{cert: cert})
			return &ssh.Permissions{Extensions: map[string]string{"hoop-auth-method": "cert"}}, nil
		},
	}
	sshCfg.AddHostKey(server.hostKey)

	sshConn, clientNewCh, sshReq, err := ssh.NewServerConn(conn, sshCfg)
	if err != nil {
		if err == io.EOF {
			return nil, io.EOF
		}
		return nil, fmt.Errorf("failed establishing SSH connection: %v", err)
	}
	go ssh.DiscardRequests(sshReq)

	if sshConn.Permissions == nil {
		return nil, fmt.Errorf("missing ssh permissions after authentication")
	}

	sessAny, ok := server.pendingCertSessions.LoadAndDelete(string(sshConn.SessionID()))
	if !ok {
		return nil, fmt.Errorf("missing cert session state after handshake (sid=%s)", sid)
	}
	sess := sessAny.(*certSession)

	user, matchedValue, err := lookupUserByCert(sess.cert, server.userMapping)
	if err != nil {
		return nil, fmt.Errorf("cert auth user lookup failed: %w", err)
	}
	if user.Status != "active" {
		return nil, fmt.Errorf("user %q is not active (status=%s)", matchedValue, user.Status)
	}
	sess.matchedValue = matchedValue
	sess.userSubject = user.Subject
	sess.orgID = user.OrgID

	ctxDuration := 24 * time.Hour
	if sess.cert.ValidBefore != ssh.CertTimeInfinity {
		expiry := time.Unix(int64(sess.cert.ValidBefore), 0)
		ctxDuration = time.Until(expiry)
		if ctxDuration <= 0 {
			return nil, fmt.Errorf("certificate has already expired (matched=%s)", matchedValue)
		}
	}

	log.With("sid", sid, "remote-addr", conn.RemoteAddr()).
		Infof("cert auth: matched=%v user=%v expires-in=%v",
			matchedValue, user.Subject, ctxDuration.Truncate(time.Second))

	ctx, cancelFn := context.WithCancelCause(context.Background())
	ctx, timeoutCancelFn := context.WithTimeoutCause(ctx, ctxDuration,
		fmt.Errorf("certificate expired, resourceid=%v", connID))

	return &certConnection{
		id:  connID,
		sid: sid,
		ctx: ctx,
		cancelFn: func(msg string, a ...any) {
			cancelFn(fmt.Errorf(msg, a...))
			timeoutCancelFn()
		},
		certSession:  sess,
		sshConn:      sshConn,
		newChannelCh: clientNewCh,
	}, nil
}

func (c *certConnection) handle() {
	go c.acceptChannels()
	<-c.ctx.Done()

	ctxErr := context.Cause(c.ctx)
	log.With("sid", c.sid, "conn", c.id).Infof("ssh cert connection closed, reason=%v", ctxErr)

	hasUserError := false
	if ctxErr != nil && c.proto != nil {
		if msg := translateUpstreamError(ctxErr.Error()); msg != "" {
			notifyOpenChannels(c.proto.RangeChannels, msg)
			hasUserError = true
		}
	}

	if !hasUserError && c.proto != nil {
		flushDone := make(chan struct{})
		go func() {
			c.proto.Wait()
			close(flushDone)
		}()
		select {
		case <-flushDone:
		case <-time.After(5 * time.Second):
			log.With("sid", c.sid, "conn", c.id).Warnf("timed out waiting for channel goroutines to finish")
		}
	}

	if c.proto != nil {
		if err := c.proto.SendClose(); err != nil {
			log.With("sid", c.sid, "conn", c.id).Warnf("failed sending session close packet, err=%v", err)
		} else if !hasUserError {
			time.Sleep(time.Second * 2)
		}
		_, _ = c.proto.GRPCClient().Close()
	}
	_ = c.sshConn.Close()
}

func (c *certConnection) acceptChannels() {
	select {
	case <-c.ctx.Done():
		return
	default:
	}

	channelID := uint16(0)
	for newCh := range c.newChannelCh {
		channelID++
		log.With("conn", c.id, "sid", c.sid, "ch", channelID).
			Infof("received new cert channel, type=%v", newCh.ChannelType())
		go c.handleChannel(newCh, channelID)
	}
	c.cancelFn("ssh client disconnected")
}

func (c *certConnection) handleChannel(newCh ssh.NewChannel, channelID uint16) {
	reason, err := c.validateChannel(newCh, channelID)
	if err != nil {
		_ = newCh.Reject(reason, err.Error())
		return
	}
	if err := c.proto.AcceptAndServeChannel(newCh, channelID); err != nil {
		c.cancelFn("cert: failed handling channel, err=%v", err)
	}
}

// validateChannel enforces cert-auth constraints and establishes the
// session-level gRPC connection on the first call. Returns a rejection reason
// and error when the channel must be refused; both are zero on success.
func (c *certConnection) validateChannel(newCh ssh.NewChannel, channelID uint16) (ssh.RejectionReason, error) {
	if newCh.ChannelType() != "direct-tcpip" {
		log.With("conn", c.id, "sid", c.sid, "ch", channelID).
			Infof("rejected %q channel: cert auth only supports port-forwarding (matched=%s)",
				newCh.ChannelType(), c.certSession.matchedValue)
		return ssh.Prohibited,
			fmt.Errorf("hoop: certificate authentication only supports port-forwarding; use ssh -L to connect")
	}

	if !c.certSession.allowPortForwarding() {
		log.With("conn", c.id, "sid", c.sid, "ch", channelID).
			Infof("denied direct-tcpip: cert lacks permit-port-forwarding (matched=%s)", c.certSession.matchedValue)
		return ssh.Prohibited, fmt.Errorf("hoop: cert does not permit port forwarding")
	}

	var dest struct {
		ConnectedHost  string
		ConnectedPort  uint32
		OriginatorIP   string
		OriginatorPort uint32
	}
	if err := ssh.Unmarshal(newCh.ExtraData(), &dest); err != nil || dest.ConnectedHost == "" {
		return ssh.ConnectionFailed, fmt.Errorf("hoop: invalid or missing port-forward destination")
	}
	connectionName := dest.ConnectedHost

	if _, err := models.GetConnectionByOrgAndName(c.certSession.orgID, connectionName); err != nil {
		if err == models.ErrNotFound {
			log.With("conn", c.id, "sid", c.sid, "ch", channelID).
				Infof("cert port-forward: connection %q not found (org=%s matched=%s)",
					connectionName, c.certSession.orgID, c.certSession.matchedValue)
			return ssh.ConnectionFailed, fmt.Errorf("hoop: connection %q not found", connectionName)
		}
		log.With("conn", c.id, "sid", c.sid, "ch", channelID).
			Warnf("cert port-forward: failed looking up connection %q: %v", connectionName, err)
		return ssh.ConnectionFailed, fmt.Errorf("hoop: failed looking up connection %q", connectionName)
	}

	log.Infof("cert auth: received direct-tcpip for connection %q (matched=%s)", connectionName, c.certSession.matchedValue)

	// Establish the session-level gRPC connection on the first direct-tcpip
	// channel. sync.Once ensures this runs exactly once per SSH connection;
	// subsequent channels reuse the same gRPC session.
	c.certGrpcOnce.Do(func() {
		grpcOpts := []*grpc.ClientOptions{
			grpc.WithOption(grpc.OptionConnectionName, connectionName),
			grpc.WithOption(grpckey.ImpersonateAuthKeyHeaderKey, grpckey.ImpersonateSecretKey),
			grpc.WithOption(grpckey.ImpersonateUserSubjectHeaderKey, c.certSession.userSubject),
			grpc.WithOption("origin", pb.ConnectionOriginClient),
			grpc.WithOption("verb", pb.ClientVerbConnect),
			grpc.WithOption("session-id", c.sid),
		}
		grpcClient, connErr := grpc.Connect(grpc.ClientConfig{
			ServerAddress: grpc.LocalhostAddr,
			UserAgent:     "ssh/grpc",
			Insecure:      appconfig.Get().GatewayUseTLS() == false,
			TLSCA:         appconfig.Get().GrpcClientTLSCa(),
			TLSSkipVerify: true,
		}, grpcOpts...)
		if connErr != nil {
			c.cancelFn("cert auth: failed to connect to grpc for connection %q: %v", connectionName, connErr)
			return
		}
		c.proto = clientproto.New(c.sid, c.id, grpcClient, c.ctx, c.cancelFn)
		c.proto.Open()
	})

	select {
	case <-c.ctx.Done():
		return ssh.ConnectionFailed,
			fmt.Errorf("hoop: connection %q not found or not accessible", connectionName)
	default:
	}

	log.With("conn", c.id, "sid", c.sid, "ch", channelID).
		Infof("cert port-forward: connection=%q matched=%s conntype=%s",
			connectionName, c.certSession.matchedValue, c.proto.ConnectionType())
	return 0, nil
}
