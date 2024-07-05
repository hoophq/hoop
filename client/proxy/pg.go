package proxy

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/memory"
	pgtypes "github.com/hoophq/hoop/common/pgtypes"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
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

type pgConnection struct {
	id             string
	client         net.Conn
	backendKeyData *pgtypes.BackendKeyData
}

func NewPGServer(proxyPort string, client pb.ClientTransport) *PGServer {
	listenAddr := fmt.Sprintf("127.0.0.1:%s", defaultPostgresPort)
	if proxyPort != "" {
		listenAddr = fmt.Sprintf("127.0.0.1:%s", proxyPort)
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
	clientConn := &pgConnection{id: connectionID, client: pgClient}
	p.connectionStore.Set(connectionID, clientConn)
	log.Infof("session=%v | conn=%s | client=%s - connected", sessionID, connectionID, pgClient.RemoteAddr())
	pgServerWriter := pb.NewStreamWriter(p.client, pbagent.PGConnectionWrite, map[string][]byte{
		string(pb.SpecClientConnectionID): []byte(connectionID),
		string(pb.SpecGatewaySessionID):   []byte(sessionID),
	})
	if written, err := p.copyPGBuffer(pgServerWriter, clientConn); err != nil {
		log.Warnf("failed copying buffer, written=%v, err=%v", written, err)
	}
}

func (p *PGServer) PacketWriteClient(connectionID string, pkt *pb.Packet) (int, error) {
	conn, err := p.getConnection(connectionID)
	if err != nil {
		return 0, err
	}
	// store the pid for each connection
	if len(pkt.Payload) >= 13 {
		pktType := pkt.Payload[0]
		if string(pktType) == string(pgtypes.ServerBackendKeyData) {
			data := make([]byte, len(pkt.Payload))
			// skip header
			_ = copy(data, pkt.Payload[5:])
			pgPid := binary.BigEndian.Uint32(data[0:4])
			log.Infof("session=%v | conn=%v | pid=%v - connection process started in the backend",
				string(pkt.Spec[pb.SpecGatewaySessionID]), connectionID, pgPid)
			conn.backendKeyData = &pgtypes.BackendKeyData{
				Pid:       pgPid,
				SecretKey: binary.BigEndian.Uint32(data[4:8]),
			}
			p.connectionStore.Set(conn.id, conn)
		}
	}
	n, err := conn.client.Write(pkt.Payload)
	if err != nil {
		log.Warnf("session=%v | conn=%v - failed writing packet, err=%v",
			string(pkt.Spec[pb.SpecGatewaySessionID]), connectionID, err)
	}
	return n, err
}

func (p *PGServer) CloseTCPConnection(connectionID string) {
	if conn, err := p.getConnection(connectionID); err == nil {
		_ = conn.client.Close()
	}
}

func (p *PGServer) Close() error { return p.listener.Close() }

func (p *PGServer) getConnection(connectionID string) (*pgConnection, error) {
	connectionObj := p.connectionStore.Get(connectionID)
	conn, ok := connectionObj.(*pgConnection)
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

func (p *PGServer) lookupConnectionByPid(pid uint32) *pgConnection {
	for _, obj := range p.connectionStore.List() {
		conn, _ := obj.(*pgConnection)
		if conn == nil || conn.backendKeyData == nil {
			continue
		}
		if conn.backendKeyData.Pid == pid {
			return conn
		}
	}
	return nil
}

func (p *PGServer) copyPGBuffer(dst io.Writer, src *pgConnection) (written int64, err error) {
	closedConn := false
	for {
		pkt, err := pgtypes.Decode(src.client)
		if err != nil {
			if err == io.EOF || closedConn {
				break
			}
			return 0, fmt.Errorf("fail to decode typed packet, err=%v", err)
		}
		// A cancel request is sent by a second connection
		// the response must be received by the pid's connection.
		// See: https://www.postgresql.org/docs/current/protocol-flow.html#PROTOCOL-FLOW-CANCELING-REQUESTS
		if pkt.IsCancelRequest() {
			frame := pkt.Frame()
			pid := binary.BigEndian.Uint32(frame[4:8])
			pidsConn := p.lookupConnectionByPid(pid)
			if pidsConn != nil {
				// swap the cancel connection with the pid's connection
				p.connectionStore.Set(src.id, pidsConn)
				// this isn't ideal, give some time to the cancel message to propagate
				// and close the cancel connection
				go func() {
					time.Sleep(time.Second * 4)
					closedConn = true
					_ = src.client.Close()
				}()
			}
			log.Infof("conn=%s | pid=%v | swapped=%v - cancel request received by the client", src.id, pid, pidsConn != nil)
		}
		writtenSize, err := dst.Write(pkt.Encode())
		if err != nil {
			return 0, fmt.Errorf("fail to write typed packet, err=%v", err)
		}
		log.Debugf("%s, copied %v byte(s) from connection %v", pkt.Type(), writtenSize, src.id)
		written += int64(writtenSize)
	}
	return written, err
}
