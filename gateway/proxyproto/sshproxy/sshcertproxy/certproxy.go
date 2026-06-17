package sshcertproxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/grpc"
	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/proxyproto/grpckey"
	"github.com/hoophq/hoop/gateway/proxyproto/sshproxy/sshcertproxy/proto/mysqlproto"
	"github.com/hoophq/hoop/gateway/proxyproto/sshproxy/sshcertproxy/proto/pgproto"
	"github.com/hoophq/hoop/gateway/proxyproto/sshproxy/sshcertproxy/proto/sshproto"
	"github.com/hoophq/hoop/gateway/proxyproto/sshproxy/sshcertproxy/proto/termproto"
	"golang.org/x/crypto/ssh"
)

// Server is the certificate-based SSH proxy server.
type Server struct {
	listenAddress       string
	connectionStore     sync.Map
	pendingCertSessions sync.Map
	listener            net.Listener
	hostKey             ssh.Signer
	certChecker         *ssh.CertChecker
	userMapping         UserMapping
}

// Run starts the certificate-based SSH proxy server. trustedCAs is the list of
// trusted SSH CA public keys in authorized_keys format. The server accepts
// connections authenticated by certificates signed by those CAs, and resolves
// users via userMapping.
func Run(listenAddr string, hostKey ssh.Signer, trustedCAs []string, userMapping UserMapping) (*Server, error) {
	certChecker, err := buildCertChecker(trustedCAs)
	if err != nil {
		return nil, fmt.Errorf("failed building cert checker: %w", err)
	}
	return runCertServer(listenAddr, hostKey, certChecker, userMapping)
}

