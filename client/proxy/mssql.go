package proxy

import (
	"fmt"
	"io"
	"net"
	"strconv"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/memory"
	"github.com/hoophq/hoop/common/mssqltypes"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
)

const minPacketSize = 512

type MSSQLServer struct {
	listenAddr      string
	client          pb.ClientTransport
	connectionStore memory.Store
	listener        net.Listener
}

func NewMSSQLServer(listenPort string, client pb.ClientTransport) *MSSQLServer {
	listenAddr := defaultListenAddr(defaultMSSQLPort)
	if listenPort != "" {
		listenAddr = defaultListenAddr(listenPort)
	}
	return &MSSQLServer{
		listenAddr:      listenAddr,
		client:          client,
		connectionStore: memory.New(),
	}
}

func (s *MSSQLServer) Serve(sessionID string) error {
	lis, err := net.Listen("tcp4", s.listenAddr)
	if err != nil {
		return fmt.Errorf("failed listening to address %v, err=%v", s.listenAddr, err)
	}
	s.listener = lis
	go func() {
		connectionID := 0
		for {
			connectionID++
			mssqlClient, err := lis.Accept()
			if err != nil {
				log.Infof("failed obtain listening connection, err=%v", err)
				lis.Close()
				break
			}
			go s.serveConn(sessionID, strconv.Itoa(connectionID), mssqlClient)
		}
	}()
	return nil
}

func (s *MSSQLServer) serveConn(sessionID, connectionID string, mssqlClient net.Conn) {
	defer func() {
		log.Infof("session=%v | conn=%s | remote=%s - closing tcp connection",
			sessionID, connectionID, mssqlClient.RemoteAddr())
		s.connectionStore.Del(connectionID)
		_ = mssqlClient.Close()
		_ = s.client.Send(&pb.Packet{
			Type: pbagent.TCPConnectionClose,
			Spec: map[string][]byte{
				pb.SpecClientConnectionID: []byte(connectionID),
				pb.SpecGatewaySessionID:   []byte(sessionID),
			}})
	}()
	connWrapper := pb.NewConnectionWrapper(mssqlClient, make(chan struct{}))
	s.connectionStore.Set(connectionID, connWrapper)

	log.Infof("session=%v | conn=%s | client=%s - connected", sessionID, connectionID, mssqlClient.RemoteAddr())
	w := pb.NewStreamWriter(s.client, pbagent.MSSQLConnectionWrite, map[string][]byte{
		string(pb.SpecClientConnectionID): []byte(connectionID),
		string(pb.SpecGatewaySessionID):   []byte(sessionID),
	})
	if _, err := copyMSSQLBuffer(&mssqlStreamWriter{w, mssqltypes.DefaultPacketSize}, mssqlClient); err != nil {
		log.Infof("failed copying buffer, err=%v", err)
		connWrapper.Close()
	}
}

func (s *MSSQLServer) PacketWriteClient(connectionID string, pkt *pb.Packet) (int, error) {
	conn, err := s.getConnection(connectionID)
	if err != nil {
		sid := string(pkt.Spec[pb.SpecGatewaySessionID])
		log.Warnf("session=%v | conn=%v | discarding packet, reason=%v", sid, connectionID, err)
		return 0, nil
	}
	return conn.Write(pkt.Payload)
}

func (s *MSSQLServer) CloseTCPConnection(connectionID string) {
	if conn, err := s.getConnection(connectionID); err == nil {
		_ = conn.Close()
	}
}

func (s *MSSQLServer) Close() error { return s.listener.Close() }
func (s *MSSQLServer) Host() Host   { return getListenAddr(s.listenAddr) }

func (s *MSSQLServer) getConnection(connectionID string) (io.WriteCloser, error) {
	connWrapperObj := s.connectionStore.Get(connectionID)
	conn, ok := connWrapperObj.(io.WriteCloser)
	if !ok {
		return nil, fmt.Errorf("local connection %q not found", connectionID)
	}
	return conn, nil
}

type mssqlStreamWriter struct {
	stream     io.Writer
	packetSize int
}

func (w *mssqlStreamWriter) Write(p []byte) (int, error) {
	pktList, err := mssqltypes.DecodeFull(p, w.packetSize)
	if err != nil {
		return 0, err
	}
	for _, pkt := range pktList {
		if pkt.Type() == mssqltypes.PacketLogin7Type {
			l := mssqltypes.DecodeLogin(pkt.Frame)
			// TODO: the server must reply informing the packet size accept
			// for now we're assuming that this value is being accepted by the server
			if l.PacketSize() >= minPacketSize {
				log.Infof("setting packet size from=%v, to=%v", w.packetSize, l.PacketSize())
				w.packetSize = int(l.PacketSize())
			}
		}
		if _, err := w.stream.Write(pkt.Encode()); err != nil {
			return 0, err
		}
	}
	return 0, nil
}

func copyMSSQLBuffer(dst io.Writer, src io.Reader) (written int64, err error) {
	for {
		buf := make([]byte, 32*1024)
		nr, er := src.Read(buf)
		if nr > 0 {
			_, ew := dst.Write(buf[0:nr])
			if ew != nil {
				err = ew
				break
			}
		}
		if er != nil {
			if er != io.EOF {
				err = er
			}
			break
		}
	}
	return written, err
}
