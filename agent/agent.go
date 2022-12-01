package agent

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/runopsio/hoop/agent/dlp"
	term "github.com/runopsio/hoop/agent/terminal"
	"github.com/runopsio/hoop/common/memory"
	pb "github.com/runopsio/hoop/common/proto"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type (
	Agent struct {
		client    pb.ClientTransport
		connStore memory.Store
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

func parseConnectionEnvVars(envVars map[string]any, connType string) (*connEnv, error) {
	if envVars == nil {
		return nil, fmt.Errorf("empty env vars")
	}
	envVarS, err := term.NewEnvVarStore(envVars)
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

func New(client pb.ClientTransport) *Agent {
	return &Agent{
		client:    client,
		connStore: memory.New()}
}

func (a *Agent) Run(svrAddr, token string, firstConnTry bool) {
	a.client.StartKeepAlive()

	for {
		pkt, err := a.client.Recv()
		if err != nil {
			if err == io.EOF {
				break
			}
			if e, ok := status.FromError(err); ok {
				switch e.Code() {
				case codes.Unauthenticated:
					if firstConnTry {
						fmt.Println("\n** UNREGISTERED AGENT **")
						fmt.Printf("Please validate the Agent in the URL: %s\n", buildAgentRegisterURL(svrAddr, token))
					}
				default:
					log.Printf("disconnecting, code=%v, msg=%v", e.Code(), err.Error())
				}

			}

			return
		}

		switch pb.PacketType(pkt.Type) {
		// gateway->agent connection ok
		case pb.PacketAgentGatewayConnectOK:
			fmt.Println("connected...")

		// client->agent connect
		case pb.PacketClientAgentConnectType:
			a.processClientConnect(pkt)

		// client->agent exec
		case pb.PacketClientAgentExecType:
			a.doExec(pkt)

		// PG protocol
		case pb.PacketPGWriteServerType:
			a.processPGProtocol(pkt)
		case pb.PacketCloseTCPConnectionType:
			a.processTCPCloseConnection(pkt)

		// terminal
		case pb.PacketTerminalWriteAgentStdinType:
			a.doTerminalWriteAgentStdin(pkt)
		case pb.PacketTerminalResizeTTYType:
			a.doTerminalResizeTTY(pkt)
		case pb.PacketTerminalCloseType:
			a.doTerminalCloseTerm(pkt)

		// raw tcp
		case pb.PacketTCPWriteServerType:
			a.processTCPWriteServer(pkt)
		}
	}
}

func (a *Agent) buildConnectionParams(pkt *pb.Packet, packetErrType pb.PacketType) (*pb.AgentConnectionParams, *connEnv, error) {
	sessionID := pkt.Spec[pb.SpecGatewaySessionID]
	sessionIDKey := string(sessionID)

	connParams := a.decodeConnectionParams(sessionID, pkt, packetErrType)
	if connParams == nil {
		return nil, nil, fmt.Errorf("session %s failed to decode connection params", sessionIDKey)
	}
	log.Printf("session=%s - connection params decoded with success, dlp-info-types=%d",
		sessionIDKey, len(connParams.DLPInfoTypes))

	connType := string(pkt.Spec[pb.SpecConnectionType])
	connEnvVars, err := parseConnectionEnvVars(connParams.EnvVars, connType)
	if err != nil {
		return nil, nil, fmt.Errorf("session %s failed to parse env vars", sessionIDKey)
	}
	if connType == pb.ConnectionTypePostgres || connType == pb.ConnectionTypeTCP {
		if err := isPortActive(connEnvVars.host, connEnvVars.port); err != nil {
			msg := fmt.Sprintf("session=%s - failed connecting to host=%q, port=%q, err=%v",
				sessionIDKey, connEnvVars.host, connEnvVars.port, err)
			log.Println(msg)
			return nil, nil, fmt.Errorf("%s", msg)
		}
	}
	return connParams, connEnvVars, nil
}

func (a *Agent) decodeConnectionParams(sessionID []byte, pkt *pb.Packet, packetType pb.PacketType) *pb.AgentConnectionParams {
	var connParams pb.AgentConnectionParams
	encConnectionParams := pkt.Spec[pb.SpecAgentConnectionParamsKey]
	if err := pb.GobDecodeInto(encConnectionParams, &connParams); err != nil {
		log.Printf("session=%v - failed decoding connection params=%#v, err=%v",
			string(sessionID), string(encConnectionParams), err)
		_ = a.client.Send(&pb.Packet{
			Type:    packetType.String(),
			Payload: []byte(`internal error, failed decoding connection params`),
			Spec:    map[string][]byte{pb.SpecGatewaySessionID: sessionID},
		})
		return nil
	}
	return &connParams
}

func (a *Agent) decodeDLPCredentials(sessionID []byte, pkt *pb.Packet, packetType pb.PacketType) dlp.Client {
	if gcpRawCred, ok := pkt.Spec[pb.SpecAgentGCPRawCredentialsKey]; ok {
		if _, ok := a.connStore.Get(dlpClientKey).(dlp.Client); !ok {
			dlpClient, err := dlp.NewDLPClient(context.Background(), gcpRawCred)
			if err != nil {
				_ = a.client.Send(&pb.Packet{
					Type:    packetType.String(),
					Payload: []byte(`failed creating dlp client`),
					Spec:    map[string][]byte{pb.SpecGatewaySessionID: sessionID},
				})
				log.Printf("failed creating dlp client, err=%v", err)
				return nil
			}
			log.Printf("session=%v - created dlp client with success", string(sessionID))
			return dlpClient
		}
	}
	log.Printf("session=%v - dlp is unavailable for this connection, missing gcp credentials", string(sessionID))
	return nil
}

func (a *Agent) processClientConnect(pkt *pb.Packet) {
	sessionID := pkt.Spec[pb.SpecGatewaySessionID]
	sessionIDKey := string(sessionID)
	packetErrType := pb.PacketClientAgentConnectErrType
	log.Printf("session=%s - received connect request", sessionIDKey)

	connParams, connEnvVars, err := a.buildConnectionParams(pkt, packetErrType)
	if err != nil {
		_ = a.client.Send(&pb.Packet{
			Type:    packetErrType.String(),
			Payload: []byte(err.Error()),
			Spec:    map[string][]byte{pb.SpecGatewaySessionID: sessionID},
		})
		return
	}

	connType := string(pkt.Spec[pb.SpecConnectionType])
	if connType == pb.ConnectionTypePostgres || connType == pb.ConnectionTypeTCP {
		connParams.EnvVars[connEnvKey] = connEnvVars
	}

	if connType == pb.ConnectionTypeCommandLine {
		sessionIDKey = fmt.Sprintf(connectionStoreParamsKey, string(sessionID))
	}

	if a.connStore.Get(dlpClientKey) == nil {
		dlpClient := a.decodeDLPCredentials(sessionID, pkt, packetErrType)
		if dlpClient != nil {
			a.connStore.Set(dlpClientKey, dlpClient)
		}
	}
	a.connStore.Set(sessionIDKey, connParams)

	a.client.Send(&pb.Packet{
		Type: pb.PacketClientAgentConnectOKType.String(),
		Spec: map[string][]byte{
			pb.SpecGatewaySessionID: sessionID,
			pb.SpecConnectionType:   pkt.Spec[pb.SpecConnectionType]}})
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

func (a *Agent) doExec(pkt *pb.Packet) {
	sessionID := pkt.Spec[pb.SpecGatewaySessionID]
	packetErrType := pb.PacketClientAgentExecErrType
	log.Printf("session=%v - received execution request", string(sessionID))

	connParams, _, err := a.buildConnectionParams(pkt, packetErrType)
	if err != nil {
		_ = a.client.Send(&pb.Packet{
			Type:    packetErrType.String(),
			Payload: []byte(err.Error()),
			Spec:    map[string][]byte{pb.SpecGatewaySessionID: sessionID},
		})
		return
	}

	cmd, err := term.NewCommand(connParams.EnvVars, append(connParams.CmdList, connParams.ClientArgs...)...)
	if err != nil {
		log.Printf("failed executing command, err=%v", err)
		_ = a.client.Send(&pb.Packet{
			Type:    packetErrType.String(),
			Payload: []byte(err.Error()),
			Spec:    map[string][]byte{pb.SpecGatewaySessionID: sessionID},
		})
		return
	}
	log.Printf("session=%v, tty=false - executing command=%q", string(sessionID), cmd.String())

	spec := map[string][]byte{pb.SpecGatewaySessionID: sessionID}
	stdoutWriter := pb.NewStreamWriter(a.client, pb.PacketClientAgentExecOKType, spec)

	onExecErr := func(exitCode int, errMsg string, v ...any) {
		errMsg = fmt.Sprintf(errMsg, v...)
		spec[pb.SpecClientExecExitCodeKey] = []byte(strconv.Itoa(exitCode))
		_, _ = pb.NewStreamWriter(a.client, packetErrType, spec).
			Write([]byte(errMsg))
	}

	// TODO: add client args
	if err = cmd.Run(stdoutWriter, pkt.Payload, onExecErr); err != nil {
		log.Printf("session=%v - err=%v", string(sessionID), err)
	}
}

func buildAgentRegisterURL(svrAddr, token string) string {
	addr := strings.Split(svrAddr, ":")
	return fmt.Sprintf("https://%s/agents/new/%s", addr[0], token)
}
