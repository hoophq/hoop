package sshproxy

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	sshtypes "libhoop/proxy/ssh/types"
	"net"
	"strconv"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/grpc"
	"github.com/hoophq/hoop/common/keys"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/memory"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	pbclient "github.com/hoophq/hoop/common/proto/client"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/proxyproto/grpckey"
	"golang.org/x/crypto/ssh"
)

var (
	instanceStore        = memory.New()
	instanceKey   string = "ssh_server"
)

// from syscall.SIGWINCH, avoid syscall errors when compiling on Windows
const SIGWINCH = syscall.Signal(0x1c)

type proxyServer struct {
	listenAddress   string
	connectionStore memory.Store
	listener        net.Listener
	hostKey         ssh.Signer
}

// GetServerInstance returns the singleton instance of SSHServer.
func GetServerInstance() *proxyServer {
	if server, ok := instanceStore.Get(instanceKey).(*proxyServer); ok {
		return server
	}
	server := &proxyServer{}
	instanceStore.Set(instanceKey, server)
	return server
}

func (s *proxyServer) Start(listenAddr, hostsKeyB64Enc string) (err error) {
	if _, ok := instanceStore.Get(instanceKey).(*proxyServer); ok && s.listener != nil {
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
	instanceStore.Set(instanceKey, server)
	return nil
}

func (s *proxyServer) Stop() error {
	if server, ok := instanceStore.Pop(instanceKey).(*proxyServer); ok {
		if s.connectionStore == nil {
			return nil
		}
		for _, obj := range s.connectionStore.List() {
			if sshConn, ok := obj.(*sshConnection); ok {
				sshConn.cancelFn("proxy server is shutting down")
			}
		}
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
		connectionStore: memory.New(),
		listener:        lis,
		listenAddress:   listenAddr,
		hostKey:         hostKey,
	}

	go func() {
		defer lis.Close()
		connectionID := 0
		for {
			connectionID++
			netConn, err := lis.Accept()
			if err != nil {
				log.With("conn", connectionID).Warnf("failed obtaining network connection, err=%v", err)
				break
			}
			sid := uuid.NewString()
			conn, err := newSSHConnection(sid, strconv.Itoa(connectionID), netConn, hostKey)
			if err != nil {
				log.With("sid", sid, "conn", connectionID).Warnf("failed creating new SSH connection, err=%v", err)
				_ = netConn.Close()
				continue
			}
			server.connectionStore.Set(sid, conn)

			go func() {
				defer server.connectionStore.Del(sid)
				conn.handleConnection()
			}()
		}
	}()

	return server, nil
}

type sshConnection struct {
	sid                  string
	id                   string
	ctx                  context.Context
	cancelFn             func(msg string, a ...any)
	grpcClient           pb.ClientTransport
	clientNewSshChannel  <-chan ssh.NewChannel
	sshServerConnCloseFn func() error
	channelStore         memory.Store
}

func newSSHConnection(sid, connID string, conn net.Conn, hostKey ssh.Signer) (*sshConnection, error) {
	sshServerConfig := &ssh.ServerConfig{
		// NoClientAuth: true, // Ignore client authentication
		PasswordCallback: func(conn ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			log.With("sid", sid, "conn", connID).Infof("ssh connection attempt, user=%v, remote-addr=%v, local-addr=%v",
				conn.User(), conn.RemoteAddr(), conn.LocalAddr())
			secretKeyHash, err := keys.Hash256Key(string(password))
			if err != nil {
				return nil, fmt.Errorf("failed hashing secret key: %v", err)
			}

			dba, err := models.GetValidConnectionCredentialsBySecretKey(secretKeyHash)
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
	sshServerConfig.AddHostKey(hostKey)

	sshConn, clientNewCh, sshReq, err := ssh.NewServerConn(conn, sshServerConfig)
	if err != nil {
		return nil, fmt.Errorf("failed establishing SSH connection: %v", err)
	}
	go ssh.DiscardRequests(sshReq)

	var connectionName string
	var userSubject string
	var ctxDurationStr string
	if sshConn.Permissions != nil {
		connectionName = sshConn.Permissions.Extensions["hoop-connection-name"]
		userSubject = sshConn.Permissions.Extensions["hoop-user-subject"]
		ctxDurationStr = sshConn.Permissions.Extensions["hoop-context-duration"]
	}

	if connectionName == "" || userSubject == "" {
		return nil, fmt.Errorf("missing required SSH connection attributes")
	}

	ctxDuration, err := time.ParseDuration(ctxDurationStr)
	if err != nil {
		return nil, fmt.Errorf("failed parsing context duration: %v", err)
	}

	log.With("sid", sid, "conn", connID, "remote-addr", conn.RemoteAddr()).Debugf("create new ssh connection, user=%v, connection_name=%v",
		userSubject, connectionName)

	tlsCA := appconfig.Get().GatewayTLSCa()
	client, err := grpc.Connect(grpc.ClientConfig{
		ServerAddress: grpc.LocalhostAddr,
		Token:         "", // it will use impersonate-auth-key as authentication
		UserAgent:     "ssh/grpc",
		Insecure:      tlsCA == "",
		TLSCA:         tlsCA,
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
	ctx, timeoutCancelFn := context.WithTimeoutCause(ctx, ctxDuration, fmt.Errorf("connection access expired"))
	return &sshConnection{
		sid: sid,
		id:  connID,
		ctx: ctx,
		cancelFn: func(msg string, a ...any) {
			cancelFn(fmt.Errorf(msg, a...))
			timeoutCancelFn()
		},
		sshServerConnCloseFn: sshConn.Close,
		grpcClient:           client,
		clientNewSshChannel:  clientNewCh,
		channelStore:         memory.New(),
	}, nil
}

func (c *sshConnection) handleConnection() {
	// it is important to wait for the session to be established
	// before handling ssh channels
	c.handleClientWrite()
	go c.handleServerWrite()

	<-c.ctx.Done()

	ctxErr := context.Cause(c.ctx)
	log.With("sid", c.sid, "conn", c.id).Infof("ssh connection closed, reason=%v", ctxErr)
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
	_ = c.sshServerConnCloseFn()
	_, _ = c.grpcClient.Close()
}

func (c *sshConnection) handleClientWrite() {
	openSessionPacket := &pb.Packet{
		Type: pbagent.SessionOpen,
		Spec: map[string][]byte{
			pb.SpecGatewaySessionID:   []byte(c.sid),
			pb.SpecClientConnectionID: []byte(c.id),
		},
	}

	if err := c.grpcClient.Send(openSessionPacket); err != nil {
		c.cancelFn("failed sending open session packet, err=%v", err)
		return
	}

	startupCh := make(chan struct{})
	go func() {
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
			case pbclient.SessionOpenWaitingApproval:
				c.cancelFn("session with review are not implemented yet, closing connection")
				startupCh <- struct{}{}
				return
			case pbclient.SSHConnectionWrite:
				switch sshtypes.DecodeType(pkt.Payload) {
				case sshtypes.DataType:
					var data sshtypes.Data
					if err := sshtypes.Decode(pkt.Payload, &data); err != nil {
						c.cancelFn("failed decoding ssh data, err=%v", err)
						return
					}
					connWrapperObj := c.channelStore.Get(fmt.Sprintf("%v", data.ChannelID))
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

					obj := c.channelStore.Get(fmt.Sprintf("%v", cc.ID))
					if clientCh, ok := obj.(io.Closer); ok {
						err := clientCh.Close()
						log.With("sid", c.sid, "ch", cc.ID, "conn", c.id).Debugf("closing client ssh channel type=%v, err=%v",
							cc.Type, err)
					}
				default:
					c.cancelFn("received unknown ssh message type (%v)", sshtypes.DecodeType(pkt.Payload))
					return
				}

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
		c.cancelFn("timeout waiting for session to be ready")
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
	log.With("ch", channelID, "conn", c.id).Infof("received new channel, type=%v", newCh.ChannelType())

	streamW := pb.NewStreamWriter(c.grpcClient, pbagent.SSHConnectionWrite, map[string][]byte{
		string(pb.SpecGatewaySessionID):   []byte(c.sid),
		string(pb.SpecClientConnectionID): []byte(c.id),
	})

	chType, chExtra := newCh.ChannelType(), newCh.ExtraData()
	clientCh, clientRequests, err := newCh.Accept()
	if err != nil {
		c.cancelFn("failed obtaining channel, err=%v", err)
		return
	}

	c.channelStore.Set(fmt.Sprintf("%v", channelID), clientCh)
	openChData := (sshtypes.OpenChannel{
		ChannelID:        channelID,
		ChannelType:      chType,
		ChannelExtraData: chExtra,
	}).Encode()
	if _, err := streamW.Write([]byte(openChData)); err != nil {
		c.cancelFn("failed writing open channel to stream, err=%v", err)
		return
	}

	go func() {
		defer clientCh.Close()
		_, err = io.Copy(sshtypes.NewDataWriter(streamW, channelID), clientCh)
		if err != nil {
			c.cancelFn("failed copying ssh buffer, err=%v", err)
		}
	}()

	go func() {
		for req := range clientRequests {
			data := (sshtypes.SSHRequest{
				ChannelID:   channelID,
				RequestType: req.Type,
				WantReply:   req.WantReply,
				Payload:     req.Payload,
			}).Encode()
			log.With("sid", c.sid, "conn", c.id, "ch", channelID, "type", req.Type).Debug("received client ssh request")
			_, err := streamW.Write([]byte(data))
			if err != nil {
				c.cancelFn("failed writing to stream, err=%v", err)
				return
			}
			if req.WantReply {
				if err := req.Reply(true, nil); err != nil {
					c.cancelFn("failed sending response to channel, err=%v", err)
					return
				}
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
	privateKey, err := keys.DecodeOpenSSHPrivateKey(privateKeyPemBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to decode hosts key: %v", err)
	}
	hostsKey, err = ssh.NewSignerFromKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create hosts key signer: %v", err)
	}
	return hostsKey, nil
}
