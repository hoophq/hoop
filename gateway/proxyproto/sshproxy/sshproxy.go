package sshproxy

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/grpc"
	"github.com/hoophq/hoop/common/keys"
	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/idp"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/proxyproto/grpckey"
	"github.com/hoophq/hoop/gateway/proxyproto/sshproxy/clientproto"
	"github.com/hoophq/hoop/gateway/transport/usertoken"
	"golang.org/x/crypto/ssh"
)

const instanceKey = "ssh_server"

var instanceStore sync.Map

// ServerConfig holds all configuration required to start the SSH proxy server.
type ServerConfig struct {
	ListenAddress string
	// HostsKey is the base64-encoded PEM private key used as the SSH host key.
	HostsKey string
	// TrustedCAs is the list of trusted SSH CA public keys in authorized_keys
	// format. When non-empty, the certificate-based server is started instead
	// of the password-based server.
	TrustedCAs []string
	// UserMapping specifies which certificate attribute (principal or key_id) is
	// matched against which user table column (email, subject, user_id).
	// Required when TrustedCAs is non-empty.
	UserMapping UserMapping
}

// proxyServer is the external-facing singleton. It holds exactly one active
// server implementation (password-based or certificate-based), selected at
// Start() time based on TrustedCAs configuration.
type proxyServer struct {
	pwdServer  *passwordServer
	certServer *certServer
}

// GetServerInstance returns the singleton proxy server instance.
func GetServerInstance() *proxyServer {
	instance, _ := instanceStore.Load(instanceKey)
	if server, ok := instance.(*proxyServer); ok {
		return server
	}
	server := &proxyServer{}
	instanceStore.Store(instanceKey, server)
	return server
}

// Start initializes and runs the SSH proxy server. When TrustedCAs are
// configured the certificate-based server is started; otherwise the
// password-based server is started. It is a no-op if already running.
func (s *proxyServer) Start(cfg ServerConfig) error {
	if s.pwdServer != nil || s.certServer != nil {
		return nil
	}

	hostKey, err := parseHostsKey(cfg.HostsKey)
	if err != nil {
		return fmt.Errorf("failed parsing hosts key, reason=%v", err)
	}

	if len(cfg.TrustedCAs) > 0 {
		certChecker, err := buildCertChecker(cfg.TrustedCAs)
		if err != nil {
			return fmt.Errorf("failed building cert checker, reason=%v", err)
		}
		srv, err := runCertServer(cfg.ListenAddress, hostKey, certChecker, cfg.UserMapping)
		if err != nil {
			return err
		}
		s.certServer = srv
	} else {
		srv, err := runPasswordServer(cfg.ListenAddress, hostKey)
		if err != nil {
			return err
		}
		s.pwdServer = srv
	}

	instanceStore.Store(instanceKey, s)
	return nil
}

// RevokeByCredentialID cancels all password-authenticated connections using
// the given credential ID. Certificate-authenticated connections are not
// tracked by credential ID and are unaffected.
func (s *proxyServer) RevokeByCredentialID(credentialID string) {
	if s.pwdServer != nil {
		s.pwdServer.revokeByCredentialID(credentialID)
	}
}

// Stop cancels all active connections and closes the listener.
func (s *proxyServer) Stop() error {
	instanceStore.Delete(instanceKey)
	if s.pwdServer != nil {
		return s.pwdServer.stop()
	}
	if s.certServer != nil {
		return s.certServer.stop()
	}
	return nil
}

func parseHostsKey(privateKeyB64Enc string) (ssh.Signer, error) {
	privateKeyPemBytes, err := base64.StdEncoding.DecodeString(privateKeyB64Enc)
	if err != nil {
		return nil, fmt.Errorf("failed to decode hosts key: %v", err)
	}
	privateKey, err := decodeOpenSSHPrivateKey(privateKeyPemBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to decode hosts key: %v", err)
	}
	hostsKey, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create hosts key signer: %v", err)
	}
	return hostsKey, nil
}

// passwordServer is the SSH proxy server implementation for password-based
// (secret access key) authentication.
type passwordServer struct {
	listenAddress   string
	connectionStore sync.Map
	listener        net.Listener
	hostKey         ssh.Signer
}

func (s *passwordServer) revokeByCredentialID(credentialID string) {
	s.connectionStore.Range(func(key, value any) bool {
		if conn, ok := value.(*passwordConnection); ok && conn.credentialID == credentialID {
			conn.cancelFn("credential revoked")
		}
		return true
	})
}

func (s *passwordServer) stop() error {
	s.connectionStore.Range(func(key, value any) bool {
		if conn, ok := value.(*passwordConnection); ok {
			conn.cancelFn("proxy server is shutting down")
		}
		return true
	})
	if s.listener != nil {
		log.Infof("stopping ssh password server at %v", s.listener.Addr().String())
		_ = s.listener.Close()
	}
	return nil
}

