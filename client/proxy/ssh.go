package proxy

import (
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"io"
	"net"
	"strconv"

	sshtypes "libhoop/proxy/ssh/types"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/memory"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	"golang.org/x/crypto/ssh"
)

type SSHServer struct {
	listenAddr      string
	client          pb.ClientTransport
	serverConfig    *ssh.ServerConfig
	connectionStore memory.Store
	listener        net.Listener
	packetType      pb.PacketType
}

func NewSSHServer(listenPort string, client pb.ClientTransport, packetType pb.PacketType) *SSHServer {
	listenAddr := defaultListenAddr(defaultSSHPort)
	if listenPort != "" {
		listenAddr = defaultListenAddr(listenPort)
	}

	config := &ssh.ServerConfig{
		NoClientAuth: true, // Ignore client authentication
		// PasswordCallback: func(conn ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
		// 	// Ignore client credentials and always allow
		// 	return &ssh.Permissions{}, nil
		// },
	}
	return &SSHServer{
		listenAddr:      listenAddr,
		client:          client,
		serverConfig:    config,
		connectionStore: memory.New(),
		packetType:      packetType,
	}
}

func (p *SSHServer) Serve(sid string) error {
	noopPrivKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("failed to generate host key: %v", err)
	}
	noopSigner, err := ssh.NewSignerFromKey(noopPrivKey)
	if err != nil {
		return fmt.Errorf("failed to signer host key: %v", err)
	}
	// It requires at least one key to host an SSH server.
	// Generate a random key just to satisfy this requirement.
	// In practice, the client will establish a plain SSH connection
	// to localhost TLS secured by the gRPC gateway.
	p.serverConfig.AddHostKey(noopSigner)

	lis, err := net.Listen("tcp4", p.listenAddr)
	if err != nil {
		return fmt.Errorf("failed listening to address %v, err=%v", p.listenAddr, err)
	}
	p.listener = lis
	go func() {
		connectionID := 0
		for {
			connectionID++
			conn, err := lis.Accept()
			if err != nil {
				log.Infof("failed obtain listening connection, err=%v", err)
				lis.Close()
				break
			}
			go p.serveConn(sid, strconv.Itoa(connectionID), conn)
		}
	}()
	return nil
}

func (p *SSHServer) serveConn(sid, connectionID string, conn net.Conn) {
	defer func() {
		log.Infof("session=%v | conn=%s | remote=%s - closing tcp connection",
			sid, connectionID, conn.RemoteAddr())
		p.connectionStore.Del(connectionID)
		_ = conn.Close()
		_ = p.client.Send(&pb.Packet{
			Type: pbagent.TCPConnectionClose,
			Spec: map[string][]byte{
				pb.SpecClientConnectionID: []byte(connectionID),
				pb.SpecGatewaySessionID:   []byte(sid),
			}})
	}()

	p.connectionStore.Set(connectionID, conn)
	sshConn, clientNewCh, sshReq, err := ssh.NewServerConn(conn, p.serverConfig)
	if err != nil {
		log.Infof("session=%v | conn=%v - failed to establish handshake with client: %v", sid, connectionID, err)
		conn.Close()
		return
	}
	go ssh.DiscardRequests(sshReq)
	defer sshConn.Close()

	spec := map[string][]byte{
		string(pb.SpecGatewaySessionID):   []byte(sid),
		string(pb.SpecClientConnectionID): []byte(connectionID),
	}
	streamWriter := pb.NewStreamWriter(p.client, p.packetType, spec)
	channelID := uint16(0)
	for newCh := range clientNewCh {
		channelID++
		go p.handleChannel(newCh, streamWriter, connectionID, channelID)
	}
}

