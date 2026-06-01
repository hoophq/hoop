package sshproxy

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	sshtypes "libhoop/proxy/ssh/types"
	"net"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/grpc"
	"github.com/hoophq/hoop/common/keys"
	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	pbclient "github.com/hoophq/hoop/common/proto/client"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/idp"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/proxyproto/grpckey"
	"github.com/hoophq/hoop/gateway/transport/usertoken"
	"golang.org/x/crypto/ssh"
)

// from syscall.SIGWINCH, avoid syscall errors when compiling on Windows
const SIGWINCH = syscall.Signal(0x1c)
const instanceKey = "ssh_server"

var instanceStore sync.Map

// ServerConfig holds all configuration required to start the SSH proxy server.
type ServerConfig struct {
	ListenAddress string
	// HostsKey is the base64-encoded PEM private key used as the SSH host key.
	HostsKey string
	// TrustedCAs is the list of trusted SSH CA public keys in authorized_keys
	// format. When non-empty, the server accepts SSH certificate authentication
	// in addition to password (secret-key) authentication.
	TrustedCAs []string
	// UserMapping specifies which certificate attribute (principal or key_id) is
	// matched against which user table column (email, subject, user_id).
	// Required when TrustedCAs is non-empty.
	UserMapping UserMapping
}

type proxyServer struct {
	listenAddress       string
	connectionStore     sync.Map
	pendingCertSessions sync.Map // sessionID → *certSession; populated during PublicKeyCallback
	listener            net.Listener
	hostKey             ssh.Signer
	certChecker         *ssh.CertChecker // nil when cert auth is not configured
	userMapping         UserMapping
}

// GetServerInstance returns the singleton instance of SSHServer.
func GetServerInstance() *proxyServer {
	instance, _ := instanceStore.Load(instanceKey)
	if server, ok := instance.(*proxyServer); ok {
		return server
	}
	server := &proxyServer{}
	instanceStore.Store(instanceKey, server)
	return server
}

func (s *proxyServer) Start(cfg ServerConfig) (err error) {
	instance, _ := instanceStore.Load(instanceKey)
	if _, ok := instance.(*proxyServer); ok && s.listener != nil {
		return nil
	}

	sshHostsKey, err := parseHostsKey(cfg.HostsKey)
	if err != nil {
		return fmt.Errorf("failed parsing hosts key, reason=%v", err)
	}

	certChecker, err := buildCertChecker(cfg.TrustedCAs)
	if err != nil {
		return fmt.Errorf("failed building cert checker, reason=%v", err)
	}

	log.Infof("starting ssh server proxy at %v", cfg.ListenAddress)

	server, err := runProxyServer(cfg.ListenAddress, sshHostsKey, certChecker, cfg.UserMapping)
	if err != nil {
		return err
	}
	instanceStore.Store(instanceKey, server)
	return nil
}

// RevokeByCredentialID cancels all connections using the given credential ID.
// This triggers the same cleanup flow as when a credential expires.
func (s *proxyServer) RevokeByCredentialID(credentialID string) {
	s.connectionStore.Range(func(key, value any) bool {
		if sshConn, ok := value.(*sshConnection); ok && sshConn.credentialID == credentialID {
			sshConn.cancelFn("credential revoked")
		}
		return true
	})
}

func (s *proxyServer) Stop() error {
	instance, _ := instanceStore.LoadAndDelete(instanceKey)
	if server, ok := instance.(*proxyServer); ok {
		// cancel all active connections
		s.connectionStore.Range(func(key, value any) bool {
			if sshConn, ok := value.(*sshConnection); ok {
				sshConn.cancelFn("proxy server is shutting down")
			}
			return true
		})

		// close the listener
		if server.listener != nil {
			log.Infof("stopping ssh server proxy at %v", server.listener.Addr().String())
			_ = server.listener.Close()
		}
	}
	return nil
}

func runProxyServer(listenAddr string, hostKey ssh.Signer, certChecker *ssh.CertChecker, userMapping UserMapping) (*proxyServer, error) {
	lis, err := net.Listen("tcp4", listenAddr)
	if err != nil {
		return nil, fmt.Errorf("failed listening to address %v, err=%v", listenAddr, err)
	}
	server := &proxyServer{
		connectionStore: sync.Map{},
		listener:        lis,
		listenAddress:   listenAddr,
		hostKey:         hostKey,
		certChecker:     certChecker,
		userMapping:     userMapping,
	}

	go func() {
		defer lis.Close()
		connectionCounter := 0
		for {
			connectionCounter++

			connectionID := strconv.Itoa(connectionCounter)
			// accepts a new standard TCP connection
			netConn, err := lis.Accept()
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					log.Info("proxy server listener closed, stopping accepting new connections")
					return
				}
				log.With("conn", connectionID).Warnf("failed obtaining tcp connection, err=%v", err)
				break
			}

			// creates a new SSH connection instance
			sessionID := uuid.NewString()
			conn, err := newSSHConnection(sessionID, connectionID, netConn, server)
			if err != nil {
				// Prevents log pollution from health check requests on this port
				if err == io.EOF {
					log.With("sid", sessionID, "conn", connectionID).
						Debugf("failed creating new SSH connection, reason=%v", err)
					_ = netConn.Close()
					continue
				}
				log.With("sid", sessionID, "conn", connectionID).
					Warnf("failed creating new SSH connection, reason=%v", err)
				_ = netConn.Close()
				continue
			}

			// store the connection instance
			server.connectionStore.Store(sessionID, conn)

			go func() {
				// handle the SSH connection
				conn.handleConnection()

				// remove the connection from the store once done
				server.connectionStore.Delete(sessionID)
			}()
		}
	}()

	return server, nil
}

type pendingSSHRequest struct {
	req   *ssh.Request
	reply chan bool
}

