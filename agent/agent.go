package agent

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/runopsio/hoop/agent/config"
	"github.com/runopsio/hoop/agent/dlp"
	"github.com/runopsio/hoop/agent/hook"
	term "github.com/runopsio/hoop/agent/terminal"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/memory"
	pb "github.com/runopsio/hoop/common/proto"
	pbagent "github.com/runopsio/hoop/common/proto/agent"
	pbclient "github.com/runopsio/hoop/common/proto/client"
)

type (
	Agent struct {
		client    pb.ClientTransport
		connStore memory.Store
		config    *config.Config
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

	log.Infof("tcp connection stablished with server. address=%v, local-addr=%v",
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
	case pb.ConnectionTypeMySQL:
		if env.port == "" {
			env.port = "3307"
		}
		if env.host == "" || env.pass == "" || env.user == "" {
			return nil, fmt.Errorf("missing required secrets for mysql connection [HOST, USER, PASS]")
		}
	case pb.ConnectionTypeTCP:
		if env.host == "" || env.port == "" {
			return nil, fmt.Errorf("missing required environment for connection [HOST, PORT]")
		}
	}
	return env, nil
}

func New(client pb.ClientTransport, cfg *config.Config) *Agent {
	return &Agent{
		client:    client,
		connStore: memory.New(),
		config:    cfg,
	}
}

func (a *Agent) handleGracefulExit() {
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc,
		syscall.SIGHUP,
		syscall.SIGINT,
		syscall.SIGTERM,
		syscall.SIGQUIT)
	go func() {
		sigval := <-sigc
		for _, obj := range a.connStore.List() {
			if client, _ := obj.(io.Closer); client != nil {
				_ = client.Close()
			}
		}
		_ = sentry.Flush(time.Second * 2)

		switch sigval {
		case syscall.SIGHUP:
			os.Exit(int(syscall.SIGHUP))
		case syscall.SIGINT:
			os.Exit(int(syscall.SIGINT))
		case syscall.SIGTERM:
			os.Exit(int(syscall.SIGTERM))
		case syscall.SIGQUIT:
			os.Exit(int(syscall.SIGQUIT))
		}
	}()
}

func (a *Agent) connectionParams(sessionID string) (*pb.AgentConnectionParams, *hook.ClientList) {
	storeKey := fmt.Sprintf(pluginHookSessionsKey, sessionID)
	if hooks, ok := a.connStore.Get(storeKey).(*hook.ClientList); ok {
		return hooks.ConnectionParams(), hooks
	}
	return nil, nil
}

func (a *Agent) Run() error {
	for {
		pkt, err := a.client.Recv()
		if err != nil {
			return err
		}
		log.With("session", string(pkt.Spec[pb.SpecGatewaySessionID])).
			Debugf("received client packet [%v]", pkt.Type)
		switch pkt.Type {
		case pbagent.GatewayConnectOK:
			log.Infof("connected with success to %v, tls=%v", a.config.GrpcURL, !a.config.IsInsecure())
			if err := a.config.Save(); err != nil {
				a.client.Close()
				return err
			}
			a.handleGracefulExit()
			a.client.StartKeepAlive()
			go a.startMonitoring(pkt)
		case pbagent.SessionOpen:
			a.processSessionOpen(pkt)

		case pbagent.ExecWriteStdin:
			a.doExec(pkt)

		// PG protocol
		case pbagent.PGConnectionWrite:
			a.processPGProtocol(pkt)

		// MySQL protocol
		case pbagent.MySQLConnectionWrite:
			a.processMySQLProtocol(pkt)

		// raw tcp
		case pbagent.TCPConnectionWrite:
			a.processTCPWriteServer(pkt)

		// terminal
		case pbagent.TerminalWriteStdin:
			a.doTerminalWriteAgentStdin(pkt)
		case pbagent.TerminalResizeTTY:
			a.doTerminalResizeTTY(pkt)

		case pbagent.SessionClose:
			a.processSessionClose(pkt)

		case pbagent.TCPConnectionClose:
			a.processTCPCloseConnection(pkt)
		}
	}
}

func (a *Agent) buildConnectionParams(pkt *pb.Packet) (*pb.AgentConnectionParams, *connEnv, error) {
	sessionID := pkt.Spec[pb.SpecGatewaySessionID]
	sessionIDKey := string(sessionID)

	connParams := a.decodeConnectionParams(sessionID, pkt)
	if connParams == nil {
		return nil, nil, fmt.Errorf("session %s failed to decode connection params", sessionIDKey)
	}

	log.Infof("session=%s - connection params decoded with success, dlp-info-types=%d",
		sessionIDKey, len(connParams.DLPInfoTypes))

	connType := string(pkt.Spec[pb.SpecConnectionType])
	connEnvVars, err := parseConnectionEnvVars(connParams.EnvVars, connType)
	if err != nil {
		log.Infof("session=%s - failed parse envvars from connection, err=%v", sessionIDKey, err)
		return nil, nil, fmt.Errorf("failed to parse connection envvars")
	}
	return connParams, connEnvVars, nil
}

