package controller

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/hoophq/hoop/agent/config"
	"github.com/hoophq/hoop/agent/controller/system/dbprovisioner"
	"github.com/hoophq/hoop/agent/controller/system/runbookhook"
	"github.com/hoophq/hoop/agent/secretsmanager"
	term "github.com/hoophq/hoop/agent/terminal"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/memory"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	pbclient "github.com/hoophq/hoop/common/proto/client"
	pbsystem "github.com/hoophq/hoop/common/proto/system"
	"go.mongodb.org/mongo-driver/x/mongo/driver/connstring"
)

type (
	Agent struct {
		client           pb.ClientTransport
		connStore        memory.Store
		config           *config.Config
		runtimeEnvs      map[string]string
		shutdownCtx      context.Context
		shutdownCancelFn context.CancelCauseFunc
	}
	connEnv struct {
		scheme             string
		host               string
		address            string
		user               string
		pass               string
		port               string
		authorizedSSHKeys  string
		dbname             string
		insecure           bool
		options            string
		postgresSSLMode    string
		connectionString   string
		httpProxyRemoteURL string
		httpProxyHeaders   map[string]string
	}
)

func (e *connEnv) Get(key string) string {
	values, _ := url.ParseQuery(e.options)
	if values == nil {
		return ""
	}
	return values.Get(key)
}

// func (e *connEnv) Has(key string) bool { return e.Get(key) != "" }
func (e *connEnv) Address() string {
	if e.address != "" {
		return e.address
	}
	return e.host + ":" + e.port
}

func New(client pb.ClientTransport, cfg *config.Config, runtimeEnvs map[string]string) *Agent {
	shutdownCtx, cancelFn := context.WithCancelCause(context.Background())
	return &Agent{
		client:           client,
		connStore:        memory.New(),
		config:           cfg,
		runtimeEnvs:      runtimeEnvs,
		shutdownCtx:      shutdownCtx,
		shutdownCancelFn: cancelFn,
	}
}

func (a *Agent) Close(cause error) {
	log.Infof("shutting down agent controller")
	for _, obj := range a.connStore.List() {
		if client, _ := obj.(io.Closer); client != nil {
			_ = client.Close()
		}
	}
	a.shutdownCancelFn(cause)
	_, _ = a.client.Close()
}

func (a *Agent) Run() error {
	a.client.StartKeepAlive()
	for {
		select {
		case <-a.shutdownCtx.Done():
			log.Infof("returning context done ...")
			return context.Cause(a.shutdownCtx)
		default:
		}
		pkt, err := a.client.Recv()
		if err != nil {
			return err
		}
		sid := string(pkt.Spec[pb.SpecGatewaySessionID])
		log.With("sid", sid).Debugf("received client packet [%v]", pkt.Type)
		switch pkt.Type {
		case pbagent.GatewayConnectOK:
			log.Infof("connected with success to %v", a.config.URL)
		case pbagent.SessionOpen:
			a.processSessionOpen(pkt)

		// terminal exec
		case pbagent.ExecWriteStdin:
			a.doExec(pkt)

		// PG protocol
		case pbagent.PGConnectionWrite:
			a.processPGProtocol(pkt)

		// MySQL protocol
		case pbagent.MySQLConnectionWrite:
			a.processMySQLProtocol(pkt)

		// MSSQL Protocol
		case pbagent.MSSQLConnectionWrite:
			a.processMSSQLProtocol(pkt)

		// MongoDB Protocol
		case pbagent.MongoDBConnectionWrite:
			a.processMongoDBProtocol(pkt)

		// raw tcp
		case pbagent.TCPConnectionWrite:
			a.processTCPWriteServer(pkt)

		// http proxy
		case pbagent.HttpProxyConnectionWrite:
			a.processHttpProxyWriteServer(pkt)

		// SSH protocol
		case pbagent.SSHConnectionWrite:
			a.processSSHProtocol(pkt)

		// terminal
		case pbagent.TerminalWriteStdin:
			a.doTerminalWriteAgentStdin(pkt)
		case pbagent.TerminalResizeTTY:
			a.doTerminalResizeTTY(pkt)

		case pbagent.SessionClose:
			a.processSessionClose(pkt)

		case pbagent.TCPConnectionClose:
			a.processTCPCloseConnection(pkt)

		// system
		case pbsystem.ProvisionDBRolesRequest:
			dbprovisioner.ProcessDBProvisionerRequest(a.client, pkt)

		case pbsystem.RunbookHookRequestType:
			runbookhook.ProcessRequest(a.client, pkt)
		}
	}
}

