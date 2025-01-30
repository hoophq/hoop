package proxy

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/memory"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
)

const (

	// keep it the same value for Linux MTU loopback interfaces
	defaultBufferSize = 16 * 1024 // 16k

	maxPacketSize = 1024 * 1024 * 16 // 16 MiB
)

type MySQLServer struct {
	listenAddr      string
	client          pb.ClientTransport
	connectionStore memory.Store
	listener        net.Listener
}

func NewMySQLServer(listenPort string, client pb.ClientTransport) *MySQLServer {
	listenAddr := defaultListenAddr(defaultMySQLPort)
	if listenPort != "" {
		listenAddr = defaultListenAddr(listenPort)
	}
	return &MySQLServer{
		listenAddr:      listenAddr,
		client:          client,
		connectionStore: memory.New(),
	}
}

func (s *MySQLServer) Serve(sessionID string) error {
	lis, err := net.Listen("tcp4", s.listenAddr)
	if err != nil {
		return fmt.Errorf("failed listening to address %v, err=%v", s.listenAddr, err)
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
	if err := copyMySQLLBuffer(w, mysqlClient); err != nil {
		log.Warnf("failed copying buffer, err=%v", err)
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

func (s *MySQLServer) Host() Host { return getListenAddr(s.listenAddr) }

func copyMySQLLBuffer(dst io.Writer, src io.Reader) (err error) {
	for {
		var header [4]byte
		_, err = io.ReadFull(src, header[:3])
		if err != nil {
			return err
		}
		var sequenceID [1]byte
		if _, err := src.Read(sequenceID[:]); err != nil {
			return err
		}
		pktLen := int(binary.LittleEndian.Uint32(header[:]))
		if pktLen >= maxPacketSize {
			return fmt.Errorf("max packet size reached (max:%v, pkt:%v)", maxPacketSize, pktLen)
		}
		frame := make([]byte, pktLen)
		log.Debugf("pktlen=%v, header=% X", pktLen, header[:3])
		copied := 0
		for {
			buf := make([]byte, defaultBufferSize)
			nr, er := src.Read(buf)
			if er != nil {
				return
			}

			copied += copy(frame[copied:], buf[0:nr])
			log.Debugf("pktlen=%v, connread=%v, copied=%v", pktLen, nr, copied)
			if pktLen > nr {
				pktLen -= nr
				continue
			}

			_, err = dst.Write(encodeMySQLPacket(header, sequenceID[0], frame))
			if err != nil {
				return
			}
			break
		}
	}
}

func encodeMySQLPacket(header [4]byte, sequenceID byte, frame []byte) []byte {
	return append(
		append(header[:3], sequenceID),
		frame...)
}
