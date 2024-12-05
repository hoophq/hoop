package proxy

import (
	"fmt"
	"io"
	"net"
	"strconv"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/memory"
	pb "github.com/hoophq/hoop/common/proto"
)

type HttpProxy struct {
	listenPort      string
	client          pb.ClientTransport
	connectionStore memory.Store
	listener        net.Listener
	packetType      pb.PacketType
}

func NewHttpProxy(listenPort string, client pb.ClientTransport, packetType pb.PacketType) *HttpProxy {
	return &HttpProxy{
		listenPort:      listenPort,
		client:          client,
		connectionStore: memory.New(),
		packetType:      packetType,
	}
}

func (p *HttpProxy) Serve(sessionID string) error {
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

func (p *HttpProxy) serveConn(sessionID, connectionID string, tcpClient net.Conn) {
	defer func() {
		log.Printf("session=%v | conn=%s | remote=%s - closing http proxy connection",
			sessionID, connectionID, tcpClient.RemoteAddr())
		p.connectionStore.Del(connectionID)
		if err := tcpClient.Close(); err != nil {
			// TODO: log warn
			log.Printf("failed closing client connection, err=%v", err)
		}
	}()
	connWrapper := pb.NewConnectionWrapper(tcpClient, make(chan struct{}))
	p.connectionStore.Set(connectionID, connWrapper)

	log.Printf("session=%v | conn=%s | client=%s - connected",
		sessionID, connectionID, tcpClient.RemoteAddr())
	spec := map[string][]byte{
		string(pb.SpecGatewaySessionID):   []byte(sessionID),
		string(pb.SpecClientConnectionID): []byte(connectionID),
	}
	httpProxyWriter := pb.NewStreamWriter(p.client, p.packetType, spec)
	if _, err := io.Copy(httpProxyWriter, tcpClient); err != nil {
		log.Printf("failed copying buffer, err=%v", err)
		connWrapper.Close()
	}
}

func (p *HttpProxy) PacketWriteClient(connectionID string, pkt *pb.Packet) (int, error) {
	conn, err := p.getConnection(connectionID)
	if err != nil {
		return 0, err
	}
	return conn.Write(pkt.Payload)
}

func (p *HttpProxy) CloseTCPConnection(connectionID string) {
	if conn, err := p.getConnection(connectionID); err == nil {
		_ = conn.Close()
	}
}

func (p *HttpProxy) Close() error { return p.listener.Close() }

func (p *HttpProxy) getConnection(connectionID string) (io.WriteCloser, error) {
	connWrapperObj := p.connectionStore.Get(connectionID)
	conn, ok := connWrapperObj.(io.WriteCloser)
	if !ok {
		return nil, fmt.Errorf("local connection %q not found", connectionID)
	}
	return conn, nil
}

func (p *HttpProxy) ListenPort() string {
	return p.listenPort
}
