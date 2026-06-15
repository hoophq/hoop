package proxy

import (
	"fmt"
	"io"
	"net"
	"strconv"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/memory"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
)

type OracleServer struct {
	listenAddr      string
	client          pb.ClientTransport
	connectionStore memory.Store
	listener        net.Listener
}

func NewOracleServer(listenPort string, client pb.ClientTransport) *OracleServer {
	listenAddr := defaultListenAddr(defaultOraclePort)
	if listenPort != "" {
		listenAddr = defaultListenAddr(listenPort)
	}
	return &OracleServer{
		listenAddr:      listenAddr,
		client:          client,
		connectionStore: memory.New(),
	}
}

func (s *OracleServer) Serve(sessionID string) error {
	lis, err := net.Listen("tcp4", s.listenAddr)
	if err != nil {
		return fmt.Errorf("failed listening on %v: %v", s.listenAddr, err)
	}
	s.listener = lis
	go func() {
		connectionID := 0
		for {
			connectionID++
			oracleClient, err := lis.Accept()
			if err != nil {
				log.Infof("oracle proxy listener closed, err=%v", err)
				lis.Close()
				break
			}
			go s.serveConn(sessionID, strconv.Itoa(connectionID), oracleClient)
		}
	}()
	return nil
}

func (s *OracleServer) serveConn(sessionID, connectionID string, oracleClient net.Conn) {
	defer func() {
		log.Infof("session=%v | conn=%s | remote=%s - oracle tcp connection closing",
			sessionID, connectionID, oracleClient.RemoteAddr())
		s.connectionStore.Del(connectionID)
		_ = oracleClient.Close()
		_ = s.client.Send(&pb.Packet{
			Type: pbagent.TCPConnectionClose,
			Spec: map[string][]byte{
				pb.SpecClientConnectionID: []byte(connectionID),
				pb.SpecGatewaySessionID:   []byte(sessionID),
			},
		})
	}()

	connWrapper := pb.NewConnectionWrapper(oracleClient, make(chan struct{}))
	s.connectionStore.Set(connectionID, connWrapper)

	log.Infof("session=%v | conn=%s | client=%s - oracle client connected",
		sessionID, connectionID, oracleClient.RemoteAddr())

	w := pb.NewStreamWriter(s.client, pbagent.OracleConnectionWrite, map[string][]byte{
		string(pb.SpecClientConnectionID): []byte(connectionID),
		string(pb.SpecGatewaySessionID):   []byte(sessionID),
	})
	buf := make([]byte, 32*1024)
	for {
		nr, er := oracleClient.Read(buf)
		if nr > 0 {
			if _, ew := w.Write(buf[:nr]); ew != nil {
				log.Infof("session=%v | conn=%s - failed writing to stream: %v", sessionID, connectionID, ew)
				break
			}
		}
		if er != nil {
			if er != io.EOF {
				log.Infof("session=%v | conn=%s - read error: %v", sessionID, connectionID, er)
			}
			break
		}
	}
	connWrapper.Close()
}

func (s *OracleServer) PacketWriteClient(connectionID string, pkt *pb.Packet) (int, error) {
	conn, err := s.getConnection(connectionID)
	if err != nil {
		sid := string(pkt.Spec[pb.SpecGatewaySessionID])
		log.Warnf("session=%v | conn=%v | discarding oracle packet: %v", sid, connectionID, err)
		return 0, nil
	}
	return conn.Write(pkt.Payload)
}

func (s *OracleServer) CloseTCPConnection(connectionID string) {
	if conn, err := s.getConnection(connectionID); err == nil {
		_ = conn.Close()
	}
}

func (s *OracleServer) Close() error { return s.listener.Close() }
func (s *OracleServer) Host() Host   { return getListenAddr(s.listenAddr) }

func (s *OracleServer) getConnection(connectionID string) (io.WriteCloser, error) {
	obj := s.connectionStore.Get(connectionID)
	conn, ok := obj.(io.WriteCloser)
	if !ok {
		return nil, fmt.Errorf("oracle connection %q not found", connectionID)
	}
	return conn, nil
}
