package agent

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"time"

	pb "github.com/runopsio/hoop/proto"
	"github.com/runopsio/hoop/proto/memory"
	"github.com/runopsio/hoop/proto/pg"
	"github.com/runopsio/hoop/proto/pg/middlewares"
	pgtypes "github.com/runopsio/hoop/proto/pg/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	EnvVarPGHostKey string = "PG_HOST"
	EnvVarPGUserKey string = "PG_USER"
	EnvVarPGPassKey string = "PG_PASS"
	EnvVarPGPortKey string = "PG_PORT"
)

type (
	Agent struct {
		ctx         context.Context
		stream      pb.Transport_ConnectClient
		closeSignal chan struct{}
		connStore   memory.Store
	}
	pgEnv struct {
		host string
		user string
		pass string
		port string
	}
)

func isPortActive(host, port string) error {
	timeout := time.Second * 5
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), timeout)
	if err != nil {
		return err
	}
	if conn != nil {
		defer conn.Close()
	}
	return nil
}

func newTCPConn(host, port string) (net.Conn, error) {
	serverConn, err := net.Dial("tcp4", fmt.Sprintf("%s:%s", host, port))
	if err != nil {
		return nil, fmt.Errorf("failed dialing server: %s", err)
	}

	log.Printf("tcp connection stablished with postgres server. address=%v, local-addr=%v\n",
		serverConn.LocalAddr(),
		serverConn.RemoteAddr())
	return serverConn, nil
}

func parseEnvVars(envVars map[string]any) (*pgEnv, error) {
	if envVars == nil {
		return nil, fmt.Errorf("empty env vars")
	}
	env := &pgEnv{}
	for key, val := range envVars {
		// key = secret/REALKEY
		parts := strings.Split(key, "/")
		if len(parts) != 2 {
			continue
		}
		switch parts[1] {
		case EnvVarPGHostKey:
			env.host, _ = val.(string)
		case EnvVarPGPortKey:
			env.port, _ = val.(string)
			if env.port == "" {
				env.port = "5432"
			}
		case EnvVarPGUserKey:
			env.user, _ = val.(string)
		case EnvVarPGPassKey:
			env.pass, _ = val.(string)
		}
	}
	if env.host == "" || env.pass == "" || env.user == "" {
		return nil, fmt.Errorf("missing required secrets for postgres connection (%v, %v, %v)",
			EnvVarPGHostKey, EnvVarPGUserKey, EnvVarPGPassKey)
	}
	return env, nil
}

func New(ctx context.Context, s pb.Transport_ConnectClient, closeSig chan struct{}) *Agent {
	return &Agent{
		ctx:         ctx,
		stream:      s,
		closeSignal: closeSig,
		connStore:   memory.New()}
}

func (a *Agent) Context() context.Context {
	return a.ctx
}

func (a *Agent) Close() {
	close(a.closeSignal)
}

func (p *Agent) GatewayLocalConnectionID() string {
	obj := p.connStore.Get(pb.SpecGatewayConnectionID)
	gwID, _ := obj.(string)
	if gwID == "" {
		// TODO: log/warn
		log.Printf("gateway id is empty")
	}
	return gwID
}

func (p *Agent) SetGatewayLocalConnectionID(gwID string) {
	p.connStore.Set(pb.SpecGatewayConnectionID, gwID)
}

func (p *Agent) GetConnection(pkt *pb.Packet) io.WriteCloser {
	if pkt.Spec == nil {
		return nil
	}
	clientConnectionID := pkt.Spec[pb.SpecClientConnectionID]
	if clientConnectionID == nil {
		// TODO: warn empty
		return nil
	}
	obj := p.connStore.Get(string(clientConnectionID))
	c, _ := obj.(io.WriteCloser)
	return c
}

func (a *Agent) Run() {
	go a.startKeepAlive()

	for {
		pkt, err := a.stream.Recv()
		if err != nil {
			if err == io.EOF {
				break
			}
			if err != nil {
				msg := err.Error()
				code := codes.Code(0)
				s, ok := status.FromError(err)
				if ok {
					msg = s.Message()
					code = s.Code()
					if s.Code() == codes.Unavailable {
						log.Println("disconnecting, server unavailable")
						time.Sleep(time.Second * 5)
						break
					}
				}
				log.Printf("disconnecting, code=%v, msg=%v", code, msg)
				time.Sleep(time.Second * 20)
				break
			}
			log.Println(err.Error())
			close(a.closeSignal)
			return
		}
		a.processAgentConnect(pkt)
		a.processPGProtocol(pkt)
		a.processCloseConnection(pkt)
	}
}

func (a *Agent) processAgentConnect(pkt *pb.Packet) {
	switch pb.PacketType(pkt.Type) {
	case pb.PacketAgentConnectType:
		gwID := pkt.Spec[pb.SpecGatewayConnectionID]
		log.Printf("gatewayid=%v - received [%s]", string(gwID), pkt.Type)
		envVars, err := pb.GobDecodeMap(pkt.Spec[pb.SpecAgentEnvVars])
		if err != nil {
			log.Printf("failed decoding env vars, err=%v", err)
			return
		}
		// log.Printf("decoded env-vars %#v", envVars)
		pgEnv, err := parseEnvVars(envVars)
		if err != nil {
			_ = a.stream.Send(&pb.Packet{
				Type:    pb.PacketGatewayConnectErrType.String(),
				Payload: []byte(err.Error()),
				Spec:    map[string][]byte{pb.SpecGatewayConnectionID: gwID},
			})
			return
		}
		a.connStore.Set(string(gwID), pgEnv)
		if err := isPortActive(pgEnv.host, pgEnv.port); err != nil {
			_ = a.stream.Send(&pb.Packet{
				Type:    pb.PacketGatewayConnectErrType.String(),
				Payload: []byte(err.Error()),
				Spec:    map[string][]byte{pb.SpecGatewayConnectionID: gwID},
			})
			log.Printf("failed connecting to postgres host=%q, port=%q, err=%v", pgEnv.host, pgEnv.port, err)
			return
		}
		_ = a.stream.Send(&pb.Packet{
			Type: pb.PacketGatewayConnectOKType.String(),
			Spec: map[string][]byte{pb.SpecGatewayConnectionID: gwID}})
	}
}

