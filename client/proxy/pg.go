package proxy

import (
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"

	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/memory"
	pgtypes "github.com/runopsio/hoop/common/pgtypes"
	pb "github.com/runopsio/hoop/common/proto"
	pbagent "github.com/runopsio/hoop/common/proto/agent"
)

const (
	defaultPostgresPort      = "5433"
	maxSimpleQueryPacketSize = 1048576 // 1MB
)

type PGServer struct {
	listenAddr      string
	client          pb.ClientTransport
	connectionStore memory.Store
	listener        net.Listener
}

func NewPGServer(listenAddr string, client pb.ClientTransport) *PGServer {
	if listenAddr == "" {
		listenAddr = fmt.Sprintf("127.0.0.1:%s", defaultPostgresPort)
	}
	return &PGServer{
		listenAddr:      listenAddr,
		client:          client,
		connectionStore: memory.New(),
	}
}

func (p *PGServer) Serve(sessionID string) error {
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
	if written, err := copyPGBuffer(pgServerWriter, pgClient); err != nil {
		log.Warnf("failed copying buffer, written=%v, err=%v", written, err)
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
	parts := strings.Split(p.listenAddr, ":")
	if len(parts) == 2 {
		return parts[1]
	}
	return ""
}

func copyPGBuffer(dst io.Writer, src io.Reader) (written int64, err error) {
	for {
		pkt, err := pgtypes.Decode(src)
		if err != nil {
			if err == io.EOF {
				break
			}
			return 0, fmt.Errorf("fail to decode typed packet, err=%v", err)
		}
		// not implemented for now
		if pkt.IsCancelRequest() {
			log.Warnf("client has sent a cancel request, but it's not implemented")
			continue
		}
		writtenSize, err := dst.Write(pkt.Encode())
		if err != nil {
			return 0, fmt.Errorf("fail to write typed packet, err=%v", err)
		}
		log.Debugf("%s, copied %v byte(s)", pkt.Type(), writtenSize)
		written += int64(writtenSize)
	}
	return written, err
}