func (a *Agent) processSessionOpen(pkt *pb.Packet) {
	sessionID := pkt.Spec[pb.SpecGatewaySessionID]
	sessionIDKey := string(sessionID)
	log.With("sid", sessionIDKey).Infof("received connect request")

	connParams, err := a.buildConnectionParams(pkt)
	if err != nil {
		log.Warnf("failed building connection params, err=%v", err)
		_ = a.client.Send(&pb.Packet{
			Type:    pbclient.SessionClose,
			Payload: []byte(err.Error()),
			Spec: map[string][]byte{
				pb.SpecClientExitCodeKey: []byte(internalExitCode),
				pb.SpecGatewaySessionID:  sessionID,
			},
		})
		return
	}

	connParams.EnvVars["envvar:HOOP_CONNECTION_NAME"] = b64Enc([]byte(connParams.ConnectionName))
	connParams.EnvVars["envvar:HOOP_CONNECTION_TYPE"] = b64Enc([]byte(connParams.ConnectionType))
	connParams.EnvVars["envvar:HOOP_CLIENT_ORIGIN"] = b64Enc([]byte(connParams.ClientOrigin))
	connParams.EnvVars["envvar:HOOP_CLIENT_VERB"] = b64Enc([]byte(connParams.ClientVerb))
	connParams.EnvVars["envvar:HOOP_USER_ID"] = b64Enc([]byte(connParams.UserID))
	connParams.EnvVars["envvar:HOOP_USER_EMAIL"] = b64Enc([]byte(connParams.UserEmail))
	connParams.EnvVars["envvar:HOOP_SESSION_ID"] = b64Enc(sessionID)

	// Embedded mode usually has the context of the application.
	// By having all environment variable in the context of execution
	// permits a more seamless integration with internal language tooling.
	if a.config.AgentMode == pb.AgentModeEmbeddedType {
		for _, envKeyVal := range os.Environ() {
			envKey, envVal, found := strings.Cut(envKeyVal, "=")
			if !found || envKey == "HOOP_DSN" || envKey == "HOOP_KEY" {
				continue
			}
			key := fmt.Sprintf("envvar:%s", envKey)
			connParams.EnvVars[key] = b64Enc([]byte(envVal))
		}
	}

	go func() {
		if err := a.checkTCPLiveness(pkt, connParams.EnvVars); err != nil {
			_ = a.client.Send(&pb.Packet{
				Type:    pbclient.SessionClose,
				Payload: []byte(err.Error()),
				Spec: map[string][]byte{
					pb.SpecClientExitCodeKey: []byte(internalExitCode),
					pb.SpecGatewaySessionID:  sessionID,
				},
			})
		}

		requestCommand := connParams.CmdList
		requestCommand = append(requestCommand, connParams.ClientArgs...)
		a.connStore.Set(string(sessionID), connParams)
		_ = a.client.Send(&pb.Packet{
			Type: pbclient.SessionOpenOK,
			Spec: map[string][]byte{
				pb.SpecGatewaySessionID:     sessionID,
				pb.SpecConnectionType:       pkt.Spec[pb.SpecConnectionType],
				pb.SpecClientRequestPort:    pkt.Spec[pb.SpecClientRequestPort],
				pb.SpecClientExecCommandKey: []byte(strings.Join(requestCommand, " ")),
			}})
		log.With("sid", sessionIDKey).Infof("sent gateway connect ok")
	}()
}

func (a *Agent) processTCPCloseConnection(pkt *pb.Packet) {
	sessionID := pkt.Spec[pb.SpecGatewaySessionID]
	clientConnID := pkt.Spec[pb.SpecClientConnectionID]
	filterKey := fmt.Sprintf("%s:%s", string(sessionID), string(clientConnID))
	log.With("sid", sessionID).Infof("closing tcp session, connid=%s, filter-by=%s", clientConnID, filterKey)
	filterFn := func(k string) bool { return strings.HasPrefix(k, filterKey) }
	for key, obj := range a.connStore.Filter(filterFn) {
		if client, _ := obj.(io.Closer); client != nil {
			defer func() {
				if err := client.Close(); err != nil {
					log.Warnf("failed closing connection, err=%v", err)
				}
			}()
			a.connStore.Del(key)
		}
	}
}