// pendingReplyQueue holds pending SSH requests per channel in FIFO order
// so that replies from the agent are matched to the correct request (e.g. pty-req then shell).
type pendingReplyQueue struct {
	mu      sync.Mutex
	pending []*pendingSSHRequest
}

type sshConnection struct {
	id                  string
	sid                 string
	credentialID        string
	ctx                 context.Context
	cancelFn            func(msg string, a ...any)
	grpcClient          pb.ClientTransport
	clientNewSshChannel <-chan ssh.NewChannel
	sshConn             *ssh.ServerConn
	sshChannels         sync.Map
	pendingRequests     sync.Map // maps channelID (uint16) to *pendingReplyQueue
	channelWg           sync.WaitGroup
	// certSession is non-nil when the connection was authenticated via an SSH
	// certificate. It holds the verified cert and the per-connection policy
	// that governs which channel types and requests are permitted.
	certSession *certSession
}

func newSSHConnection(sid, connID string, conn net.Conn, server *proxyServer) (*sshConnection, error) {
	sshServerConfig := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			log.
				With("sid", sid).
				Infof("ssh connection attempt, user=%v, remote-addr=%v, local-addr=%v", c.User(), c.RemoteAddr(), c.LocalAddr())

			// Hash the received password (secret access key)
			secretKeyHash, err := keys.Hash256Key(string(password))
			if err != nil {
				return nil, fmt.Errorf("failed hashing secret key: %v", err)
			}

			// Retrieve the connection credentials using the hashed secret key
			dba, err := models.GetValidConnectionCredentialsBySecretKey([]string{pb.ConnectionTypeSSH.String()}, secretKeyHash)
			if err != nil {
				// Differentiate between not found and other errors
				if err == models.ErrNotFound {
					return nil, fmt.Errorf("invalid secret access key credentials")
				}
				return nil, fmt.Errorf("failed obtaining secret access key, reason=%v", err)
			}

			// Check if the credentials have expired
			if dba.ExpireAt.Before(time.Now().UTC()) {
				return nil, fmt.Errorf("invalid secret access key credentials")
			}

			// Session duration remaining based on the expiration time
			ctxDuration := dba.ExpireAt.Sub(time.Now().UTC())

			log.Infof("obtained access by id, id=%v, subject=%v, connection=%v, session_id=%v, expires-at=%v (in %v)",
				dba.ID, dba.UserSubject, dba.ConnectionName, sid,
				dba.ExpireAt.Format(time.RFC3339), ctxDuration.Truncate(time.Second).String())

			extensions := map[string]string{
				"hoop-auth-method":      "password",
				"hoop-credential-id":    dba.ID,
				"hoop-user-subject":     dba.UserSubject,
				"hoop-connection-name":  dba.ConnectionName,
				"hoop-context-duration": ctxDuration.String(),
			}
			if models.IsMachineIdentityCredential(dba.ID) {
				extensions["hoop-is-machine-credential"] = "true"
				extensions["hoop-machine-identity-org-id"] = dba.OrgID
			} else if dba.SessionID != "" {
				extensions["hoop-credential-session-id"] = dba.SessionID
			}
			return &ssh.Permissions{Extensions: extensions}, nil
		},
	}

	// When trusted CAs are configured, also accept SSH certificate authentication.
	// The certificate's first principal is matched against a Hoop user email and
	// the SSH username (conn.User()) is used as the connection name.
	if server.certChecker != nil {
		log.Info("CERT CHECK IS ON, configuring SSH SERVER to accept cert auth with trusted CAs")
		sshServerConfig.PublicKeyCallback = func(c ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			fmt.Println("VALIDATING CERTIFICATE FOR SSH CONNECTION")
			cert, ok := key.(*ssh.Certificate)
			if !ok {
				return nil, fmt.Errorf("only certificate-based public key authentication is accepted")
			}

			// Authenticate validates: cert type, CA trust (IsUserAuthority), validity
			// window, critical options (including source-address against remote IP).
			if _, err := server.certChecker.Authenticate(c, cert); err != nil {
				log.With("sid", sid).Infof("cert auth failed for user=%v: %v", c.User(), err)
				return nil, fmt.Errorf("certificate verification failed: %w", err)
			}

			if len(cert.ValidPrincipals) == 0 && server.userMapping.CertAttr != "key_id" {
				return nil, fmt.Errorf("certificate has no principals")
			}

			log.With("sid", sid).Infof("cert auth accepted: user=%v key-id=%q serial=%d principals=%v",
				c.User(), cert.KeyId, cert.Serial, cert.ValidPrincipals)

			// Stash the cert state so newCertAuthConnection can retrieve it after
			// the handshake completes. The session ID is the stable key.
			server.pendingCertSessions.Store(string(c.SessionID()), &certSession{
				cert: cert,
			})

			return &ssh.Permissions{Extensions: map[string]string{
				"hoop-auth-method": "cert",
			}}, nil
		}
	}

	// the encryption key to be used we use a single hosts key
	// used for the SSH handshake and related to the known_hosts file
	sshServerConfig.AddHostKey(server.hostKey)

	sshConn, clientNewCh, sshReq, err := ssh.NewServerConn(conn, sshServerConfig)
	if err != nil {
		if err == io.EOF {
			return nil, io.EOF
		}
		return nil, fmt.Errorf("failed establishing SSH connection: %v", err)
	}

	// discard all global out-of-band requests
	go ssh.DiscardRequests(sshReq)

	// validate permissions after authentication
	if sshConn.Permissions == nil {
		return nil, fmt.Errorf("missing ssh permissions after authentication")
	}

	authMethod := sshConn.Permissions.Extensions["hoop-auth-method"]
	if authMethod == "cert" {
		return newCertAuthConnection(sid, connID, conn, sshConn, clientNewCh, server)
	}
	return newPasswordAuthConnection(sid, connID, conn, sshConn, clientNewCh)
}

