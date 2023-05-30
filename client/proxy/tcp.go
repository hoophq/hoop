package proxy

import (
	"fmt"
	"io"
	"net"
	"strconv"

	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/memory"
	pb "github.com/runopsio/hoop/common/proto"
	pbagent "github.com/runopsio/hoop/common/proto/agent"
)

type TCPServer struct {
	listenPort      string
	client          pb.ClientTransport
	connectionStore memory.Store
	listener        net.Listener
	packetType      pb.PacketType
}

func NewTCPServer(listenPort string, client pb.ClientTransport, packetType pb.PacketType) *TCPServer {
	return &TCPServer{
		listenPort:      listenPort,
		client:          client,
		connectionStore: memory.New(),
		packetType:      packetType,
	}
}

func (p *TCPServer) Serve(sessionID string) error {
	listenAddr := fmt.Sprintf("127.0.0.1:%s", p.listenPort)
	lis, err := net.Listen("tcp4", listenAddr)
	if err != nil {
		return fmt.Errorf("failed listening to address %v, err=%v", listenAddr, err)
	}
	p.listener = lis
	go func() {
		connectionID := 0
		for {
			connectionID++
			tcpClient, err := lis.Accept()
			if err != nil {
				log.Printf("failed obtain listening connection, err=%v", err)
				lis.Close()
				break
			}
			go p.serveConn(sessionID, strconv.Itoa(connectionID), tcpClient)
		}
	}()
	return nil
}

func (p *TCPServer) serveConn(sessionID, connectionID string, tcpClient net.Conn) {
	defer func() {
		log.Printf("session=%v | conn=%s | remote=%s - closing tcp connection",
			sessionID, connectionID, tcpClient.RemoteAddr())
		p.connectionStore.Del(connectionID)
		if err := tcpClient.Close(); err != nil {
			// TODO: log warn
			log.Printf("failed closing client connection, err=%v", err)
		}
		_ = p.client.Send(&pb.Packet{
			Type: pbagent.TCPConnectionClose,
			Spec: map[string][]byte{
				pb.SpecClientConnectionID: []byte(connectionID),
				pb.SpecGatewaySessionID:   []byte(sessionID),
			}})
	}()
	connWrapper := pb.NewConnectionWrapper(tcpClient, make(chan struct{}))
	p.connectionStore.Set(connectionID, connWrapper)

	log.Printf("session=%v | conn=%s | client=%s - connected",
		sessionID, connectionID, tcpClient.RemoteAddr())
	spec := map[string][]byte{
		string(pb.SpecGatewaySessionID):   []byte(sessionID),
		string(pb.SpecClientConnectionID): []byte(connectionID),
	}
	tcpServerWriter := pb.NewStreamWriter(p.client, p.packetType, spec)
	// first make a connection in the agent to start exchanging packets.
	// this is required for mysql server, where the server sends the packet
	// first.
	if err := p.connectTCP(sessionID, connectionID); err != nil {
		log.Printf("session=%v | conn=%s - failed initializing tcp connection, err=%v",
			sessionID, connectionID, err)
	}
	if _, err := io.Copy(tcpServerWriter, tcpClient); err != nil {
		log.Printf("failed copying buffer, err=%v", err)
		connWrapper.Close()
	}
}

func (p *TCPServer) PacketWriteClient(connectionID string, pkt *pb.Packet) (int, error) {
	conn, err := p.getConnection(connectionID)
	if err != nil {
		return 0, err
	}
	return conn.Write(pkt.Payload)
}

func (p *TCPServer) PacketCloseConnection(connectionID string) {
	if conn, err := p.getConnection(connectionID); err == nil {
		_ = conn.Close()
	}
	_ = p.listener.Close()
}

func (p *TCPServer) getConnection(connectionID string) (io.WriteCloser, error) {
	connWrapperObj := p.connectionStore.Get(connectionID)
	conn, ok := connWrapperObj.(io.WriteCloser)
	if !ok {
		return nil, fmt.Errorf("local connection %q not found", connectionID)
	}
	return conn, nil
}

func (p *TCPServer) connectTCP(sessionID, connectionID string) error {
	return p.client.Send(&pb.Packet{
		Type: p.packetType.String(),
		Spec: map[string][]byte{
			pb.SpecGatewaySessionID:    []byte(sessionID),
			pb.SpecClientConnectionID:  []byte(connectionID),
			pb.SpecTCPServerConnectKey: nil,
		},
	})
}

func (p *TCPServer) ListenPort() string {
	return p.listenPort
}