func (a *Agent) processSessionClose(pkt *pb.Packet) {
	sessionID := string(pkt.Spec[pb.SpecGatewaySessionID])
	if sessionID == "" {
		log.Warnf("received packet %v without a session", pkt.Type)
		return
	}
	a.sessionCleanup(sessionID)
}

func (a *Agent) sessionCleanup(sessionID string) {
	log.With("sid", sessionID).Infof("cleaning up session")
	filterFn := func(k string) bool { return strings.Contains(k, sessionID) }
	for key, obj := range a.connStore.Filter(filterFn) {
		if v, ok := obj.(io.Closer); ok {
			go func() {
				if err := v.Close(); err != nil {
					log.With("sid", sessionID).Warnf("failed closing connection, err=%v", err)
				}
			}()
			a.connStore.Del(key)
		}
	}
}

func (a *Agent) sendClientSessionClose(sessionID string, errMsg string) {
	// if it doesn't contain any error message, it has ended with success
	exitCode := "0"
	if errMsg != "" {
		exitCode = internalExitCode
	}
	a.sendClientSessionCloseWithExitCode(sessionID, errMsg, exitCode)
}

func (a *Agent) sendClientSessionCloseWithExitCode(sessionID string, errMsg, exitCode string) {
	if sessionID == "" {
		return
	}
	var errPayload []byte
	if errMsg != "" {
		errPayload = []byte(errMsg)
	}
	_ = a.client.Send(&pb.Packet{
		Type:    pbclient.SessionClose,
		Payload: errPayload,
		Spec: map[string][]byte{
			pb.SpecGatewaySessionID:  []byte(sessionID),
			pb.SpecClientExitCodeKey: []byte(exitCode),
		},
	})
}

func (a *Agent) sendClientTCPConnectionClose(sessionID, connectionID string) {
	_ = a.client.Send(&pb.Packet{
		Type:    pbclient.TCPConnectionClose,
		Payload: nil,
		Spec: map[string][]byte{
			pb.SpecGatewaySessionID:   []byte(sessionID),
			pb.SpecClientConnectionID: []byte(connectionID),
		},
	})
}

func (a *Agent) connectionParams(sessionID string) *pb.AgentConnectionParams {
	if params, ok := a.connStore.Get(sessionID).(*pb.AgentConnectionParams); ok {
		return params
	}
	return nil
}

func (a *Agent) buildConnectionParams(pkt *pb.Packet) (*pb.AgentConnectionParams, error) {
	sessionID := pkt.Spec[pb.SpecGatewaySessionID]
	sessionIDKey := string(sessionID)

	connParams := a.decodeConnectionParams(sessionID, pkt)
	if connParams == nil {
		return nil, fmt.Errorf("session %s failed to decode connection params", sessionIDKey)
	}

	for key, val := range a.runtimeEnvs {
		connParams.EnvVars[key] = val
	}

	connType := pb.ConnectionType(pkt.Spec[pb.SpecConnectionType])
	for key, b64EncVal := range connParams.EnvVars {
		if !strings.HasPrefix(key, "envvar:") {
			continue
		}
		envVal, _ := base64.StdEncoding.DecodeString(fmt.Sprintf("%v", b64EncVal))
		if len(envVal) > 0 && string(envVal) == pb.SystemAgentEnvs {
			envKey := key[7:]
			upstreamEnvVal, exists := os.LookupEnv(envKey)
			if exists {
				connParams.EnvVars[fmt.Sprintf("envvar:%s", envKey)] = b64Enc([]byte(upstreamEnvVal))
			}
			log.With("sid", sessionIDKey).Debugf("upstream system envs, key='%v', exists=%v, empty=%v",
				envKey, exists, len(upstreamEnvVal) == 0)
		}
	}

	if b64EncPaswd, ok := connParams.EnvVars["envvar:PASS"]; ok {
		switch connType {
		case pb.ConnectionTypePostgres:
			connParams.EnvVars["envvar:PGPASSWORD"] = b64EncPaswd
		case pb.ConnectionTypeMySQL:
			connParams.EnvVars["envvar:MYSQL_PWD"] = b64EncPaswd
		case pb.ConnectionTypeMSSQL:
			connParams.EnvVars["envvar:SQLCMDPASSWORD"] = b64EncPaswd
		}
	}
	return connParams, nil
}