// newCertAuthConnection completes connection setup for certificate-authenticated
// connections. It retrieves the cert session stashed during PublicKeyCallback,
// looks up the Hoop user by the certificate's first principal (email), and
// derives the session lifetime from the certificate's ValidBefore field.
//
// Unlike password auth, no session-level gRPC connection is established here.
// Each port-forwarding channel (direct-tcpip) opens its own gRPC connection,
// using the channel's destination host as the Hoop connection name. Regular
// SSH session channels (shell, exec, subsystem) are rejected — certificate
// authentication is only valid for port-forwarding.
func newCertAuthConnection(sid, connID string, tcpConn net.Conn, sshConn *ssh.ServerConn, clientNewCh <-chan ssh.NewChannel, server *proxyServer) (*sshConnection, error) {
	sessAny, ok := server.pendingCertSessions.LoadAndDelete(string(sshConn.SessionID()))
	if !ok {
		return nil, fmt.Errorf("missing cert session state after handshake (sid=%s)", sid)
	}
	sess := sessAny.(*certSession)

	// Resolve the certificate to a Hoop user using the configured mapping.
	user, matchedValue, err := lookupUserByCert(sess.cert, server.userMapping)
	if err != nil {
		return nil, fmt.Errorf("cert auth user lookup failed: %w", err)
	}
	if user.Status != "active" {
		return nil, fmt.Errorf("user %q is not active (status=%s)", matchedValue, user.Status)
	}
	sess.matchedValue = matchedValue
	sess.userSubject = user.Subject

	// Derive session lifetime from the certificate's ValidBefore timestamp.
	// var ctxDuration time.Duration
	ctxDuration := 24 * time.Hour
	if sess.cert.ValidBefore != ssh.CertTimeInfinity {
		expiry := time.Unix(int64(sess.cert.ValidBefore), 0)
		ctxDuration = time.Until(expiry)
		if ctxDuration <= 0 {
			return nil, fmt.Errorf("certificate has already expired (matched=%s)", matchedValue)
		}
	}

	log.With("sid", sid, "remote-addr", tcpConn.RemoteAddr()).
		Infof("cert auth: matched=%v user=%v expires-in=%v",
			matchedValue, user.Subject, ctxDuration.Truncate(time.Second))

	ctx, cancelFn := context.WithCancelCause(context.Background())
	ctx, timeoutCancelFn := context.WithTimeoutCause(ctx, ctxDuration,
		fmt.Errorf("certificate expired, resourceid=%v", connID))
	sessionConn := &sshConnection{
		id:  connID,
		sid: sid,
		ctx: ctx,
		cancelFn: func(msg string, a ...any) {
			cancelFn(fmt.Errorf(msg, a...))
			timeoutCancelFn()
		},
		sshConn:             sshConn,
		grpcClient:          nil,
		clientNewSshChannel: clientNewCh,
		certSession:         sess,
	}
	return sessionConn, nil
}

// newPasswordAuthConnection completes connection setup for password-authenticated
// connections. This is the original authentication path using secret access key
// credentials stored in the database.
func newPasswordAuthConnection(sid, connID string, tcpConn net.Conn, sshConn *ssh.ServerConn, clientNewCh <-chan ssh.NewChannel) (*sshConnection, error) {
	connectionName := sshConn.Permissions.Extensions["hoop-connection-name"]
	userSubject := sshConn.Permissions.Extensions["hoop-user-subject"]
	ctxDurationStr := sshConn.Permissions.Extensions["hoop-context-duration"]
	credentialSessionID := sshConn.Permissions.Extensions["hoop-credential-session-id"]
	credentialID := sshConn.Permissions.Extensions["hoop-credential-id"]
	isMachineCredential := sshConn.Permissions.Extensions["hoop-is-machine-credential"] == "true"
	machineIdentityOrgID := sshConn.Permissions.Extensions["hoop-machine-identity-org-id"]

	if connectionName == "" || userSubject == "" {
		return nil, fmt.Errorf("missing required SSH connection attributes")
	}

	ctxDuration, err := time.ParseDuration(ctxDurationStr)
	if err != nil {
		return nil, fmt.Errorf("failed parsing context duration: %v", err)
	}

	var tokenVerifier idp.UserInfoTokenVerifier
	if !isMachineCredential {
		var err error
		tokenVerifier, _, err = idp.NewUserInfoTokenVerifierProvider()
		if err != nil {
			log.Errorf("failed to load IDP provider: %v", err)
			return nil, err
		}

		if err := usertoken.CheckUserToken(tokenVerifier, userSubject); err != nil {
			return nil, err
		}
	}

	log.
		With("sid", sid, "remote-addr", tcpConn.RemoteAddr()).
		Debugf("create new ssh connection, user=%v, connection_name=%v", userSubject, connectionName)

	grpcOpts := []*grpc.ClientOptions{
		grpc.WithOption(grpc.OptionConnectionName, connectionName),
		grpc.WithOption(grpckey.ImpersonateAuthKeyHeaderKey, grpckey.ImpersonateSecretKey),
		grpc.WithOption(grpckey.ImpersonateUserSubjectHeaderKey, userSubject),
		grpc.WithOption("origin", pb.ConnectionOriginClient),
		grpc.WithOption("verb", pb.ClientVerbConnect),
		grpc.WithOption("session-id", sid),
	}
	if isMachineCredential {
		grpcOpts = append(grpcOpts,
			grpc.WithOption(grpckey.MachineIdentityFlagHeaderKey, "true"),
			grpc.WithOption(grpckey.MachineIdentityOrgIDHeaderKey, machineIdentityOrgID),
		)
	} else if credentialSessionID != "" {
		grpcOpts = append(grpcOpts, grpc.WithOption("credential-session-id", credentialSessionID))
	}

	// connect to the gateway gRPC server
	client, err := grpc.Connect(grpc.ClientConfig{
		ServerAddress: grpc.LocalhostAddr,
		Token:         "", // it will use impersonate-auth-key as authentication
		UserAgent:     "ssh/grpc",
		Insecure:      appconfig.Get().GatewayUseTLS() == false,
		TLSCA:         appconfig.Get().GrpcClientTLSCa(),
		// it should be safe to skip verify here as we are connecting to localhost
		TLSSkipVerify: true,
	}, grpcOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed connecting to grpc server, err=%v", err)
	}

	ctx, cancelFn := context.WithCancelCause(context.Background())
	ctx, timeoutCancelFn := context.WithTimeoutCause(ctx, ctxDuration, fmt.Errorf("connection access expired, resourceid=%v", connID))
	sessionConn := &sshConnection{
		id:           connID,
		sid:          sid,
		credentialID: credentialID,
		ctx:          ctx,
		cancelFn: func(msg string, a ...any) {
			cancelFn(fmt.Errorf(msg, a...))
			timeoutCancelFn()
		},
		sshConn:             sshConn,
		grpcClient:          client,
		clientNewSshChannel: clientNewCh,
	}

	if !isMachineCredential {
		usertoken.PollingUserToken(sessionConn.ctx, func(cause error) {
			sessionConn.cancelFn(cause.Error())
		}, tokenVerifier, userSubject)
	}

	return sessionConn, nil
}

