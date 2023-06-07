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

const defaultPostgresPort = "5433"

type PGServer struct {
	listenPort      string
	client          pb.ClientTransport
	connectionStore memory.Store
	listener        net.Listener
}

func NewPGServer(listenPort string, client pb.ClientTransport) *PGServer {
	if listenPort == "" {
		listenPort = defaultPostgresPort
	}
	return &PGServer{
		listenPort:      listenPort,
		client:          client,
		connectionStore: memory.New(),
	}
}

func (p *PGServer) Serve(sessionID string) error {
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
			pgClient, err := lis.Accept()
			if err != nil {
				log.Infof("failed obtain listening connection, err=%v", err)
				lis.Close()
				break
			}
			go p.serveConn(sessionID, strconv.Itoa(connectionID), pgClient)
		}
	}()
	return nil
}

func (p *PGServer) serveConn(sessionID, connectionID string, pgClient net.Conn) {
	defer func() {
		log.Infof("session=%v | conn=%s | remote=%s - closing tcp connection",
			sessionID, connectionID, pgClient.RemoteAddr())
		p.connectionStore.Del(connectionID)
		if err := pgClient.Close(); err != nil {
			log.Warnf("failed closing client connection, err=%v", err)
		}
		_ = p.client.Send(&pb.Packet{
			Type: pbagent.TCPConnectionClose,
			Spec: map[string][]byte{
				pb.SpecClientConnectionID: []byte(connectionID),
				pb.SpecGatewaySessionID:   []byte(sessionID),
			}})
	}()
	connWrapper := pb.NewConnectionWrapper(pgClient, make(chan struct{}))
	p.connectionStore.Set(connectionID, connWrapper)

	log.Infof("session=%v | conn=%s | client=%s - connected", sessionID, connectionID, pgClient.RemoteAddr())
	pgServerWriter := pb.NewStreamWriter(p.client, pbagent.PGConnectionWrite, map[string][]byte{
		string(pb.SpecClientConnectionID): []byte(connectionID),
		string(pb.SpecGatewaySessionID):   []byte(sessionID),
	})
	if _, err := io.CopyBuffer(pgServerWriter, pgClient, nil); err != nil {
		log.Infof("failed copying buffer, err=%v", err)
		connWrapper.Close()
	}
}

func (p *PGServer) PacketWriteClient(connectionID string, pkt *pb.Packet) (int, error) {
	conn, err := p.getConnection(connectionID)
	if err != nil {
		return 0, err
	}
	return conn.Write(pkt.Payload)
}

func (p *PGServer) CloseTCPConnection(connectionID string) {
	if conn, err := p.getConnection(connectionID); err == nil {
		_ = conn.Close()
	}
}

func (p *PGServer) Close() error { return p.listener.Close() }

func (p *PGServer) getConnection(connectionID string) (io.WriteCloser, error) {
	connWrapperObj := p.connectionStore.Get(connectionID)
	conn, ok := connWrapperObj.(io.WriteCloser)
	if !ok {
		return nil, fmt.Errorf("local connection %q not found", connectionID)
	}
	return conn, nil
}

func (p *PGServer) ListenPort() string {
	return p.listenPort
}
