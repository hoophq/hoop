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

const defaultMySQLPort = "3307"

type MySQLServer struct {
	listenPort      string
	client          pb.ClientTransport
	connectionStore memory.Store
	listener        net.Listener
}

func NewMySQLServer(listenPort string, client pb.ClientTransport) *MySQLServer {
	if listenPort == "" {
		listenPort = defaultMySQLPort
	}
	return &MySQLServer{
		listenPort:      listenPort,
		client:          client,
		connectionStore: memory.New(),
	}
}

func (s *MySQLServer) Serve(sessionID string) error {
	listenAddr := fmt.Sprintf("127.0.0.1:%s", s.listenPort)
	lis, err := net.Listen("tcp4", listenAddr)
	if err != nil {
		return fmt.Errorf("failed listening to address %v, err=%v", listenAddr, err)
	}
	s.listener = lis
	go func() {
		connectionID := 0
		for {
			connectionID++
			mysqlClient, err := lis.Accept()
			if err != nil {
				log.Infof("failed obtain listening connection, err=%v", err)
				lis.Close()
				break
			}
			go s.serveConn(sessionID, strconv.Itoa(connectionID), mysqlClient)
		}
	}()
	return nil
}

func (s *MySQLServer) serveConn(sessionID, connectionID string, mysqlClient net.Conn) {
	defer func() {
		log.Infof("session=%v | conn=%s | remote=%s - closing tcp connection",
			sessionID, connectionID, mysqlClient.RemoteAddr())
		s.connectionStore.Del(connectionID)
		if err := mysqlClient.Close(); err != nil {
			log.Warnf("failed closing client connection, err=%v", err)
		}
		_ = s.client.Send(&pb.Packet{
			Type: pbagent.TCPConnectionClose,
			Spec: map[string][]byte{
				pb.SpecClientConnectionID: []byte(connectionID),
				pb.SpecGatewaySessionID:   []byte(sessionID),
			}})
	}()
	connWrapper := pb.NewConnectionWrapper(mysqlClient, make(chan struct{}))
	s.connectionStore.Set(connectionID, connWrapper)

	log.Infof("session=%v | conn=%s | client=%s - connected", sessionID, connectionID, mysqlClient.RemoteAddr())
	w := pb.NewStreamWriter(s.client, pbagent.MySQLConnectionWrite, map[string][]byte{
		string(pb.SpecClientConnectionID): []byte(connectionID),
		string(pb.SpecGatewaySessionID):   []byte(sessionID),
	})
	// it will make the mysql proxy to initialize
	w.Write(nil)
	if _, err := io.CopyBuffer(w, mysqlClient, nil); err != nil {
		log.Infof("failed copying buffer, err=%v", err)
		connWrapper.Close()
	}
}

func (s *MySQLServer) PacketWriteClient(connectionID string, pkt *pb.Packet) (int, error) {
	conn, err := s.getConnection(connectionID)
	if err != nil {
		return 0, err
	}
	return conn.Write(pkt.Payload)
}

func (s *MySQLServer) CloseTCPConnection(connectionID string) {
	if conn, err := s.getConnection(connectionID); err == nil {
		_ = conn.Close()
	}
}

func (s *MySQLServer) Close() error { return s.listener.Close() }

func (s *MySQLServer) getConnection(connectionID string) (io.WriteCloser, error) {
	connWrapperObj := s.connectionStore.Get(connectionID)
	conn, ok := connWrapperObj.(io.WriteCloser)
	if !ok {
		return nil, fmt.Errorf("local connection %q not found", connectionID)
	}
	return conn, nil
}

func (s *MySQLServer) ListenPort() string {
	return s.listenPort
}
