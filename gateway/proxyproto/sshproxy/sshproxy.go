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
	"github.com/hoophq/hoop/gateway/transport"
	"golang.org/x/crypto/ssh"
)

// from syscall.SIGWINCH, avoid syscall errors when compiling on Windows
const SIGWINCH = syscall.Signal(0x1c)
const instanceKey = "ssh_server"

var instanceStore sync.Map

type proxyServer struct {
	listenAddress   string
	connectionStore sync.Map
	listener        net.Listener
	hostKey         ssh.Signer
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

func (s *proxyServer) Start(listenAddr, hostsKeyB64Enc string) (err error) {
	instance, _ := instanceStore.Load(instanceKey)
	if _, ok := instance.(*proxyServer); ok && s.listener != nil {
		return nil
	}

	sshHostsKey, err := parseHostsKey(hostsKeyB64Enc)
	if err != nil {
		return fmt.Errorf("failed parsing hosts key, reason=%v", err)
	}
	log.Infof("starting ssh server proxy at %v", listenAddr)

	// start new instance
	server, err := runProxyServer(listenAddr, sshHostsKey)
	if err != nil {
		return err
	}
	instanceStore.Store(instanceKey, server)
	return nil
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

func runProxyServer(listenAddr string, hostKey ssh.Signer) (*proxyServer, error) {
	lis, err := net.Listen("tcp4", listenAddr)
	if err != nil {
		return nil, fmt.Errorf("failed listening to address %v, err=%v", listenAddr, err)
	}
	server := &proxyServer{
		connectionStore: sync.Map{},
		listener:        lis,
		listenAddress:   listenAddr,
		hostKey:         hostKey,
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
			conn, err := newSSHConnection(sessionID, connectionID, netConn, hostKey)
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

type sshConnection struct {
	id                  string
	sid                 string
	ctx                 context.Context
	cancelFn            func(msg string, a ...any)
	grpcClient          pb.ClientTransport
	clientNewSshChannel <-chan ssh.NewChannel
	sshConn             *ssh.ServerConn
	sshChannels         sync.Map
	pendingRequests     sync.Map // maps channelID (uint16) to pending SSH request
}

func newSSHConnection(sid, connID string, conn net.Conn, hostKey ssh.Signer) (*sshConnection, error) {
	sshServerConfig := &ssh.ServerConfig{
		// NoClientAuth: true, // Ignore client authentication
		PasswordCallback: func(conn ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			log.
				With("sid", sid).
				Infof("ssh connection attempt, user=%v, remote-addr=%v, local-addr=%v", conn.User(), conn.RemoteAddr(), conn.LocalAddr())

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

			log.Infof("obtained access by id, id=%v, subject=%v, connection=%v, expires-at=%v (in %v)",
				dba.ID, dba.UserSubject, dba.ConnectionName,
				dba.ExpireAt.Format(time.RFC3339), ctxDuration.Truncate(time.Second).String())

			return &ssh.Permissions{
				Extensions: map[string]string{
					"hoop-user-subject":     dba.UserSubject,
					"hoop-connection-name":  dba.ConnectionName,
					"hoop-context-duration": ctxDuration.String(),
				},
			}, nil
		},
	}

	// the encryption key to be used we use a single hosts key
	// used for the SSH handshake and related to the known_hosts file
	sshServerConfig.AddHostKey(hostKey)

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

	connectionName := sshConn.Permissions.Extensions["hoop-connection-name"]
	userSubject := sshConn.Permissions.Extensions["hoop-user-subject"]
	ctxDurationStr := sshConn.Permissions.Extensions["hoop-context-duration"]

	if connectionName == "" || userSubject == "" {
		return nil, fmt.Errorf("missing required SSH connection attributes")
	}

	ctxDuration, err := time.ParseDuration(ctxDurationStr)
	if err != nil {
		return nil, fmt.Errorf("failed parsing context duration: %v", err)
	}

	tokenVerifier, _, err := idp.NewUserInfoTokenVerifierProvider()
	if err != nil {
		log.Errorf("failed to load IDP provider: %v", err)
		return nil, err
	}

	if err := transport.CheckUserToken(tokenVerifier, userSubject); err != nil {
		return nil, err
	}

	log.
		With("sid", sid, "remote-addr", conn.RemoteAddr()).
		Debugf("create new ssh connection, user=%v, connection_name=%v", userSubject, connectionName)

	// connect to the gateway gRPC server
	client, err := grpc.Connect(grpc.ClientConfig{
		ServerAddress: grpc.LocalhostAddr,
		Token:         "", // it will use impersonate-auth-key as authentication
		UserAgent:     "ssh/grpc",
		Insecure:      appconfig.Get().GatewayUseTLS() == false,
		TLSCA:         appconfig.Get().GrpcClientTLSCa(),
		// it should be safe to skip verify here as we are connecting to localhost
		TLSSkipVerify: true,
	},
		grpc.WithOption(grpc.OptionConnectionName, connectionName),
		grpc.WithOption(grpckey.ImpersonateAuthKeyHeaderKey, grpckey.ImpersonateSecretKey),
		grpc.WithOption(grpckey.ImpersonateUserSubjectHeaderKey, userSubject),
		grpc.WithOption("origin", pb.ConnectionOriginClient),
		grpc.WithOption("verb", pb.ClientVerbConnect),
		grpc.WithOption("session-id", sid),
	)
	if err != nil {
		return nil, fmt.Errorf("failed connecting to grpc server, err=%v", err)
	}

	ctx, cancelFn := context.WithCancelCause(context.Background())
	ctx, timeoutCancelFn := context.WithTimeoutCause(ctx, ctxDuration, fmt.Errorf("connection access expired, resourceid=%v", connID))
	sessionConn := &sshConnection{
		id:  connID,
		sid: sid,
		ctx: ctx,
		cancelFn: func(msg string, a ...any) {
			cancelFn(fmt.Errorf(msg, a...))
			timeoutCancelFn()
		},
		sshConn:             sshConn,
		grpcClient:          client,
		clientNewSshChannel: clientNewCh,
	}

	transport.PollingUserToken(sessionConn.ctx, func(cause error) {
		sessionConn.cancelFn(cause.Error())
	}, tokenVerifier, userSubject)

	return sessionConn, nil
}

func (c *sshConnection) handleConnection() {
	// it is important to wait for the session to be established
	// before handling ssh channels
	c.handleClientWrite()
	go c.handleServerWrite()

	// wait for the connection to be closed
	// either by the client, server, or context cancellation
	<-c.ctx.Done()

	ctxErr := context.Cause(c.ctx)
	log.With("sid", c.sid, "conn", c.id).Infof("ssh connection closed, reason=%v", ctxErr)

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

	// wait 2 seconds for cleaning up session gracefully
	time.Sleep(time.Second * 2)
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

					// Forward the reply to the pending client request
					if pendingReqObj, ok := c.pendingRequests.Load(reply.ChannelID); ok {
						if pendingReq, ok := pendingReqObj.(*pendingSSHRequest); ok {
							select {
							case pendingReq.reply <- reply.OK:
								log.With("sid", c.sid, "ch", reply.ChannelID, "conn", c.id).
									Debugf("forwarded ssh request reply to client")
							case <-c.ctx.Done():
								return
							default:
								// Channel full or already handled, drop the reply
							}
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
}

func (c *sshConnection) handleChannel(newCh ssh.NewChannel, channelID uint16) {
	log.With("conn", c.id, "sid", c.sid, "ch", channelID).Infof("received new channel, type=%v", newCh.ChannelType())

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
	go func() {
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
	go func() {
		for req := range clientRequests {
			log.With("sid", c.sid, "conn", c.id, "ch", channelID, "type", req.Type).Debug("received client ssh request")

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
				// Store the pending request to wait for agent's reply
				replyChan := make(chan bool, 1)
				pendingReq := &pendingSSHRequest{
					req:   req,
					reply: replyChan,
				}
				c.pendingRequests.Store(channelID, pendingReq)

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
					c.pendingRequests.Delete(chID)
				}(req, channelID)
			}
		}

		log.With("ch", channelID, "conn", c.id).Debugf("done processing ssh client requests")
	}()
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