func (a *Agent) checkTCPLiveness(pkt *pb.Packet, envVars map[string]any) error {
	sessionID := string(pkt.Spec[pb.SpecGatewaySessionID])
	connType := pb.ConnectionType(pkt.Spec[pb.SpecConnectionType])
	if connType == pb.ConnectionTypePostgres ||
		connType == pb.ConnectionTypeTCP ||
		connType == pb.ConnectionTypeMySQL ||
		connType == pb.ConnectionTypeMSSQL ||
		connType == pb.ConnectionTypeMongoDB {
		connEnvVars, err := parseConnectionEnvVars(envVars, connType)
		if err != nil {
			return err
		}
		if err := isPortActive(connEnvVars); err != nil {
			msg := fmt.Sprintf("failed connecting to remote host=%s, port=%s, reason=%v",
				connEnvVars.host, connEnvVars.port, err)
			log.With("sid", sessionID).Warn(msg)
			return fmt.Errorf("%s", msg)
		}
	}
	return nil
}

func (a *Agent) decodeConnectionParams(sessionID []byte, pkt *pb.Packet) *pb.AgentConnectionParams {
	var connParams pb.AgentConnectionParams
	encConnectionParams := pkt.Spec[pb.SpecAgentConnectionParamsKey]
	if err := pb.GobDecodeInto(encConnectionParams, &connParams); err != nil {
		log.With("sid", string(sessionID)).Errorf("failed decoding connection params=%#v, err=%v",
			string(encConnectionParams), err)
		sentry.CaptureException(err)
		_ = a.client.Send(&pb.Packet{
			Type:    pbclient.SessionClose,
			Payload: []byte(`internal error, failed decoding connection params`),
			Spec: map[string][]byte{
				pb.SpecClientExitCodeKey: []byte(internalExitCode),
				pb.SpecGatewaySessionID:  sessionID,
			},
		})
		return nil
	}
	envVars, err := secretsmanager.Decode(connParams.EnvVars)
	if err != nil {
		errMsg := fmt.Sprintf("failed decoding environment variables %v", err)
		log.With("sid", string(sessionID)).Warn(errMsg)
		_ = a.client.Send(&pb.Packet{
			Type:    pbclient.SessionClose,
			Payload: []byte(errMsg),
			Spec: map[string][]byte{
				pb.SpecClientExitCodeKey: []byte(internalExitCode),
				pb.SpecGatewaySessionID:  sessionID,
			},
		})
		return nil
	}
	connParams.EnvVars = envVars
	if clientEnvVarsEnc := pkt.Spec[pb.SpecClientExecEnvVar]; len(clientEnvVarsEnc) > 0 {
		var clientEnvVars map[string]string
		if err := pb.GobDecodeInto(clientEnvVarsEnc, &clientEnvVars); err != nil {
			log.With("sid", string(sessionID)).Errorf("failed decoding client env vars, err=%v", err)
			_ = a.client.Send(&pb.Packet{
				Type:    pbclient.SessionClose,
				Payload: []byte(`internal error, failed decoding client env vars`),
				Spec: map[string][]byte{
					pb.SpecClientExitCodeKey: []byte(internalExitCode),
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

func b64Enc(src []byte) string { return base64.StdEncoding.EncodeToString(src) }

func isPortActive(e *connEnv) error {
	timeout := time.Second * 5
	conn, err := net.DialTimeout("tcp", e.Address(), timeout)
	if err != nil {
		return err
	}
	conn.Close()
	return nil
}

func newTCPConn(c *connEnv) (net.Conn, error) {
	serverConn, err := net.DialTimeout("tcp4", c.Address(), time.Second*10)
	if err != nil {
		return nil, fmt.Errorf("failed dialing server: %s", err)
	}
	log.Infof("tcp connection stablished with server. address=%v, local-addr=%v",
		serverConn.LocalAddr(),
		serverConn.RemoteAddr())
	return serverConn, nil
}

func parseConnectionEnvVars(envVars map[string]any, connType pb.ConnectionType) (*connEnv, error) {
	if envVars == nil {
		return nil, fmt.Errorf("empty env vars")
	}
	envVarS, err := term.NewEnvVarStore(envVars)
	if err != nil {
		return nil, err
	}

	httpProxyHeaders := envVarS.Search(func(key string) bool { return strings.HasPrefix(strings.ToLower(key), "header_") })
	env := &connEnv{
		scheme:            envVarS.Getenv("SCHEME"),
		host:              envVarS.Getenv("HOST"),
		user:              envVarS.Getenv("USER"),
		pass:              envVarS.Getenv("PASS"),
		port:              envVarS.Getenv("PORT"),
		authorizedSSHKeys: envVarS.Getenv("AUTHORIZED_SERVER_KEYS"),
		dbname:            envVarS.Getenv("DB"),
		insecure:          envVarS.Getenv("INSECURE") == "true",
		postgresSSLMode:   envVarS.Getenv("SSLMODE"),
		options:           envVarS.Getenv("OPTIONS"),
		// this option is only used by mongodb at the momento
		connectionString:   envVarS.Getenv("CONNECTION_STRING"),
		httpProxyRemoteURL: envVarS.Getenv("REMOTE_URL"),
		httpProxyHeaders:   httpProxyHeaders,
	}
	switch connType {
	case pb.ConnectionTypePostgres:
		if env.port == "" {
			env.port = "5432"
		}
		if env.host == "" || env.pass == "" || env.user == "" {
			return nil, errors.New("missing required secrets for postgres connection [HOST, USER, PASS]")
		}
		mode := env.postgresSSLMode
		if mode == "" {
			mode = "prefer"
		}
		if mode != "require" && mode != "verify-full" && mode != "prefer" && mode != "disable" {
			return nil, fmt.Errorf("wrong option (%q) for SSLMODE, accept only: %v", mode,
				[]string{"disable", "prefer", "require", "verify-full"})
		}
	case pb.ConnectionTypeMySQL:
		if env.port == "" {
			env.port = "3306"
		}
		if env.host == "" || env.pass == "" || env.user == "" {
			return nil, errors.New("missing required secrets for mysql connection [HOST, USER, PASS]")
		}
	case pb.ConnectionTypeMSSQL:
		if env.port == "" {
			env.port = "1433"
		}
		if env.host == "" || env.pass == "" || env.user == "" {
			return nil, errors.New("missing required secrets for mssql connection [HOST, USER, PASS]")
		}
	case pb.ConnectionTypeMongoDB:
		if env.connectionString != "" {
			connStr, err := connstring.ParseAndValidate(env.connectionString)
			if err != nil {
				return nil, fmt.Errorf("failed parsing %v connection string", pb.ConnectionTypeMongoDB)
			}
			return &connEnv{connectionString: env.connectionString, address: connStr.Hosts[0]}, nil
		}
		// TODO: this usage should be deprecated, only connection string
		// should be used
		if env.port == "" {
			env.port = "27017"
		}
		if env.scheme == "" {
			env.scheme = "mongodb"
		}
		host, port, err := parseMongoDbUriHost(env)
		if err != nil {
			return nil, err
		}
		env.host = host
		env.port = port
		if env.host == "" || env.pass == "" || env.user == "" {
			return nil, errors.New("missing required secrets for mongodb connection [HOST, USER, PASS]")
		}
	case pb.ConnectionTypeSSH:
		if env.port == "" {
			env.port = "22"
		}
		if env.host == "" || (env.pass == "" && env.authorizedSSHKeys == "") || env.user == "" {
			return nil, errors.New("missing required secrets for ssh connection [HOST, USER, PASS or AUTHORIZED_SERVER_KEYS]")
		}
	case pb.ConnectionTypeTCP:
		if env.host == "" || env.port == "" {
			return nil, errors.New("missing required environment for connection [HOST, PORT]")
		}
	case pb.ConnectionTypeHttpProxy:
		if env.httpProxyRemoteURL == "" {
			return nil, fmt.Errorf("missing required environment for connection [REMOTE_URL]")
		}
		if _, err := url.Parse(env.httpProxyRemoteURL); err != nil {
			return nil, fmt.Errorf("failed parsing REMOTE_URL env, reason=%v", err)
		}
	}
	return env, nil
}

func parseMongoDbUriHost(env *connEnv) (hostname string, port string, err error) {
	uri := fmt.Sprintf("%s://%s:%s@%s:%s/", env.scheme, env.user, env.pass, env.host, env.port)
	if env.scheme == "mongodb+srv" {
		uri = fmt.Sprintf("%s://%s:%s@%s/", env.scheme, env.user, env.pass, env.host)
	}
	// it allow to obtain the options from a mongodb+srv scheme (TXT, SRV dns records)
	connStr, err := connstring.ParseAndValidate(uri)
	if err != nil {
		return "", "", err
	}
	parts := strings.Split(connStr.Hosts[0], ":")
	if len(parts) > 1 {
		return parts[0], parts[1], nil
	}
	return connStr.Hosts[0], "27017", nil
}