func (c *sshConnection) handleConnection() {
	// Cert-auth connections have no session-level gRPC stream. Each
	// direct-tcpip channel manages its own gRPC connection independently.
	// Skip handleClientWrite (which establishes the session-level stream) and
	// proceed directly to accepting SSH channels.
	if c.certSession != nil {
		go c.handleServerWrite()
		<-c.ctx.Done()
		c.channelWg.Wait()
		_ = c.sshConn.Close()
		return
	}

	// it is important to wait for the session to be established
	// before handling ssh channels
	c.handleClientWrite()
	go c.handleServerWrite()

	// wait for the connection to be closed
	// either by the client, server, or context cancellation
	<-c.ctx.Done()

	ctxErr := context.Cause(c.ctx)
	log.With("sid", c.sid, "conn", c.id).Infof("ssh connection closed, reason=%v", ctxErr)

	// Surface the cancellation cause to the user's terminal before
	// we tear the SSH connection down. Without this, the user sees
	// only `Connection closed by remote host` and has to ask an
	// admin to read the gateway logs to find out what went wrong.
	//
	// translateUpstreamError sanitizes the raw libhoop / agent text
	// into a fixed-vocabulary message so we don't leak internal
	// addresses, library jargon, or stack traces to end users; it
	// also returns an empty string for benign causes (e.g. the user
	// disconnected themselves) so we don't write noise.
	//
	// A non-empty message means we're tearing the session down for a
	// real error — there's no point waiting on the normal flush and
	// grace period below because the user already knows it's over.
	// We close the channels inside notifyOpenChannels and skip
	// straight to closing the SSH transport.
	hasUserError := false
	if ctxErr != nil {
		if msg := translateUpstreamError(ctxErr.Error()); msg != "" {
			notifyOpenChannels(&c.sshChannels, msg)
			hasUserError = true
		}
	}

	if !hasUserError {
		// Normal (non-error) teardown: wait for all channel
		// data-forwarding goroutines to flush remaining writes
		// to the gRPC stream. Without this, SessionClose can be
		// sent before trailing data packets, causing the agent
		// to tear down the session prematurely.
		flushDone := make(chan struct{})
		go func() {
			c.channelWg.Wait()
			close(flushDone)
		}()
		select {
		case <-flushDone:
		case <-time.After(5 * time.Second):
			log.With("sid", c.sid, "conn", c.id).Warnf("timed out waiting for channel goroutines to finish")
		}
	}

	// notify the server that the session is closing
	err := c.grpcClient.Send(&pb.Packet{
		Type: pbagent.SessionClose,
		Spec: map[string][]byte{
			pb.SpecGatewaySessionID: []byte(c.sid),
		},
	})
	if err != nil {
		log.With("sid", c.sid, "conn", c.id).Warnf("failed sending session close packet, err=%v", err)
		return
	}

	// On normal teardown, give the agent 2 seconds to drain its
	// state before we hang up the transport. On error teardowns the
	// user has nothing else to see and we already closed the
	// channels in notifyOpenChannels, so close immediately.
	if !hasUserError {
		time.Sleep(time.Second * 2)
	}
	_ = c.sshConn.Close()
	_, _ = c.grpcClient.Close()
}

