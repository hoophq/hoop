package proxy

import (
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/memory"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
)

const (
	defaultMongoDBPort = "27018"
)

type MongoDBServer struct {
	listenAddr      string
	client          pb.ClientTransport
	connectionStore memory.Store
	listener        net.Listener
}

func NewMongoDBServer(proxyPort string, client pb.ClientTransport) *MongoDBServer {
	listenAddr := fmt.Sprintf("127.0.0.1:%s", defaultMongoDBPort)
	if proxyPort != "" {
		listenAddr = fmt.Sprintf("127.0.0.1:%s", proxyPort)
	}
	return &MongoDBServer{
		listenAddr:      listenAddr,
		client:          client,
		connectionStore: memory.New(),
	}
}

func (p *MongoDBServer) Serve(sessionID string) error {
	listenAddr := p.listenAddr
	lis, err := net.Listen("tcp4", listenAddr)
	if err != nil {
		return fmt.Errorf("failed listening to address %v, err=%v", listenAddr, err)
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
			go p.serveConn(sessionID, strconv.Itoa(connectionID), conn)
		}
	}()
	return nil
}

func (s *MongoDBServer) serveConn(sessionID, connectionID string, conn net.Conn) {
	defer func() {
		log.Infof("session=%v | conn=%s | client=%s - closing tcp connection",
			sessionID, connectionID, conn.RemoteAddr())
		s.connectionStore.Del(connectionID)
		if err := conn.Close(); err != nil {
			log.Warnf("failed closing client connection, err=%v", err)
		}
		_ = s.client.Send(&pb.Packet{
			Type: pbagent.TCPConnectionClose,
			Spec: map[string][]byte{
				pb.SpecClientConnectionID: []byte(connectionID),
				pb.SpecGatewaySessionID:   []byte(sessionID),
			}})
	}()
	s.connectionStore.Set(connectionID, conn)
	log.Infof("session=%v | conn=%s | client=%s - connected", sessionID, connectionID, conn.RemoteAddr())
	stream := pb.NewStreamWriter(s.client, pbagent.MongoDBConnectionWrite, map[string][]byte{
		string(pb.SpecClientConnectionID): []byte(connectionID),
		string(pb.SpecGatewaySessionID):   []byte(sessionID),
	})
	if written, err := io.Copy(stream, conn); err != nil {
		log.Warnf("failed copying buffer, written=%v, err=%v", written, err)
	}
}

func (s *MongoDBServer) PacketWriteClient(connectionID string, pkt *pb.Packet) (int, error) {
	conn, err := s.getConnection(connectionID)
	if err != nil {
		log.Warnf("receive packet (length=%v) after connection (%v) is closed", len(pkt.Payload), connectionID)
		return 0, nil
	}
	return conn.Write(pkt.Payload)
}

func (s *MongoDBServer) CloseTCPConnection(connectionID string) {
	if conn, err := s.getConnection(connectionID); err == nil {
		_ = conn.Close()
	}
}

func (s *MongoDBServer) Close() error { return s.listener.Close() }

func (s *MongoDBServer) getConnection(connectionID string) (io.WriteCloser, error) {
	connectionObj := s.connectionStore.Get(connectionID)
	conn, ok := connectionObj.(io.WriteCloser)
	if !ok {
		return nil, fmt.Errorf("local connection %q not found", connectionID)
	}
	return conn, nil
}

func (s *MongoDBServer) ListenPort() string {
	parts := strings.Split(s.listenAddr, ":")
	if len(parts) == 2 {
		return parts[1]
	}
	return ""
}
