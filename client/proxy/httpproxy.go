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
	listenAddr      string
	client          pb.ClientTransport
	connectionStore memory.Store
	listener        net.Listener
	packetType      pb.PacketType
	proxyBaseURL    string
}

func NewHttpProxy(listenPort string, client pb.ClientTransport, packetType pb.PacketType) *HttpProxy {
	listenAddr := defaultListenAddr(defaultHttpProxyPort)
	if listenPort != "" {
		listenAddr = defaultListenAddr(listenPort)
	}
	return &HttpProxy{
		listenAddr:      listenAddr,
		client:          client,
		connectionStore: memory.New(),
		packetType:      packetType,
		proxyBaseURL:    fmt.Sprintf("http://%s", listenAddr),
	}
}

func (p *HttpProxy) Serve(sessionID string) error {
	lis, err := net.Listen("tcp4", p.listenAddr)
	if err != nil {
		return fmt.Errorf("failed listening to address %v, err=%v", p.listenAddr, err)
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
			log.Warnf("failed closing client connection, err=%v", err)
		}
	}()
	connWrapper := pb.NewConnectionWrapper(tcpClient, make(chan struct{}))
	p.connectionStore.Set(connectionID, connWrapper)

	log.Printf("session=%v | conn=%s | client=%s - connected",
		sessionID, connectionID, tcpClient.RemoteAddr())
	spec := map[string][]byte{
		string(pb.SpecGatewaySessionID):   []byte(sessionID),
		string(pb.SpecClientConnectionID): []byte(connectionID),
		string(pb.SpecHttpProxyBaseUrl):   []byte(p.proxyBaseURL),
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
		log.Warnf("receive packet (length=%v) after connection (%v) is closed", len(pkt.Payload), connectionID)
		return 0, nil
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

func (s *HttpProxy) Host() Host { return getListenAddr(s.listenAddr) }