func (c *sshConnection) handleClientWrite() {
	// send the open session packet to the server
	err := c.grpcClient.Send(&pb.Packet{
		Type: pbagent.SessionOpen,
		Spec: map[string][]byte{
			pb.SpecGatewaySessionID:   []byte(c.sid),
			pb.SpecClientConnectionID: []byte(c.id),
		},
	})
	if err != nil {
		c.cancelFn("failed sending open session packet, err=%v", err)
		return
	}

	startupCh := make(chan struct{})
	// listen for incoming packets from the gRPC server
	go func() {
		// always send startup signal when the control loop ends
		// to ensure it doesn't get stuck until it reaches the open session timeout
		defer func() { startupCh <- struct{}{}; close(startupCh) }()
		for {
			pkt, err := c.grpcClient.Recv()
			if err != nil {
				c.cancelFn("received error processing grpc client, err=%v", err)
				return
			}
			if pkt == nil {
				c.cancelFn("received nil packet, closing connection")
				return
			}

			switch pb.PacketType(pkt.Type) {
			case pbclient.SessionOpenOK:
				log.With("sid", c.sid).Infof("session opened successfully")
				startupCh <- struct{}{}

			case pbclient.SSHConnectionWrite:
				switch sshtypes.DecodeType(pkt.Payload) {
				case sshtypes.DataType:
					var data sshtypes.Data
					if err := sshtypes.Decode(pkt.Payload, &data); err != nil {
						c.cancelFn("failed decoding ssh data, err=%v", err)
						return
					}
					connWrapperObj, _ := c.sshChannels.Load(fmt.Sprintf("%v", data.ChannelID))
					clientCh, ok := connWrapperObj.(io.WriteCloser)
					if !ok {
						c.cancelFn("local channel %q not found", data.ChannelID)
						return
					}
					log.With("sid", c.sid, "ch", data.ChannelID, "conn", c.id).Debugf("received data type")
					if _, err := clientCh.Write(data.Payload); err != nil {
						c.cancelFn("failed writing ssh data, err=%v", err)
						return
					}

				case sshtypes.CloseChannelType:
					var cc sshtypes.CloseChannel
					if err := sshtypes.Decode(pkt.Payload, &cc); err != nil {
						c.cancelFn("failed decoding close channel, err=%v", err)
						return
					}

					obj, _ := c.sshChannels.Load(fmt.Sprintf("%v", cc.ID))
					if clientCh, ok := obj.(io.Closer); ok {
						err := clientCh.Close()

						log.
							With("sid", c.sid, "ch", cc.ID, "conn", c.id).
							Debugf("closing client ssh channel type=%v, err=%v", cc.Type, err)
					}

				case sshtypes.SSHRequestReplyType:
					var reply sshtypes.SSHRequestReply
					if err := sshtypes.Decode(pkt.Payload, &reply); err != nil {
						c.cancelFn("failed decoding ssh request reply, err=%v", err)
						return
					}
					log.With("sid", c.sid, "ch", reply.ChannelID, "conn", c.id).
						Debugf("received ssh request reply, ok=%v", reply.OK)

					// Forward the reply to the next pending client request (FIFO per channel)
					queue := c.loadPendingReplyQueue(reply.ChannelID)
					if queue == nil {
						log.With("sid", c.sid, "ch", reply.ChannelID, "conn", c.id).
							Infof("pending reply queue missing or invalid, dropping reply")
					} else {
						queue.mu.Lock()
						if len(queue.pending) > 0 {
							pendingReq := queue.pending[0]
							queue.pending = queue.pending[1:]
							queue.mu.Unlock()
							select {
							case pendingReq.reply <- reply.OK:
								log.With("sid", c.sid, "ch", reply.ChannelID, "conn", c.id).
									Debugf("forwarded ssh request reply to client")
							case <-c.ctx.Done():
								return
							default:
								log.With("sid", c.sid, "ch", reply.ChannelID, "conn", c.id).Infof("channel full or already handled (e.g. timeout), dropping request")
							}
						} else {
							queue.mu.Unlock()
						}
					}

				case sshtypes.ServerSSHRequestType:
					var serverReq sshtypes.ServerSSHRequest
					if err := sshtypes.Decode(pkt.Payload, &serverReq); err != nil {
						c.cancelFn("failed decoding server ssh request, err=%v", err)
						return
					}
					log.With("sid", c.sid, "ch", serverReq.ChannelID, "conn", c.id).
						Debugf("received server ssh request type=%q, wantreply=%v, payload-length=%v",
							serverReq.RequestType, serverReq.WantReply, len(serverReq.Payload))

					// Forward the request to the client channel
					connWrapperObj, _ := c.sshChannels.Load(fmt.Sprintf("%v", serverReq.ChannelID))
					clientCh, ok := connWrapperObj.(ssh.Channel)
					if !ok {
						log.With("sid", c.sid, "ch", serverReq.ChannelID, "conn", c.id).
							Warnf("local channel not found for server request")
						continue
					}
					// Send the request to the client (e.g., exit-status)
					_, err := clientCh.SendRequest(serverReq.RequestType, serverReq.WantReply, serverReq.Payload)
					if err != nil {
						log.With("sid", c.sid, "ch", serverReq.ChannelID, "conn", c.id).
							Debugf("failed sending server request to client: %v", err)
					}

				case sshtypes.EOFType:
					var eof sshtypes.EOF
					if err := sshtypes.Decode(pkt.Payload, &eof); err != nil {
						log.With("sid", c.sid, "ch", eof.ChannelID, "conn", c.id).
							Infof("failed decoding ssh eof, err=%v", err)
						c.cancelFn("failed decoding ssh eof, err=%v", err)
						return
					}
					obj, _ := c.sshChannels.Load(fmt.Sprintf("%v", eof.ChannelID))
					if ch, ok := obj.(interface{ CloseWrite() error }); ok {
						_ = ch.CloseWrite()
					}

				default:
					c.cancelFn("received unknown ssh message type (%v)", sshtypes.DecodeType(pkt.Payload))
					return
				}

			case pbclient.SessionOpenWaitingApproval:
				c.cancelFn("session with review are not implemented yet, closing connection")
				return

			case pbclient.TCPConnectionClose, pbclient.SessionClose:
				c.cancelFn("connection closed by server, payload=%v", string(pkt.Payload))
				return

			default:
				c.cancelFn(`received invalid packet type %T`, pkt.Type)
				return
			}
		}
	}()

	openSessionReadyTimeout, cancelFn := context.WithTimeout(context.Background(), time.Second*5)
	defer cancelFn()

	select {
	case <-startupCh:
	case <-openSessionReadyTimeout.Done():
		c.cancelFn("session timed out before it was ready")
	}
}

