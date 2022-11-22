package agent

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"time"

	"github.com/runopsio/hoop/agent/dlp"
	"github.com/runopsio/hoop/agent/terminal"
	"github.com/runopsio/hoop/common/memory"
	pb "github.com/runopsio/hoop/common/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type (
	Agent struct {
		client      pb.ClientTransport
		closeSignal chan struct{}
		connStore   memory.Store
	}
	connEnv struct {
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

	log.Printf("tcp connection stablished with server. address=%v, local-addr=%v\n",
		serverConn.LocalAddr(),
		serverConn.RemoteAddr())
	return serverConn, nil
}

func parseEnvVars(envVars map[string]any, connType string) (*connEnv, error) {
	if envVars == nil {
		return nil, fmt.Errorf("empty env vars")
	}
	envVarS, err := terminal.NewEnvVarStore(envVars)
	if err != nil {
		return nil, err
	}
	env := &connEnv{
		host: envVarS.Getenv("HOST"),
		user: envVarS.Getenv("USER"),
		pass: envVarS.Getenv("PASS"),
		port: envVarS.Getenv("PORT"),
	}
	switch connType {
	case pb.ConnectionTypePostgres:
		if env.port == "" {
			env.port = "5432"
		}
		if env.host == "" || env.pass == "" || env.user == "" {
			return nil, fmt.Errorf("missing required secrets for postgres connection [HOST, USER, PASS]")
		}
	case pb.ConnectionTypeTCP:
		if env.host == "" || env.port == "" {
			return nil, fmt.Errorf("missing required environment for connection [HOST, PORT]")
		}
	}
	return env, nil
}

func New(client pb.ClientTransport, closeSig chan struct{}) *Agent {
	return &Agent{
		client:      client,
		closeSignal: closeSig,
		connStore:   memory.New()}
}

func (a *Agent) Close() {
	close(a.closeSignal)
}