func (a *Agent) checkTCPLiveness(pkt *pb.Packet, connEnvVars *connEnv) error {
	sessionID := fmt.Sprintf("%v", pkt.Spec[pb.SpecGatewaySessionID])
	connType := fmt.Sprintf("%v", pkt.Spec[pb.SpecConnectionType])
	if connType == pb.ConnectionTypePostgres || connType == pb.ConnectionTypeTCP || connType == pb.ConnectionTypeMySQL {
		if err := isPortActive(connEnvVars.host, connEnvVars.port); err != nil {
			msg := fmt.Sprintf("failed connecting to %s:%s, err=%v",
				connEnvVars.host, connEnvVars.port, err)
			log.Warnf("session=%v - %v", sessionID, msg)
			return fmt.Errorf("%s", msg)
		}
	}
	return nil
}

func (a *Agent) decodeConnectionParams(sessionID []byte, pkt *pb.Packet) *pb.AgentConnectionParams {
	var connParams pb.AgentConnectionParams
	encConnectionParams := pkt.Spec[pb.SpecAgentConnectionParamsKey]
	if err := pb.GobDecodeInto(encConnectionParams, &connParams); err != nil {
		log.Infof("session=%v - failed decoding connection params=%#v, err=%v",
			string(sessionID), string(encConnectionParams), err)
		sentry.CaptureException(err)
		_ = a.client.Send(&pb.Packet{
			Type:    pbclient.SessionClose,
			Payload: []byte(`internal error, failed decoding connection params`),
			Spec: map[string][]byte{
				pb.SpecClientExitCodeKey: []byte(`1`),
				pb.SpecGatewaySessionID:  sessionID,
			},
		})
		return nil
	}
	if clientEnvVarsEnc := pkt.Spec[pb.SpecClientExecEnvVar]; len(clientEnvVarsEnc) > 0 {
		var clientEnvVars map[string]string
		if err := pb.GobDecodeInto(clientEnvVarsEnc, &clientEnvVars); err != nil {
			log.Infof("session=%v - failed decoding client env vars, err=%v", string(sessionID), err)
			sentry.CaptureException(err)
			_ = a.client.Send(&pb.Packet{
				Type:    pbclient.SessionClose,
				Payload: []byte(`internal error, failed decoding client env vars`),
				Spec: map[string][]byte{
					pb.SpecClientExitCodeKey: []byte(`1`),
					pb.SpecGatewaySessionID:  sessionID,
				},
			})
			return nil
		}
		for key, val := range clientEnvVars {
			if _, ok := connParams.EnvVars[key]; ok {
				continue
			}
			connParams.EnvVars[key] = val
		}
	}
	return &connParams
}

func (a *Agent) decodeDLPCredentials(sessionID []byte, pkt *pb.Packet) dlp.Client {
	if gcpRawCred, ok := pkt.Spec[pb.SpecAgentGCPRawCredentialsKey]; ok {
		if _, ok := a.connStore.Get(dlpClientKey).(dlp.Client); !ok {
			dlpClient, err := dlp.NewDLPClient(context.Background(), gcpRawCred)
			if err != nil {
				log.Infof("failed creating dlp client, err=%v", err)
				sentry.CaptureException(err)
				_ = a.client.Send(&pb.Packet{
					Type:    pbclient.SessionClose,
					Payload: []byte(`failed creating dlp client`),
					Spec: map[string][]byte{
						pb.SpecClientExitCodeKey: []byte(`1`),
						pb.SpecGatewaySessionID:  sessionID,
					},
				})
				return nil
			}
			log.Infof("session=%v - created dlp client with success", string(sessionID))
			return dlpClient
		}
	}
	log.Infof("session=%v - dlp is unavailable for this connection, missing gcp credentials", string(sessionID))
	return nil
}