// loadPendingReplyQueue returns the pending reply queue for the channel, or nil if missing or invalid.
func (c *sshConnection) loadPendingReplyQueue(channelID uint16) *pendingReplyQueue {
	queueObj, ok := c.pendingRequests.Load(channelID)
	if !ok {
		return nil
	}
	queue, ok := queueObj.(*pendingReplyQueue)
	if !ok || queue == nil {
		return nil
	}
	return queue
}

func (c *sshConnection) handleServerWrite() {
	// do not continue if the context is already done
	select {
	case <-c.ctx.Done():
		return
	default:
	}

	channelID := uint16(0)
	for newCh := range c.clientNewSshChannel {
		channelID++
		go c.handleChannel(newCh, channelID)
	}
	// The SSH client disconnected (clientNewSshChannel was closed).
	// Cancel the context so handleConnection proceeds to close the gRPC stream
	// and the SSH TCP connection. Without this, handleConnection blocks forever
	// at <-c.ctx.Done() and c.sshConn.Close() is never called, causing the
	// client-side ProxyCommand to hang waiting for the TCP FIN.
	c.cancelFn("ssh client disconnected")
}

func (c *sshConnection) handleChannel(newCh ssh.NewChannel, channelID uint16) {
	log.With("conn", c.id, "sid", c.sid, "ch", channelID).Infof("received new channel, type=%v", newCh.ChannelType())

	// Cert-auth connections only support port-forwarding. Route each channel
	// type accordingly and return; the password-auth path below is skipped.
	if c.certSession != nil {
		switch newCh.ChannelType() {
		case "direct-tcpip":
			if !c.certSession.allowPortForwarding() {
				log.With("conn", c.id, "sid", c.sid, "ch", channelID).
					Infof("denied direct-tcpip: cert lacks permit-port-forwarding (principal=%s)", c.certSession.matchedValue)
				_ = newCh.Reject(ssh.Prohibited, "hoop: cert does not permit port forwarding")
				return
			}
			c.handleCertDirectTcpip(newCh, channelID)
		default:
			log.With("conn", c.id, "sid", c.sid, "ch", channelID).
				Infof("rejected %q channel: cert auth only supports port-forwarding (principal=%s)",
					newCh.ChannelType(), c.certSession.matchedValue)
			_ = newCh.Reject(ssh.Prohibited,
				"hoop: certificate authentication only supports port-forwarding; use ssh -L to connect")
		}
		return
	}

	clientCh, clientRequests, err := newCh.Accept()
	if err != nil {
		c.cancelFn("failed obtaining channel, err=%v", err)
		return
	}

	c.sshChannels.Store(fmt.Sprintf("%v", channelID), clientCh)

	streamW := pb.NewStreamWriter(c.grpcClient, pbagent.SSHConnectionWrite, map[string][]byte{
		string(pb.SpecGatewaySessionID):   []byte(c.sid),
		string(pb.SpecClientConnectionID): []byte(c.id),
	})

	openChData := (sshtypes.OpenChannel{
		ChannelID:        channelID,
		ChannelType:      newCh.ChannelType(),
		ChannelExtraData: newCh.ExtraData(),
	}).Encode()

	if _, err := streamW.Write(openChData); err != nil {
		c.cancelFn("failed writing open channel to stream, err=%v", err)
		return
	}

	// Copy data from client to agent. Note: We don't close clientCh here because
	// the client may still be waiting to receive data (e.g., git clone sends a command
	// then waits for the response). The channel will be closed when we receive
	// a CloseChannel message from the agent.
	c.channelWg.Add(1)
	go func() {
		defer c.channelWg.Done()
		buf := make([]byte, 32*1024)
		for {
			n, readErr := clientCh.Read(buf)
			if n > 0 {
				log.With("sid", c.sid, "conn", c.id, "ch", channelID).
					Debugf("read %d bytes from client, forwarding to agent", n)
				data := sshtypes.Data{ChannelID: channelID, Payload: buf[:n]}
				if _, writeErr := streamW.Write(data.Encode()); writeErr != nil {
					c.cancelFn("failed writing client data to agent, err=%v", writeErr)
					return
				}
			}
			if readErr != nil {
				if readErr != io.EOF {
					log.With("sid", c.sid, "conn", c.id, "ch", channelID).
						Debugf("error reading from client: %v", readErr)
				} else {
					log.With("sid", c.sid, "conn", c.id, "ch", channelID).
						Debugf("client closed write side (EOF), sending EOF to agent")
					// Signal to the agent that the client has closed its write side
					eofData := sshtypes.EOF{ChannelID: channelID}
					if _, writeErr := streamW.Write(eofData.Encode()); writeErr != nil {
						log.With("sid", c.sid, "conn", c.id, "ch", channelID).
							Debugf("failed sending EOF to agent: %v", writeErr)
					}
				}
				break
			}
		}
	}()

	// handle incoming requests from the client
	c.channelWg.Add(1)
	go func() {
		defer c.channelWg.Done()
		for req := range clientRequests {
			log.With("sid", c.sid, "conn", c.id, "ch", channelID, "type", req.Type).Debug("received client ssh request")

			// Enforce cert extensions and critical options on channel requests
			// before forwarding.
			if c.certSession != nil {
				var denied bool
				switch req.Type {
				case "pty-req":
					if !c.certSession.allowPTY() {
						log.With("sid", c.sid, "conn", c.id, "ch", channelID).
							Infof("denied pty-req: cert lacks permit-pty (principal=%s)", c.certSession.matchedValue)
						denied = true
					}
				case "x11-req":
					if !c.certSession.allowX11Forwarding() {
						log.With("sid", c.sid, "conn", c.id, "ch", channelID).
							Infof("denied x11-req: cert lacks permit-X11-forwarding (principal=%s)", c.certSession.matchedValue)
						denied = true
					}
				case "auth-agent-req@openssh.com":
					if !c.certSession.allowAgentForwarding() {
						log.With("sid", c.sid, "conn", c.id, "ch", channelID).
							Infof("denied auth-agent-req: cert lacks permit-agent-forwarding (principal=%s)", c.certSession.matchedValue)
						denied = true
					}
				case "exec":
					if fc := c.certSession.forceCommand(); fc != "" {
						log.With("sid", c.sid, "conn", c.id, "ch", channelID).
							Infof("force-command applied: %q (principal=%s)", fc, c.certSession.matchedValue)
						req.Payload = ssh.Marshal(struct{ Command string }{fc})
					}
				case "shell":
					if fc := c.certSession.forceCommand(); fc != "" {
						log.With("sid", c.sid, "conn", c.id, "ch", channelID).
							Infof("force-command applied (shell→exec): %q (principal=%s)", fc, c.certSession.matchedValue)
						req.Type = "exec"
						req.Payload = ssh.Marshal(struct{ Command string }{fc})
					}
				case "subsystem":
					if fc := c.certSession.forceCommand(); fc != "" {
						log.With("sid", c.sid, "conn", c.id, "ch", channelID).
							Infof("denied subsystem: force-command is set (principal=%s)", c.certSession.matchedValue)
						denied = true
					}
				}
				if denied {
					if req.WantReply {
						_ = req.Reply(false, nil)
					}
					continue
				}
			}

			data := (sshtypes.SSHRequest{
				ChannelID:   channelID,
				RequestType: req.Type,
				WantReply:   req.WantReply,
				Payload:     req.Payload,
			}).Encode()

			// send the request to the server
			_, err := streamW.Write(data)
			if err != nil {
				c.cancelFn("failed writing to stream, err=%v", err)
				return
			}

			// respond to the request if needed
			if req.WantReply {
				replyChan := make(chan bool, 1)
				pendingReq := &pendingSSHRequest{
					req:   req,
					reply: replyChan,
				}
				v, _ := c.pendingRequests.LoadOrStore(channelID, &pendingReplyQueue{})
				queue, ok := v.(*pendingReplyQueue)
				if !ok || queue == nil {
					log.With("sid", c.sid, "conn", c.id, "ch", channelID).Warnf("pending reply queue missing or invalid, skipping")
					continue
				}
				queue.mu.Lock()
				queue.pending = append(queue.pending, pendingReq)
				queue.mu.Unlock()

				// Wait for the agent's reply in a separate goroutine to avoid blocking
				go func(clientReq *ssh.Request, chID uint16) {
					select {
					case <-c.ctx.Done():
						return
					case ok := <-replyChan:
						if err := clientReq.Reply(ok, nil); err != nil {
							log.With("sid", c.sid, "conn", c.id, "ch", chID, "type", clientReq.Type).
								Debugf("failed sending response to channel, err=%v", err)
						}
					case <-time.After(5 * time.Second):
						// Timeout waiting for agent reply, assume failure
						if err := clientReq.Reply(false, nil); err != nil {
							log.With("sid", c.sid, "conn", c.id, "ch", chID, "type", clientReq.Type).
								Debugf("failed sending response to channel (timeout), err=%v", err)
						}
					}
				}(req, channelID)
			}
		}

		log.With("ch", channelID, "conn", c.id).Debugf("done processing ssh client requests")
	}()
}