func runPasswordServer(listenAddr string, hostKey ssh.Signer) (*passwordServer, error) {
	lis, err := net.Listen("tcp4", listenAddr)
	if err != nil {
		return nil, fmt.Errorf("failed listening to address %v, err=%v", listenAddr, err)
	}

	log.Infof("starting ssh password server at %v", listenAddr)

	server := &passwordServer{
		listenAddress: listenAddr,
		listener:      lis,
		hostKey:       hostKey,
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
					log.Info("password ssh proxy listener closed")
					return
				}
				log.With("conn", connectionID).Warnf("failed obtaining tcp connection, err=%v", err)
				break
			}

			sessionID := uuid.NewString()
			conn, err := newPasswordConnection(sessionID, connectionID, netConn, server)
			if err != nil {
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

			server.connectionStore.Store(sessionID, conn)
			go func() {
				defer server.connectionStore.Delete(sessionID)
				conn.handle()
			}()
		}
	}()

	return server, nil
}

type passwordConnection struct {
	id           string
	sid          string
	credentialID string
	ctx          context.Context
	cancelFn     func(msg string, a ...any)
	proto        *clientproto.Session
	newChannelCh <-chan ssh.NewChannel
	sshConn      *ssh.ServerConn
}

func newPasswordConnection(sid, connID string, conn net.Conn, server *passwordServer) (*passwordConnection, error) {
	sshCfg := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			log.With("sid", sid).Infof("ssh connection attempt, user=%v, remote-addr=%v, local-addr=%v",
				c.User(), c.RemoteAddr(), c.LocalAddr())

			secretKeyHash, err := keys.Hash256Key(string(password))
			if err != nil {
				return nil, fmt.Errorf("failed hashing secret key: %v", err)
			}

			dba, err := models.GetValidConnectionCredentialsBySecretKey([]string{pb.ConnectionTypeSSH.String()}, secretKeyHash)
			if err != nil {
				if err == models.ErrNotFound {
					return nil, fmt.Errorf("invalid secret access key credentials")
				}
				return nil, fmt.Errorf("failed obtaining secret access key, reason=%v", err)
			}

			if dba.ExpireAt.Before(time.Now().UTC()) {
				return nil, fmt.Errorf("invalid secret access key credentials")
			}

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
		tokenVerifier, _, err = idp.NewUserInfoTokenVerifierProvider()
		if err != nil {
			log.Errorf("failed to load IDP provider: %v", err)
			return nil, err
		}
		if err := usertoken.CheckUserToken(tokenVerifier, userSubject); err != nil {
			return nil, err
		}
	}

	log.With("sid", sid, "remote-addr", conn.RemoteAddr()).
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

	client, err := grpc.Connect(grpc.ClientConfig{
		ServerAddress: grpc.LocalhostAddr,
		Token:         "",
		UserAgent:     "ssh/grpc",
		Insecure:      appconfig.Get().GatewayUseTLS() == false,
		TLSCA:         appconfig.Get().GrpcClientTLSCa(),
		TLSSkipVerify: true,
	}, grpcOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed connecting to grpc server, err=%v", err)
	}

	ctx, cancelFn := context.WithCancelCause(context.Background())
	ctx, timeoutCancelFn := context.WithTimeoutCause(ctx, ctxDuration,
		fmt.Errorf("connection access expired, resourceid=%v", connID))
	wrappedCancelFn := func(msg string, a ...any) {
		cancelFn(fmt.Errorf(msg, a...))
		timeoutCancelFn()
	}

	proto := clientproto.New(sid, connID, client, ctx, wrappedCancelFn)
	c := &passwordConnection{
		id:           connID,
		sid:          sid,
		credentialID: credentialID,
		ctx:          ctx,
		cancelFn:     wrappedCancelFn,
		proto:        proto,
		sshConn:      sshConn,
		newChannelCh: clientNewCh,
	}

	if !isMachineCredential {
		usertoken.PollingUserToken(c.ctx, func(cause error) {
			c.cancelFn(cause.Error())
		}, tokenVerifier, userSubject)
	}

	return c, nil
}

func (c *passwordConnection) handle() {
	c.proto.Open()
	go c.acceptChannels()

	<-c.ctx.Done()
	ctxErr := context.Cause(c.ctx)
	log.With("sid", c.sid, "conn", c.id).Infof("ssh connection closed, reason=%v", ctxErr)

	hasUserError := false
	if ctxErr != nil {
		if msg := translateUpstreamError(ctxErr.Error()); msg != "" {
			notifyOpenChannels(c.proto.RangeChannels, msg)
			hasUserError = true
		}
	}

	if !hasUserError {
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

	if err := c.proto.SendClose(); err != nil {
		log.With("sid", c.sid, "conn", c.id).Warnf("failed sending session close packet, err=%v", err)
		return
	}

	if !hasUserError {
		time.Sleep(time.Second * 2)
	}
	_ = c.sshConn.Close()
	_, _ = c.proto.GRPCClient().Close()
}

func (c *passwordConnection) acceptChannels() {
	select {
	case <-c.ctx.Done():
		return
	default:
	}

	channelID := uint16(0)
	for newCh := range c.newChannelCh {
		channelID++
		log.With("conn", c.id, "sid", c.sid, "ch", channelID).
			Infof("received new channel, type=%v", newCh.ChannelType())
		go c.handleChannel(newCh, channelID)
	}
	c.cancelFn("ssh client disconnected")
}

func (c *passwordConnection) handleChannel(newCh ssh.NewChannel, channelID uint16) {
	if err := c.proto.AcceptAndServeChannel(newCh, channelID); err != nil {
		c.cancelFn("failed handling channel, err=%v", err)
	}
}
