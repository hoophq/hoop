package proxy

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"

	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/memory"
	"github.com/runopsio/hoop/common/pg"
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
	if _, err := copyPgBuffer(pgServerWriter, pgClient); err != nil {
		log.Warnf("failed copying buffer, err=%v", err)
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

// copyBuffer is an adaptation of the actual implementation of Copy and CopyBuffer.
// it parses simple query packets fully.
func copyPgBuffer(dst io.Writer, src io.Reader) (written int64, err error) {
	buf := make([]byte, 32*1024)
	var fullBuffer []byte
	for {
		nr, er := src.Read(buf)
		if nr > 0 {
			pktType := pg.PacketType(buf[0])
			switch pktType {
			case pg.ClientSimpleQuery:
				pktLen := int(binary.BigEndian.Uint32(buf[1:5]) - 4)
				frameSize := len(buf[5:nr])
				log.With("type", "simple").Infof("action=begin, read %v with header size of %v", frameSize, pktLen)
				if pktLen > frameSize {
					fullBuffer = append(fullBuffer, buf[0:nr]...)
					continue
				}
				if pktLen != frameSize {
					log.With("type", "simple").Warnf("action=begin, unknown packet format (frame/header) %v/%v",
						frameSize, pktLen)
					fmt.Println(hex.Dump(buf[0:nr]))
				}
			case pg.ClientParse:
				return 0, fmt.Errorf("extended query protocol is not supported")
			}

			if len(fullBuffer) > 0 {
				fullBuffer = append(fullBuffer, buf[0:nr]...)
				pktLen := int(binary.BigEndian.Uint32(fullBuffer[1:5]) - 4)
				frameSize := len(fullBuffer[5:])
				log.With("type", "simple").Infof("action=append, read %v with header size of %v, total = %v",
					len(buf[0:nr]), pktLen, len(fullBuffer))
				switch {
				case frameSize < pktLen:
					continue
				case frameSize > pktLen:
					return 0, fmt.Errorf("failed processing simple query packet, inconsistent sizes")
				case len(fullBuffer) > maxSimpleQueryPacketSize:
					return 0, fmt.Errorf("the query is too big (> 1MB)")
				}
				_, derr := dst.Write(fullBuffer)
				if derr != nil {
					return 0, derr
				}
				log.With("type", "simple").Infof("action=write, wrote %v with headersize of %v", len(fullBuffer), pktLen)
				fullBuffer = []byte{} // reset
				continue
			}
			nw, ew := dst.Write(buf[0:nr])
			if nw < 0 || nr < nw {
				nw = 0
				if ew == nil {
					ew = errors.New("invalid write result")
				}
			}
			written += int64(nw)
			if ew != nil {
				err = ew
				break
			}
			if nr != nw {
				err = io.ErrShortWrite
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