// Stop cancels all active connections and closes the listener.
func (s *Server) Stop() error {
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

func runCertServer(listenAddr string, hostKey ssh.Signer, certChecker *ssh.CertChecker, userMapping UserMapping) (*Server, error) {
	lis, err := net.Listen("tcp4", listenAddr)
	if err != nil {
		return nil, fmt.Errorf("failed listening to address %v, err=%v", listenAddr, err)
	}

	log.Infof("starting ssh cert server at %v", listenAddr)

	server := &Server{
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
	handler      ChannelHandler
	handlerOnce  sync.Once
	certSession  *certSession
	newChannelCh <-chan ssh.NewChannel
	sshConn      *ssh.ServerConn
}

// sendErrorToClient delivers msg to the SSH client by accepting the first
// session channel it opens, writing to stderr, and sending exit-status 1.
// It always closes sshConn before returning. Used for hard failures that occur
// after the SSH handshake (user not found, inactive user, expired cert) so the
// client sees a diagnostic instead of a silent "Connection closed" message.
func sendErrorToClient(sshConn *ssh.ServerConn, clientNewCh <-chan ssh.NewChannel, msg string) {
	defer sshConn.Close()

	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()

	select {
	case newCh, ok := <-clientNewCh:
		if !ok {
			return
		}
		if newCh.ChannelType() != "session" {
			_ = newCh.Reject(ssh.Prohibited, "hoop: "+msg)
			return
		}
		ch, reqs, err := newCh.Accept()
		if err != nil {
			return
		}
		go ssh.DiscardRequests(reqs)
		_, _ = io.WriteString(ch.Stderr(), "hoop: "+msg+"\r\n")
		exitPayload := ssh.Marshal(struct{ ExitStatus uint32 }{1})
		_, _ = ch.SendRequest("exit-status", false, exitPayload)
		_ = ch.Close()
	case <-timer.C:
	}
}

func newCertConnection(sid, connID string, conn net.Conn, server *Server) (*certConnection, error) {
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
			return nil, nil
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

	sessAny, ok := server.pendingCertSessions.LoadAndDelete(string(sshConn.SessionID()))
	if !ok {
		sendErrorToClient(sshConn, clientNewCh, "internal error, missing cert state after handshake")
		return nil, fmt.Errorf("missing cert session state after handshake (sid=%s)", sid)
	}
	sess := sessAny.(*certSession)

	user, matchedValue, err := lookupUserByCert(sess.cert, server.userMapping)
	if err != nil {
		sendErrorToClient(sshConn, clientNewCh, "unable to authenticate, certificate attribute does not match any user")
		return nil, fmt.Errorf("cert auth user lookup failed: %w", err)
	}
	if user.Status != "active" {
		sendErrorToClient(sshConn, clientNewCh, fmt.Sprintf("user is not active (status=%q)", user.Status))
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
			sendErrorToClient(sshConn, clientNewCh, "certificate has already expired")
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
	if ctxErr != nil && c.handler != nil {
		if msg := translateUpstreamError(ctxErr.Error()); msg != "" {
			notifyOpenChannels(c.handler.RangeChannels, msg)
			hasUserError = true
		}
	}

	if !hasUserError && c.handler != nil {
		flushDone := make(chan struct{})
		go func() {
			c.handler.Wait()
			close(flushDone)
		}()
		select {
		case <-flushDone:
		case <-time.After(5 * time.Second):
			log.With("sid", c.sid, "conn", c.id).Warnf("timed out waiting for channel goroutines to finish")
		}
	}

	if c.handler != nil {
		if err := c.handler.SendClose(); err != nil {
			log.With("sid", c.sid, "conn", c.id).Warnf("failed sending session close packet, err=%v", err)
		} else if !hasUserError {
			time.Sleep(time.Second * 2)
		}
		_ = c.handler.Close()
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
		go func() {
			reason, err := c.validateChannel(newCh, channelID)
			if err != nil {
				_ = newCh.Reject(reason, err.Error())
				return
			}
			if newCh.ChannelType() == "session" {
				if err := c.serveSessionChannel(newCh, channelID); err != nil {
					c.cancelFn("cert: failed serving session channel %v, err=%v", channelID, err)
				}
				return
			}
			if err := c.handler.AcceptAndServe(newCh, channelID); err != nil {
				c.cancelFn("cert: failed handling channel, err=%v", err)
			}
		}()
	}
	c.cancelFn("ssh client disconnected")
}

// validateChannel enforces cert-auth constraints and establishes the
// session-level gRPC connection on the first call (direct-tcpip only). Returns
// a rejection reason and error when the channel must be refused; both are zero
// on success.
func (c *certConnection) validateChannel(newCh ssh.NewChannel, channelID uint16) (ssh.RejectionReason, error) {
	switch newCh.ChannelType() {
	case "session":
		if !c.certSession.allowPTY() {
			log.With("conn", c.id, "sid", c.sid, "ch", channelID).
				Infof("rejected session channel: cert lacks permit-pty (matched=%s)", c.certSession.matchedValue)
			return ssh.Prohibited, fmt.Errorf("hoop: cert does not permit pty/exec sessions")
		}
		return 0, nil
	case "direct-tcpip":
	default:
		log.With("conn", c.id, "sid", c.sid, "ch", channelID).
			Infof("rejected %q channel: not supported by cert auth (matched=%s)",
				newCh.ChannelType(), c.certSession.matchedValue)
		return ssh.Prohibited, fmt.Errorf("hoop: channel type %q is not supported", newCh.ChannelType())
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

	dbConn, err := models.GetConnectionByOrgAndName(c.certSession.orgID, connectionName)
	if err != nil {
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

	connType := pb.ToConnectionType(dbConn.Type, dbConn.SubType.String)
	log.Infof("cert auth: received direct-tcpip for connection %q type=%s (matched=%s)",
		connectionName, connType, c.certSession.matchedValue)

	// Establish the session-level gRPC connection on the first direct-tcpip
	// channel. sync.Once ensures this runs exactly once per SSH connection;
	// subsequent channels reuse the same gRPC session.
	c.handlerOnce.Do(func() {
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
		var handler ChannelHandler
		var openErr error
		switch connType {
		case pb.ConnectionTypePostgres:
			handler, openErr = pgproto.OpenSession(c.sid, c.id, grpcClient, c.ctx, c.cancelFn)
		case pb.ConnectionTypeMySQL:
			handler, openErr = mysqlproto.OpenSession(c.sid, c.id, grpcClient, c.ctx, c.cancelFn)
		case pb.ConnectionTypeSSH:
			handler, openErr = sshproto.OpenSession(c.sid, c.id, grpcClient, c.ctx, c.cancelFn)
		default:
			c.cancelFn("cert auth: unsupported connection type %q for connection %q", connType, connectionName)
			_, _ = grpcClient.Close()
		}
		if openErr != nil {
			c.cancelFn("cert auth: session open failed for connection %q: %v", connectionName, openErr)
			_, _ = grpcClient.Close()
			return
		}
		c.handler = handler
	})

	select {
	case <-c.ctx.Done():
		return ssh.ConnectionFailed,
			fmt.Errorf("hoop: connection %q not found or not accessible", connectionName)
	default:
	}

	log.With("conn", c.id, "sid", c.sid, "ch", channelID).
		Infof("cert port-forward: connection=%q matched=%s conntype=%s",
			connectionName, c.certSession.matchedValue, connType)
	return 0, nil
}

// serveSessionChannel handles a "session" channel. It reads channel requests
// until an exec is received — the exec payload is the target connection name.
// Any pty-req that arrives before exec is pre-approved (cert already has
// permit-pty) and buffered so it can be forwarded to the agent once the gRPC
// connection is established. The target connection must be of type ssh.
func (c *certConnection) serveSessionChannel(newCh ssh.NewChannel, channelID uint16) error {
	clientCh, clientRequests, err := newCh.Accept()
	if err != nil {
		return fmt.Errorf("failed accepting session channel: %w", err)
	}

	// Collect pre-exec requests (e.g. pty-req) and wait for the exec request
	// that carries the connection name. We cannot forward them to the agent yet
	// because the gRPC connection is not established until we know the connection.
	var preExecRequests []*ssh.Request
	var execReq *ssh.Request
	timeout := time.NewTimer(30 * time.Second)
	defer timeout.Stop()
loop:
	for {
		select {
		case req, ok := <-clientRequests:
			if !ok {
				_ = clientCh.Close()
				return fmt.Errorf("session channel %v closed before exec request was received", channelID)
			}
			switch req.Type {
			case "pty-req":
				// Pre-approve: the cert already has permit-pty (checked by
				// validateChannel). Buffer the payload to forward to the agent
				// once the gRPC connection is established.
				if req.WantReply {
					_ = req.Reply(true, nil)
				}
				preExecRequests = append(preExecRequests, req)
			case "exec":
				execReq = req
				break loop
			case "subsystem", "shell":
				msg := fmt.Sprintf("request type %q is not supported; specify a connection name: ssh -p <port> <user>@<host> <connection-name>", req.Type)
				if req.Type == "subsystem" {
					var subsysReq struct{ Subsystem string }
					_ = ssh.Unmarshal(req.Payload, &subsysReq)
					msg = fmt.Sprintf("subsystem %q is not supported; for file transfers use scp legacy mode (add -O flag): scp -O ...", subsysReq.Subsystem)
				}
				log.With("sid", c.sid, "conn", c.id, "ch", channelID).
					Infof("rejected %q on session channel: not supported by cert auth", req.Type)
				// Reply true to suppress the generic "request failed on channel N"
				// message from the SSH client; our stderr message reaches the user instead.
				if req.WantReply {
					_ = req.Reply(true, nil)
				}
				_, _ = io.WriteString(clientCh.Stderr(), "hoop: "+msg+"\r\n")
				exitPayload := ssh.Marshal(struct{ ExitStatus uint32 }{1})
				_, _ = clientCh.SendRequest("exit-status", false, exitPayload)
				_ = clientCh.Close()
				return nil
			default:
				log.With("sid", c.sid, "conn", c.id, "ch", channelID).
					Infof("rejected unexpected pre-exec request type %q on session channel", req.Type)
				if req.WantReply {
					_ = req.Reply(false, nil)
				}
			}
		case <-timeout.C:
			_ = clientCh.Close()
			return fmt.Errorf("timed out waiting for exec request on session channel %v", channelID)
		case <-c.ctx.Done():
			_ = clientCh.Close()
			return nil
		}
	}

	// rejectExec writes msg to the channel's stderr, sends exit-status 1, and
	// closes the channel. Replying true to exec (rather than false) suppresses
	// OpenSSH's generic "exec request failed on channel N" and lets the caller's
	// message reach the user instead.
	rejectExec := func(msg string) {
		if execReq.WantReply {
			_ = execReq.Reply(true, nil)
		}
		_, _ = io.WriteString(clientCh.Stderr(), "hoop: "+msg+"\r\n")
		exitPayload := ssh.Marshal(struct{ ExitStatus uint32 }{1})
		_, _ = clientCh.SendRequest("exit-status", false, exitPayload)
		_ = clientCh.Close()
	}

	// Parse the exec payload (SSH wire-encoded string). The format is:
	// "<connection-name> [command...]" — the first token is the Hoop connection
	// name used for routing; everything after the first space is the optional
	// command to execute on the upstream SSH server.
	var execCmd struct{ Command string }
	if err := ssh.Unmarshal(execReq.Payload, &execCmd); err != nil || execCmd.Command == "" {
		rejectExec("invalid exec payload")
		return fmt.Errorf("invalid exec payload on session channel %v", channelID)
	}
	var connectionName, upstreamCommand string
	if idx := strings.IndexByte(execCmd.Command, ' '); idx >= 0 {
		connectionName = execCmd.Command[:idx]
		upstreamCommand = execCmd.Command[idx+1:]
	} else {
		connectionName = execCmd.Command
	}

	// Verify the target connection exists and is of type ssh.
	dbConn, err := models.GetConnectionByOrgAndName(c.certSession.orgID, connectionName)
	if err != nil {
		if err == models.ErrNotFound {
			log.With("sid", c.sid, "conn", c.id, "ch", channelID).
				Infof("cert session: connection %q not found (org=%s matched=%s)",
					connectionName, c.certSession.orgID, c.certSession.matchedValue)
			rejectExec(fmt.Sprintf("connection %q not found", connectionName))
			return fmt.Errorf("connection %q not found", connectionName)
		}
		log.With("sid", c.sid, "conn", c.id, "ch", channelID).
			Warnf("cert session: failed looking up connection %q: %v", connectionName, err)
		rejectExec(fmt.Sprintf("failed looking up connection %q", connectionName))
		return fmt.Errorf("failed looking up connection %q: %w", connectionName, err)
	}
	connType := pb.ToConnectionType(dbConn.Type, dbConn.SubType.String)
	switch connType {
	case pb.ConnectionTypeSSH, pb.ConnectionTypeCommandLine:
		// supported
	default:
		log.With("sid", c.sid, "conn", c.id, "ch", channelID).
			Infof("cert session: connection %q type %s does not support pty/exec (matched=%s)",
				connectionName, connType, c.certSession.matchedValue)
		rejectExec(fmt.Sprintf("connection %q (type=%s) does not support pty/exec sessions; must be type ssh or custom",
			connectionName, connType))
		return fmt.Errorf("connection %q (type=%s) does not support pty/exec sessions",
			connectionName, connType)
	}

	// Verb for command-line connections:
	//   - PTY + no args  → connect  (interactive terminal)
	//   - PTY + args     → exec     (args sent as stdin input)
	//   - no PTY         → exec     (empty stdin, command runs one-shot)
	isPTY := len(preExecRequests) > 0
	verb := pb.ClientVerbConnect
	if connType == pb.ConnectionTypeCommandLine && (!isPTY || upstreamCommand != "") {
		verb = pb.ClientVerbExec
	}

	// Establish the session-level gRPC connection (once per SSH connection).
	c.handlerOnce.Do(func() {
		grpcOpts := []*grpc.ClientOptions{
			grpc.WithOption(grpc.OptionConnectionName, connectionName),
			grpc.WithOption(grpckey.ImpersonateAuthKeyHeaderKey, grpckey.ImpersonateSecretKey),
			grpc.WithOption(grpckey.ImpersonateUserSubjectHeaderKey, c.certSession.userSubject),
			grpc.WithOption("origin", pb.ConnectionOriginClient),
			grpc.WithOption("verb", verb),
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
			c.cancelFn("cert auth: failed connecting to grpc for connection %q: %v", connectionName, connErr)
			return
		}
		var handler ChannelHandler
		var openErr error
		switch connType {
		case pb.ConnectionTypeSSH:
			handler, openErr = sshproto.OpenSession(c.sid, c.id, grpcClient, c.ctx, c.cancelFn)
		case pb.ConnectionTypeCommandLine:
			handler, openErr = termproto.OpenSession(c.sid, c.id, grpcClient, c.ctx, c.cancelFn)
		}
		if openErr != nil {
			c.cancelFn("cert auth: session open failed for connection %q: %v", connectionName, openErr)
			_, _ = grpcClient.Close()
			return
		}
		c.handler = handler
	})

	select {
	case <-c.ctx.Done():
		cause := context.Cause(c.ctx)
		msg := translateUpstreamError(cause.Error())
		if msg == "" {
			msg = "connection setup failed"
		}
		rejectExec(msg)
		return nil
	default:
	}

	sessHandler, ok := c.handler.(SessionHandler)
	if !ok || sessHandler == nil {
		rejectExec("session handler not initialized")
		return fmt.Errorf("session handler not initialized for connection %q", connectionName)
	}

	log.With("sid", c.sid, "conn", c.id, "ch", channelID).
		Infof("cert session: serving pty/exec for connection=%q matched=%s conntype=%s",
			connectionName, c.certSession.matchedValue, connType)

	return sessHandler.ServeSession(clientCh, clientRequests, channelID, preExecRequests, execReq, upstreamCommand)
}