func (a *Agent) Run(svrAddr, token string) {
	a.client.StartKeepAlive()

	for {
		pkt, err := a.client.Recv()
		if err != nil {
			if e, ok := status.FromError(err); ok {
				switch e.Code() {
				case codes.Unauthenticated:
					fmt.Println("** UNREGISTERED AGENT **")
					fmt.Println("Please validate the Agent in the URL: " + buildAgentRegisterURL(svrAddr, token))
				default:
				}
			}
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

		switch pb.PacketType(pkt.Type) {
		// client connect
		case pb.PacketClientAgentConnectType:
			a.processClientConnect(pkt)

		// PG protocol
		case pb.PacketPGWriteServerType:
			a.processPGProtocol(pkt)
		case pb.PacketCloseTCPConnectionType:
			a.processTCPCloseConnection(pkt)

		// terminal
		case pb.PacketTerminalRunProcType:
			a.doTerminalRunProc(pkt)
		case pb.PacketTerminalWriteAgentStdinType:
			a.doTerminalWriteAgentStdin(pkt)
		case pb.PacketTerminalCloseType:
			a.doTerminalCloseTerm(pkt)

		// raw tcp
		case pb.PacketTCPWriteServerType:
			a.processTCPWriteServer(pkt)
		}
	}
}

func (a *Agent) decodeConnectionParams(sessionID []byte, pkt *pb.Packet) *pb.AgentConnectionParams {
	var connParams pb.AgentConnectionParams
	encConnectionParams := pkt.Spec[pb.SpecAgentConnectionParamsKey]
	if err := pb.GobDecodeInto(encConnectionParams, &connParams); err != nil {
		log.Printf("session=%v - failed decoding connection params=%#v, err=%v",
			string(sessionID), string(encConnectionParams), err)
		_ = a.client.Send(&pb.Packet{
			Type:    pb.PacketClientAgentConnectErrType.String(),
			Payload: []byte(`internal error, failed decoding connection params`),
			Spec:    map[string][]byte{pb.SpecGatewaySessionID: sessionID},
		})
	}
	return &connParams
}

func (a *Agent) processClientConnect(pkt *pb.Packet) {
	sessionID := pkt.Spec[pb.SpecGatewaySessionID]
	log.Printf("session=%v - received connect request", string(sessionID))

	if gcpRawCred, ok := pkt.Spec[pb.SpecAgentGCPRawCredentialsKey]; ok {
		if _, ok := a.connStore.Get(dlpClientKey).(dlp.Client); !ok {
			dlpClient, err := dlp.NewDLPClient(context.Background(), gcpRawCred)
			if err != nil {
				_ = a.client.Send(&pb.Packet{
					Type:    pb.PacketClientAgentConnectErrType.String(),
					Payload: []byte(`failed creating dlp client`),
					Spec:    map[string][]byte{pb.SpecGatewaySessionID: sessionID},
				})
				log.Printf("failed creating dlp client, err=%v", err)
				return
			}
			log.Printf("session=%v - created dlp client with success", string(sessionID))
			a.connStore.Set(dlpClientKey, dlpClient)
		}
	}

	sessionIDKey := string(sessionID)
	switch connType := string(pkt.Spec[pb.SpecConnectionType]); {
	case connType == pb.ConnectionTypePostgres || connType == pb.ConnectionTypeTCP:
		connParams := a.decodeConnectionParams(sessionID, pkt)
		if connParams == nil {
			return
		}
		log.Printf("session=%v - connection params decoded with success, dlp-info-types=%v",
			sessionIDKey, connParams.DLPInfoTypes)
		// envVarS, err :=
		connenv, err := parseEnvVars(connParams.EnvVars, connType)
		if err != nil {
			_ = a.client.Send(&pb.Packet{
				Type:    pb.PacketClientAgentConnectErrType.String(),
				Payload: []byte(err.Error()),
				Spec:    map[string][]byte{pb.SpecGatewaySessionID: sessionID},
			})
			return
		}
		if err := isPortActive(connenv.host, connenv.port); err != nil {
			_ = a.client.Send(&pb.Packet{
				Type:    pb.PacketClientAgentConnectErrType.String(),
				Payload: []byte(err.Error()),
				Spec:    map[string][]byte{pb.SpecGatewaySessionID: sessionID},
			})
			log.Printf("session=%v - failed connecting to host=%q, port=%q, err=%v",
				sessionIDKey, connenv.host, connenv.port, err)
			return
		}
		connParams.EnvVars[connEnvKey] = connenv
		a.connStore.Set(sessionIDKey, connParams)
	case connType == pb.ConnectionTypeCommandLine:
		sessionIDKey = fmt.Sprintf(connectionStoreParamsKey, string(sessionID))
		connParams := a.decodeConnectionParams(sessionID, pkt)
		if connParams == nil {
			return
		}
		log.Printf("connection params decoded with success, dlp-info-types=%v", connParams.DLPInfoTypes)
		a.connStore.Set(sessionIDKey, connParams)
	default:
		_ = a.client.Send(&pb.Packet{
			Type:    pb.PacketClientAgentConnectErrType.String(),
			Payload: []byte(fmt.Sprintf("unknown connection type %q", connType)),
			Spec:    map[string][]byte{pb.SpecGatewaySessionID: sessionID},
		})
		log.Printf("unknown connection type %q", connType)
		return
	}
	err := a.client.Send(&pb.Packet{
		Type: pb.PacketClientAgentConnectOKType.String(),
		Spec: map[string][]byte{
			pb.SpecGatewaySessionID: sessionID,
			pb.SpecConnectionType:   pkt.Spec[pb.SpecConnectionType]}})
	if err != nil {
		a.connStore.Del(string(sessionIDKey))
		log.Printf("failed sending %v, err=%v", pb.PacketClientAgentConnectOKType, err)
	}
	log.Printf("session=%v - sent gateway connect ok", string(sessionID))
}

func (a *Agent) processTCPCloseConnection(pkt *pb.Packet) {
	sessionID := pkt.Spec[pb.SpecGatewaySessionID]
	clientConnID := pkt.Spec[pb.SpecClientConnectionID]
	filterKey := fmt.Sprintf("%s:%s", string(sessionID), string(clientConnID))
	log.Printf("received %s, filter-by=%s", pb.PacketCloseTCPConnectionType, filterKey)
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

func buildAgentRegisterURL(svrAddr, token string) string {
	addr := strings.Split(svrAddr, ":")
	return fmt.Sprintf("https://%s/agents/new/%s", addr[0], token)
}
