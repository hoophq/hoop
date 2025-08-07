package proxyproto

import (
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/grpc"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/memory"
	pgtypes "github.com/hoophq/hoop/common/pgtypes"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	pbclient "github.com/hoophq/hoop/common/proto/client"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/clientexec"
	"github.com/hoophq/hoop/gateway/models"
)

type PGServer struct {
	listenAddr      string
	connectionStore memory.Store
	listener        net.Listener
}

type pgConnection struct {
	id     string
	client net.Conn
}

func NewPGServer() *PGServer {
	listenAddr := "0.0.0.0:15432"
	return &PGServer{
		listenAddr:      listenAddr,
		connectionStore: memory.New(),
	}
}

func (p *PGServer) Serve() error {
	lis, err := net.Listen("tcp4", p.listenAddr)
	if err != nil {
		return fmt.Errorf("failed listening to address %v, err=%v", p.listenAddr, err)
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
			go p.serveConn(strconv.Itoa(connectionID), pgClient)
		}
	}()
	return nil
}

func (p *PGServer) serveConn(connectionID string, pgClient net.Conn) {
	clientConn := &pgConnection{id: connectionID, client: pgClient}
	if written, err := p.copyPGBuffer(clientConn); err != nil {
		log.Warnf("failed copying buffer, written=%v, err=%v", written, err)
	}
}

func (p *PGServer) initializeConnection(src *pgConnection) (pb.ClientTransport, error) {
	pgpkt, err := pgtypes.Decode(src.client)
	if err != nil {
		return nil, fmt.Errorf("failed decoding startup packet, err=%v", err)
	}
	pgpkt.Dump()
	if pgpkt.IsFrontendSSLRequest() {
		// TODO: handle SSL request
		if _, err = src.client.Write([]byte{pgtypes.ServerSSLNotSupported.Byte()}); err != nil {
			return nil, fmt.Errorf("failed writing ssl not supported response, err=%v", err)
		}
		return nil, nil
	}
	if pgpkt.IsCancelRequest() {
		// TODO: handle cancel request
		log.Fatalf("cancel request received, this should not happen in the server side")
	}
	startupPkt := pgpkt.Frame()

	var parameters []string
	var param string
	for _, p := range startupPkt[4 : len(startupPkt)-1] {
		if p == 0x00 {
			parameters = append(parameters, param)
			param = ""
			continue
		}
		param += string(p)
	}

	var dbAccessKeyID string
	for i, p := range parameters {
		if p == "user" {
			dbAccessKeyID = parameters[i+1]
		}
	}

	if dbAccessKeyID == "" {
		log.Errorf("failed obtaining secret key from startup parameters")
		return nil, fmt.Errorf("failed obtaining secret key from startup parameters")
	}

	dba, err := models.GetValidDbAccessBySecretKey("", dbAccessKeyID)
	switch err {
	case models.ErrNotFound:
		log.Errorf("db access key not found or not active, id=%v", dbAccessKeyID)
		return nil, fmt.Errorf("db access key not found or not active")
	case nil:
		log.Infof("obtained db access by id, id=%v, user=%v, host=%v, port=%v, expires-at=%v",
			dbAccessKeyID, dba.DbUsername, dba.DbHostname, dba.DbPort, dba.ExpireAt.Format(time.RFC3339))
		if dba.ExpireAt.Before(time.Now().UTC()) {
			return nil, fmt.Errorf("db access key is expired")
		}
	default:
		log.Errorf("failed obtaining db access by id, id=%v, err=%v", dbAccessKeyID, err)
		return nil, fmt.Errorf("failed obtaining db access by id")
	}

	sessionID := uuid.NewString()
	tlsCA := appconfig.Get().GatewayTLSCa()
	client, err := grpc.Connect(grpc.ClientConfig{
		ServerAddress: grpc.LocalhostAddr,
		Token:         "", // it will use impersonate-auth-key as authentication
		UserAgent:     "postgres/grpc",
		Insecure:      tlsCA == "",
		TLSCA:         tlsCA,
	},
		grpc.WithOption(grpc.OptionConnectionName, dba.ConnectionName),
		grpc.WithOption("impersonate-user-id", dba.UserID),
		grpc.WithOption("impersonate-auth-key", clientexec.ImpersonateSecretKey),
		grpc.WithOption("origin", pb.ConnectionOriginClient),
		grpc.WithOption("verb", pb.ClientVerbConnect),
		grpc.WithOption("session-id", sessionID),
	)
	if err != nil {
		return nil, err
	}

	openSessionPacket := &pb.Packet{
		Type: pbagent.SessionOpen,
		Spec: map[string][]byte{
			pb.SpecGatewaySessionID:   []byte(sessionID),
			pb.SpecClientConnectionID: []byte(src.id),
		},
	}
	if err := client.Send(openSessionPacket); err != nil {
		return nil, fmt.Errorf("failed sending open session packet, err=%v", err)
	}
	go func() {
		for {
			pkt, err := client.Recv()
			if err != nil {
				log.Errorf("failed receiving packet, err=%v", err)
				return
			}
			if pkt == nil {
				log.Warnf("received nil packet, closing connection")
				return
			}

			switch pb.PacketType(pkt.Type) {
			case pbclient.SessionOpenOK:
				log.Infof("session=%v - session opened successfully", sessionID)
				sessionID, ok := pkt.Spec[pb.SpecGatewaySessionID]
				if !ok || sessionID == nil {
					log.Errorf("session id not found in packet spec, pkt=%v", pkt)
					return
				}
				connectionType := pb.ConnectionType(pkt.Spec[pb.SpecConnectionType])
				if connectionType != pb.ConnectionTypePostgres {
					log.Errorf("unsupported connection type, pkt=%v", pkt)
					return
				}
				_ = client.Send(&pb.Packet{
					Type:    pbagent.PGConnectionWrite,
					Payload: pgpkt.Encode(),
					Spec: map[string][]byte{
						pb.SpecGatewaySessionID:   sessionID,
						pb.SpecClientConnectionID: []byte(src.id),
					},
				})
			case pbclient.PGConnectionWrite:
				log.Infof("session=%v | conn=%s - writing packet to client connection", sessionID, src.id)
				fmt.Println(hex.Dump(pkt.Payload))
				if _, err := src.client.Write(pkt.Payload); err != nil {
					log.Errorf("failed writing packet, err=%v", err)
					return
				}
			case pbclient.SessionClose:
				log.Infof("session=%v - closing session", string(pkt.Spec[pb.SpecGatewaySessionID]))
				return
			default:
				log.Warnf("session=%v | conn=%s - packet type %v not implemented", sessionID, src.id, pkt.Type)
				return
			}
		}
	}()
	fmt.Printf("startup parameters: %#v, username=%v\n", parameters, dbAccessKeyID)
	return client, nil
}