func (a *Agent) processCloseConnection(pkt *pb.Packet) {
	if pb.PacketType(pkt.Type) != pb.PacketCloseConnectionType {
		return
	}
	gwID := pkt.Spec[pb.SpecGatewayConnectionID]
	clientConnID := pkt.Spec[pb.SpecClientConnectionID]
	filterKey := fmt.Sprintf("%s:%s", string(gwID), string(clientConnID))
	log.Printf("received %s, filter-by=%s", pb.PacketCloseConnectionType, filterKey)
	filterFn := func(k string) bool { return strings.HasPrefix(k, filterKey) }
	for key, obj := range a.connStore.Filter(filterFn) {
		if client, _ := obj.(io.Closer); client != nil {
			defer func() {
				if err := client.Close(); err != nil {
					log.Printf("failed closing connection, err=%v", err)
				}
			}()
			a.connStore.Del(key)
		}
	}
}

func (a *Agent) processPGProtocol(pkt *pb.Packet) {
	if pb.PacketType(pkt.Type) != pb.PacketPGWriteServerType {
		return
	}
	gwID := pkt.Spec[pb.SpecGatewayConnectionID]
	swPgClient := pb.NewStreamWriter(a.stream.Send, pb.PacketPGWriteClientType, pkt.Spec)
	envObj := a.connStore.Get(string(gwID))
	pgEnv, _ := envObj.(*pgEnv)
	if pgEnv == nil {
		log.Println("postgres credentials not found in memory")
		writePGClientErr(swPgClient,
			pg.NewFatalError("credentials is empty, contact the administrator").Encode())
		return
	}
	clientConnectionID := pkt.Spec[pb.SpecClientConnectionID]
	clientObj := a.connStore.Get(string(clientConnectionID))
	if proxyServerWriter, ok := clientObj.(pg.Proxy); ok {
		if err := proxyServerWriter.Send(pkt.Payload); err != nil {
			log.Println(err)
			proxyServerWriter.Cancel()
		}
		return
	}
	// startup phase
	_, pgPkt, err := pg.DecodeStartupPacket(pb.BufferedPayload(pkt.Payload))
	if err != nil {
		log.Printf("failed decoding startup packet: %v", err)
		writePGClientErr(swPgClient,
			pg.NewFatalError("failed decoding startup packet (1), contact the administrator").Encode())
		return
	}

	if pgPkt.IsFrontendSSLRequest() {
		err := a.stream.Send(&pb.Packet{
			Type:    pb.PacketPGWriteClientType.String(),
			Spec:    pkt.Spec,
			Payload: []byte{pgtypes.ServerSSLNotSupported.Byte()},
		})
		if err != nil {
			log.Printf("failed sending ssl response back, err=%v", err)
		}
		return
	}

	startupPkt, err := pg.DecodeStartupPacketWithUsername(pb.BufferedPayload(pkt.Payload), pgEnv.user)
	if err != nil {
		log.Printf("failed decoding startup packet with username, err=%v", err)
		writePGClientErr(swPgClient,
			pg.NewFatalError("failed decoding startup packet (2), contact the administrator").Encode())
		return
	}

	log.Printf("starting postgres connection for %s", gwID)
	pgServer, err := newTCPConn(pgEnv.host, pgEnv.port)
	if err != nil {
		log.Printf("failed obtaining connection with postgres server, err=%v", err)
		writePGClientErr(swPgClient,
			pg.NewFatalError("failed connecting with postgres server, contact the administrator").Encode())
		return
	}
	if _, err := pgServer.Write(startupPkt.Encode()); err != nil {
		log.Printf("failed writing startup packet, err=%v", err)
		writePGClientErr(swPgClient,
			pg.NewFatalError("failed writing startup packet, contact the administrator").Encode())
	}
	log.Println("finish startup phase")
	mid := middlewares.New(swPgClient, pgServer, pgEnv.user, pgEnv.pass)
	pg.NewProxy(context.Background(), swPgClient, mid.ProxyCustomAuth).
		RunWithReader(pg.NewReader(pgServer))
	proxyServerWriter := pg.NewProxy(context.Background(), pgServer, mid.DenyChangePassword).Run()
	a.connStore.Set(string(clientConnectionID), proxyServerWriter)
}

func writePGClientErr(w io.Writer, msg []byte) {
	if _, err := w.Write(msg); err != nil {
		log.Printf("failed writing error back to client, err=%v", err)
	}
}

func (a *Agent) startKeepAlive() {
	for {
		time.Sleep(pb.DefaultKeepAlive)
		proto := &pb.Packet{
			Type: pb.PacketKeepAliveType.String(),
		}
		// log.Println("sending keep alive command")
		if err := a.stream.Send(proto); err != nil {
			if err == io.EOF {
				break
			}
			if err != nil {
				log.Printf("failed sending keep alive command, err=%v", err)
				break
			}
		}
	}
}