// directTcpipExtra is the wire format of the extra data carried by a
// direct-tcpip channel open request (RFC 4254 §7.2).
type directTcpipExtra struct {
	ConnectedHost  string
	ConnectedPort  uint32
	OriginatorIP   string
	OriginatorPort uint32
}

// handleCertDirectTcpip handles one direct-tcpip channel for a cert-auth
// connection. It derives the Hoop connection name from the channel's
// destination host, establishes a dedicated per-channel gRPC session, and
// proxies data bidirectionally. The channel is rejected before Accept is
// called if the connection cannot be found or accessed, so the SSH client
// receives a protocol-level rejection rather than a silent close.
func (c *sshConnection) handleCertDirectTcpip(newCh ssh.NewChannel, channelID uint16) {
	var dest directTcpipExtra
	if err := ssh.Unmarshal(newCh.ExtraData(), &dest); err != nil || dest.ConnectedHost == "" {
		_ = newCh.Reject(ssh.ConnectionFailed, "hoop: invalid or missing port-forward destination")
		return
	}
	connectionName := dest.ConnectedHost

	// Each direct-tcpip channel is an independent Hoop session.
	channelSID := uuid.New().String()
	channelConnID := fmt.Sprintf("%s-%d", c.id, channelID)

	grpcOpts := []*grpc.ClientOptions{
		grpc.WithOption(grpc.OptionConnectionName, connectionName),
		grpc.WithOption(grpckey.ImpersonateAuthKeyHeaderKey, grpckey.ImpersonateSecretKey),
		grpc.WithOption(grpckey.ImpersonateUserSubjectHeaderKey, c.certSession.userSubject),
		grpc.WithOption("origin", pb.ConnectionOriginClient),
		grpc.WithOption("verb", pb.ClientVerbConnect),
		grpc.WithOption("session-id", channelSID),
	}
	grpcClient, err := grpc.Connect(grpc.ClientConfig{
		ServerAddress: grpc.LocalhostAddr,
		UserAgent:     "ssh/grpc",
		Insecure:      appconfig.Get().GatewayUseTLS() == false,
		TLSCA:         appconfig.Get().GrpcClientTLSCa(),
		TLSSkipVerify: true,
	}, grpcOpts...)
	if err != nil {
		log.With("sid", c.sid, "conn", c.id, "ch", channelID).
			Infof("cert port-forward: failed to connect for %q: %v", connectionName, err)
		_ = newCh.Reject(ssh.ConnectionFailed,
			fmt.Sprintf("hoop: connection %q not found", connectionName))
		return
	}

	// Send SessionOpen and wait for SessionOpenOK before accepting the channel
	// so a missing/inaccessible connection is surfaced as a protocol rejection.
	if err := grpcClient.Send(&pb.Packet{
		Type: pbagent.SessionOpen,
		Spec: map[string][]byte{
			pb.SpecGatewaySessionID:   []byte(channelSID),
			pb.SpecClientConnectionID: []byte(channelConnID),
		},
	}); err != nil {
		_, _ = grpcClient.Close()
		_ = newCh.Reject(ssh.ConnectionFailed, "hoop: failed to open session")
		return
	}

	if err := waitForCertSessionOpen(c.ctx, grpcClient); err != nil {
		log.With("sid", c.sid, "conn", c.id, "ch", channelID).
			Infof("cert port-forward: session open failed for %q: %v", connectionName, err)
		_, _ = grpcClient.Close()
		_ = newCh.Reject(ssh.ConnectionFailed,
			fmt.Sprintf("hoop: connection %q not found", connectionName))
		return
	}

	clientCh, clientRequests, err := newCh.Accept()
	if err != nil {
		_, _ = grpcClient.Close()
		return
	}

	log.With("sid", c.sid, "conn", c.id, "ch", channelID).
		Infof("cert port-forward: opened to %q (principal=%s)", connectionName, c.certSession.matchedValue)

	streamW := pb.NewStreamWriter(grpcClient, pbagent.SSHConnectionWrite, map[string][]byte{
		string(pb.SpecGatewaySessionID):   []byte(channelSID),
		string(pb.SpecClientConnectionID): []byte(channelConnID),
	})

	openChData := (sshtypes.OpenChannel{
		ChannelID:        channelID,
		ChannelType:      "direct-tcpip",
		ChannelExtraData: newCh.ExtraData(),
	}).Encode()
	if _, err := streamW.Write(openChData); err != nil {
		_ = clientCh.Close()
		_, _ = grpcClient.Close()
		return
	}

	// SSH client → gRPC
	c.channelWg.Add(1)
	go func() {
		defer c.channelWg.Done()
		buf := make([]byte, 32*1024)
		for {
			n, readErr := clientCh.Read(buf)
			if n > 0 {
				data := sshtypes.Data{ChannelID: channelID, Payload: buf[:n]}
				if _, err := streamW.Write(data.Encode()); err != nil {
					return
				}
			}
			if readErr != nil {
				if readErr == io.EOF {
					eofData := sshtypes.EOF{ChannelID: channelID}
					_, _ = streamW.Write(eofData.Encode())
				}
				return
			}
		}
	}()

	// channel requests (uncommon for direct-tcpip; discard with reply)
	c.channelWg.Add(1)
	go func() {
		defer c.channelWg.Done()
		for req := range clientRequests {
			if req.WantReply {
				_ = req.Reply(false, nil)
			}
		}
	}()

	defer func() {
		_ = grpcClient.Send(&pb.Packet{
			Type: pbagent.SessionClose,
			Spec: map[string][]byte{pb.SpecGatewaySessionID: []byte(channelSID)},
		})
		_, _ = grpcClient.Close()
	}()

	// gRPC → SSH client (main read loop for this channel)
	for {
		pkt, err := grpcClient.Recv()
		if err != nil {
			_ = clientCh.Close()
			return
		}
		if pkt == nil {
			_ = clientCh.Close()
			return
		}

		switch pb.PacketType(pkt.Type) {
		case pbclient.SSHConnectionWrite:
			switch sshtypes.DecodeType(pkt.Payload) {
			case sshtypes.DataType:
				var data sshtypes.Data
				if err := sshtypes.Decode(pkt.Payload, &data); err != nil {
					_ = clientCh.Close()
					return
				}
				if _, err := clientCh.Write(data.Payload); err != nil {
					return
				}
			case sshtypes.CloseChannelType:
				_ = clientCh.Close()
				return
			case sshtypes.EOFType:
				if ch, ok := clientCh.(interface{ CloseWrite() error }); ok {
					_ = ch.CloseWrite()
				}
			}
		case pbclient.TCPConnectionClose, pbclient.SessionClose:
			_ = clientCh.Close()
			return
		}

		select {
		case <-c.ctx.Done():
			_ = clientCh.Close()
			return
		default:
		}
	}
}