// 1. client obtain connection string information from hoop server gateway
// 2. client establishes tcp connection with the proxy server (gateway)
// 3. parse the startup packet and obtain the username (sid)
// 4. obtain sid connection info from store
// 5. route the connection to establish a connection with the right agent / postgres server
func (p *PGServer) copyPGBuffer(src *pgConnection) (written int64, err error) {
	log.Infof("initializing postgres connection, id=%v", src.id)
	client, err := p.initializeConnection(src)
	if err != nil {
		_, _ = src.client.Write(pgtypes.NewFatalError(err.Error()).Encode())
		return 0, fmt.Errorf("failed initializing connection, err=%v", err)
	}
	for {
		pkt, err := pgtypes.Decode(src.client)
		if err != nil {
			if err == io.EOF {
				break
			}
			return 0, fmt.Errorf("fail to decode typed packet, err=%v", err)
		}
		err = client.Send(&pb.Packet{
			Type:    pbagent.PGConnectionWrite,
			Payload: pkt.Encode(),
			Spec: map[string][]byte{
				pb.SpecGatewaySessionID:   []byte(src.id),
				pb.SpecClientConnectionID: []byte(src.id),
			},
		})
		if err != nil {
			return 0, fmt.Errorf("fail to write typed packet, err=%v", err)
		}
		log.Debugf("%s, copied %v byte(s) from connection", pkt.Type(), src.id)
		written += int64(len(pkt.Encode()))
	}
	return written, err
}