func (a *Agent) processSessionOpen(pkt *pb.Packet) {
	sessionID := pkt.Spec[pb.SpecGatewaySessionID]
	sessionIDKey := string(sessionID)
	log.Infof("session=%s - received connect request", sessionIDKey)

	connParams, connEnvVars, err := a.buildConnectionParams(pkt)
	if err != nil {
		log.Warnf("failed building connection params, err=%v", err)
		_ = a.client.Send(&pb.Packet{
			Type:    pbclient.SessionClose,
			Payload: []byte(err.Error()),
			Spec: map[string][]byte{
				pb.SpecClientExitCodeKey: []byte(`1`),
				pb.SpecGatewaySessionID:  sessionID,
			},
		})
		return
	}

	encFn := base64.StdEncoding.EncodeToString
	connParams.EnvVars["envvar:HOOP_CONNECTION_NAME"] = encFn([]byte(connParams.ConnectionName))
	connParams.EnvVars["envvar:HOOP_CONNECTION_TYPE"] = encFn([]byte(connParams.ConnectionType))
	connParams.EnvVars["envvar:HOOP_USER_ID"] = encFn([]byte(connParams.UserID))
	connParams.EnvVars["envvar:HOOP_SESSION_ID"] = encFn(sessionID)

	if a.connStore.Get(dlpClientKey) == nil {
		dlpClient := a.decodeDLPCredentials(sessionID, pkt)
		if dlpClient != nil {
			a.connStore.Set(dlpClientKey, dlpClient)
		}
	}

	go func() {
		if err := a.loadHooks(sessionIDKey, connParams); err != nil {
			log.Error(err)
			sentry.CaptureException(err)
			_ = a.client.Send(&pb.Packet{
				Type:    pbclient.SessionClose,
				Payload: []byte(`failed loading plugin hooks for this connection`),
				Spec: map[string][]byte{
					pb.SpecClientExitCodeKey: []byte(`1`),
					pb.SpecGatewaySessionID:  sessionID,
				},
			})
			return
		}
		if err := a.checkTCPLiveness(pkt, connEnvVars); err != nil {
			_ = a.client.Send(&pb.Packet{
				Type:    pbclient.SessionClose,
				Payload: []byte(err.Error()),
				Spec: map[string][]byte{
					pb.SpecClientExitCodeKey: []byte(`1`),
					pb.SpecGatewaySessionID:  sessionID,
				},
			})
		}
		a.client.Send(&pb.Packet{
			Type: pbclient.SessionOpenOK,
			Spec: map[string][]byte{
				pb.SpecGatewaySessionID: sessionID,
				pb.SpecConnectionType:   pkt.Spec[pb.SpecConnectionType]}})
		log.Infof("session=%v - sent gateway connect ok", string(sessionID))
	}()
}

func (a *Agent) processTCPCloseConnection(pkt *pb.Packet) {
	sessionID := pkt.Spec[pb.SpecGatewaySessionID]
	clientConnID := pkt.Spec[pb.SpecClientConnectionID]
	filterKey := fmt.Sprintf("%s:%s", string(sessionID), string(clientConnID))
	log.Infof("received %s, filter-by=%s", pkt.Type, filterKey)
	filterFn := func(k string) bool { return strings.HasPrefix(k, filterKey) }
	for key, obj := range a.connStore.Filter(filterFn) {
		if client, _ := obj.(io.Closer); client != nil {
			defer func() {
				if err := client.Close(); err != nil {
					log.Infof("failed closing connection, err=%v", err)
				}
			}()
			a.connStore.Del(key)
		}
	}
}

func (a *Agent) processSessionClose(pkt *pb.Packet) {
	sessionID := string(pkt.Spec[pb.SpecGatewaySessionID])
	if sessionID == "" {
		log.Infof("received packet %v without a session", pkt.Type)
		return
	}
	a.sessionCleanup(sessionID)
}

func (a *Agent) sessionCleanup(sessionID string) {
	log.Infof("session=%s - cleaning up session", sessionID)
	filterFn := func(k string) bool { return strings.Contains(k, sessionID) }
	for key, obj := range a.connStore.Filter(filterFn) {
		switch v := obj.(type) {
		case *hook.ClientList:
			a.connStore.Del(key)
			for _, hookClient := range v.Items() {
				*hookClient.SessionCounter()--
				if *hookClient.SessionCounter() <= 0 {
					go hookClient.Kill()
				}
			}
		case io.Closer:
			go func() {
				if err := v.Close(); err != nil {
					log.Printf("failed closing connection, err=%v", err)
				}
			}()
			a.connStore.Del(key)
		}
	}
}

func (a *Agent) sendClientSessionClose(sessionID string, errMsg string, specKeyVal ...string) {
	if sessionID == "" {
		return
	}
	var errPayload []byte
	spec := map[string][]byte{pb.SpecGatewaySessionID: []byte(sessionID)}
	for _, keyval := range specKeyVal {
		parts := strings.Split(keyval, "=")
		if len(parts) == 2 {
			spec[parts[0]] = []byte(parts[1])
		}
	}
	if errMsg != "" {
		errPayload = []byte(errMsg)
	}
	_ = a.client.Send(&pb.Packet{
		Type:    pbclient.SessionClose,
		Payload: errPayload,
		Spec:    spec,
	})
}