// waitForCertSessionOpen reads from grpcClient until it receives SessionOpenOK,
// a terminal failure packet, or times out. It is used by handleCertDirectTcpip
// to confirm the Hoop connection is accessible before accepting the SSH channel.
func waitForCertSessionOpen(ctx context.Context, grpcClient pb.ClientTransport) error {
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()

	pktCh := make(chan *pb.Packet, 1)
	errCh := make(chan error, 1)
	go func() {
		pkt, err := grpcClient.Recv()
		if err != nil {
			errCh <- err
			return
		}
		pktCh <- pkt
	}()

	select {
	case pkt := <-pktCh:
		if pb.PacketType(pkt.Type) == pbclient.SessionOpenOK {
			return nil
		}
		return fmt.Errorf("unexpected session response: %v", pkt.Type)
	case err := <-errCh:
		return fmt.Errorf("session open failed: %w", err)
	case <-timer.C:
		return fmt.Errorf("session open timed out")
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func parseHostsKey(privateKeyB64Enc string) (hostsKey ssh.Signer, err error) {
	privateKeyPemBytes, err := base64.StdEncoding.DecodeString(privateKeyB64Enc)
	if err != nil {
		return nil, fmt.Errorf("failed to decode hosts key: %v", err)
	}
	privateKey, err := decodeOpenSSHPrivateKey(privateKeyPemBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to decode hosts key: %v", err)
	}
	hostsKey, err = ssh.NewSignerFromKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create hosts key signer: %v", err)
	}
	return hostsKey, nil
}