func (p *SSHServer) handleChannel(newCh ssh.NewChannel, streamW io.Writer, connID string, channelID uint16) {
	log.With("ch", channelID, "conn", connID).Infof("received new channel, type=%v", newCh.ChannelType())
	chType, chExtra := newCh.ChannelType(), newCh.ExtraData()
	clientCh, clientRequests, err := newCh.Accept()
	if err != nil {
		log.With("ch", channelID, "conn", connID).Errorf("failed obtaining channel, err=%v", err)
		return
	}

	p.connectionStore.Set(fmt.Sprintf("%s:%v", connID, channelID), clientCh)
	openChData := (sshtypes.OpenChannel{
		ChannelID:        channelID,
		ChannelType:      chType,
		ChannelExtraData: chExtra,
	}).Encode()
	if _, err := streamW.Write([]byte(openChData)); err != nil {
		log.With("ch", channelID, "conn", connID).Debugf("unable to write open channel to stream, err=%v", err)
		return
	}

	go func() {
		defer clientCh.Close()
		_, err = io.Copy(sshtypes.NewDataWriter(streamW, channelID), clientCh)
		log.With("ch", channelID, "conn", connID).Debugf("done copying ssh buffer, err=%v", err)
	}()

	go func() {
		for req := range clientRequests {
			data := (sshtypes.SSHRequest{
				ChannelID:   channelID,
				RequestType: req.Type,
				WantReply:   req.WantReply,
				Payload:     req.Payload,
			}).Encode()
			log.With("ch", channelID, "conn", connID).Debugf("received client ssh request: %s", req.Type)
			_, err := streamW.Write([]byte(data))
			if err != nil {
				log.With("ch", channelID, "conn", connID).Errorf("failed writing to stream, err=%v", err)
				return
			}
			if req.WantReply {
				if err := req.Reply(true, nil); err != nil {
					log.With("ch", channelID, "conn", connID).Errorf("failed sending response to channel, err=%v", err)
					return
				}
			}
		}
		log.With("ch", channelID, "conn", connID).Debugf("done processing ssh client requests")
	}()
}

func (p *SSHServer) PacketWriteClient(connectionID string, pkt *pb.Packet) (int, error) {
	switch sshtypes.DecodeType(pkt.Payload) {
	case sshtypes.DataType:
		var data sshtypes.Data
		if err := sshtypes.Decode(pkt.Payload, &data); err != nil {
			return 0, err
		}
		connWrapperObj := p.connectionStore.Get(fmt.Sprintf("%s:%v", connectionID, data.ChannelID))
		clientCh, ok := connWrapperObj.(io.WriteCloser)
		if !ok {
			return 0, fmt.Errorf("local channel %q not found", connectionID)
		}
		return clientCh.Write(data.Payload)
	case sshtypes.CloseChannelType:
		var cc sshtypes.CloseChannel
		if err := sshtypes.Decode(pkt.Payload, &cc); err != nil {
			return 0, err
		}
		obj := p.connectionStore.Get(fmt.Sprintf("%s:%v", connectionID, cc.ID))
		if clientCh, ok := obj.(io.Closer); ok {
			log.With("ch", cc.ID, "conn", connectionID).Infof("closing client ssh channel type=%v, err=%v",
				cc.Type, clientCh.Close())
		}
		return 0, nil
	default:
		return 0, fmt.Errorf("unknown ssh message type (%v)", sshtypes.DecodeType(pkt.Payload))
	}
}

func (p *SSHServer) CloseTCPConnection(connectionID string) {
	if conn, err := p.getConnection(connectionID); err == nil {
		_ = conn.Close()
	}
}

func (p *SSHServer) Close() error { return p.listener.Close() }

func (p *SSHServer) getConnection(connectionID string) (io.WriteCloser, error) {
	connWrapperObj := p.connectionStore.Get(connectionID)
	conn, ok := connWrapperObj.(io.WriteCloser)
	if !ok {
		return nil, fmt.Errorf("local connection %q not found", connectionID)
	}
	return conn, nil
}

func (p *SSHServer) Host() Host { return getListenAddr(p.listenAddr) }
