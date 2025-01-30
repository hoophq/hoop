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

type MongoDBServer struct {
	listenAddr      string
	client          pb.ClientTransport
	connectionStore memory.Store
	listener        net.Listener
}

func NewMongoDBServer(proxyPort string, client pb.ClientTransport) *MongoDBServer {
	listenAddr := defaultListenAddr(defaultMongoDBPort)
	if proxyPort != "" {
		listenAddr = defaultListenAddr(proxyPort)
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
	if err := copyMongoDBBuffer(stream, conn); err != nil && err != io.EOF {
		log.Warnf("failed copying buffer, err=%v", err)
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

func (s *MongoDBServer) Host() Host { return getListenAddr(s.listenAddr) }

func copyMongoDBBuffer(dst io.Writer, src io.Reader) (err error) {
	for {
		var header [16]byte
		_, err = io.ReadFull(src, header[:])
		if err != nil {
			return err
		}
		pktLen := int(binary.LittleEndian.Uint32(header[0:4])) - binary.Size(header)
		if pktLen > maxPacketSize {
			return fmt.Errorf("max packet size reached (max:%v, pkt:%v)", maxPacketSize, pktLen)
		}
		frame := make([]byte, pktLen)
		opCode := binary.LittleEndian.Uint32(header[12:16])
		log.Debugf("pktlen=%v, opcode=%v, header=% X", pktLen, opCode, header[:])
		copied := 0

		for i := 0; ; i++ {
			buf := make([]byte, defaultBufferSize)
			nr, er := src.Read(buf)
			if er != nil {
				return
			}

			copied += copy(frame[copied:], buf[0:nr])
			log.Debugf("pktlen=%v, opcode=%v, connread=%v, copied=%v",
				pktLen, opCode, nr, copied)
			if pktLen > nr {
				pktLen -= nr
				continue
			}
			encPkt := encodeMongoDbPacket(header, frame)
			_, err = dst.Write(encPkt)
			if err != nil {
				return
			}
			break
		}
	}
}

func encodeMongoDbPacket(header [16]byte, frame []byte) []byte {
	pktBytes := make([]byte, len(frame)+16)
	_ = copy(pktBytes[:16], header[:])
	_ = copy(pktBytes[16:], frame)
	return pktBytes
}
