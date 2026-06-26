package controller

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"libhoop"
	redactortypes "libhoop/redactor/types"
	"net"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/hoophq/hoop/agent/config"
	"github.com/hoophq/hoop/agent/controller/awseks"
	"github.com/hoophq/hoop/agent/controller/featureflagstate"
	"github.com/hoophq/hoop/agent/controller/system/bareexec"
	"github.com/hoophq/hoop/agent/controller/system/dbprovisioner"
	"github.com/hoophq/hoop/agent/controller/system/pgmanager"
	"github.com/hoophq/hoop/agent/controller/system/runbookhook"
	"github.com/hoophq/hoop/agent/rds"
	"github.com/hoophq/hoop/agent/secretsmanager"
	term "github.com/hoophq/hoop/agent/terminal"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/memory"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	pbclient "github.com/hoophq/hoop/common/proto/client"
	pbgateway "github.com/hoophq/hoop/common/proto/gateway"
	pbsystem "github.com/hoophq/hoop/common/proto/system"
	"go.mongodb.org/mongo-driver/x/mongo/driver/connstring"
	"golang.org/x/sync/singleflight"
)

type (
	// sessionState carries a per-session RW lock and a closed sentinel.
	// Packet handlers take RLock for the duration of their work; SessionClose
	// takes Lock, which drains any in-flight handlers before cleanup runs.
	// The closed flag, read under RLock, lets handlers drop packets that
	// arrive after cleanup has begun.
	sessionState struct {
		mu     sync.RWMutex
		closed atomic.Bool
	}

	Agent struct {
		client           pb.ClientTransport
		connStore        memory.Store
		config           *config.Config
		runtimeEnvs      map[string]string
		shutdownCtx      context.Context
		shutdownCancelFn context.CancelCauseFunc

		// sessionStates is keyed by gateway session ID. Entries are created
		// lazily on first access and live for the duration of the agent
		// process; never deleting them is what lets late packets find
		// closed=true under RLock instead of racing through a fresh state.
		sessionStates sync.Map

		// connWriteLocks serializes writes per (sessionID, connectionID).
		// Required when packet dispatch is async (see Run) so that two
		// goroutines handling consecutive packets for the same connection
		// don't reorder writes to the upstream proxy.
		connWriteLocks sync.Map

		// sshFlightGroup deduplicates concurrent first-packet handling for
		// the same (sessionID, connectionID) in processSSHProtocol. Without
		// it, async-dispatched goroutines could each miss the connStore
		// cache and dial duplicate upstream SSH connections.
		sshFlightGroup singleflight.Group

		// gcpTokenSources caches one oauth2.TokenSource per session (keyed by
		// gateway session ID) for claude-code connections that federate to
		// Google Vertex AI. The source is built once from the connection's
		// service-account key and reused for the life of the session so the
		// bearer is minted lazily and auto-refreshed by the oauth2 library
		// shortly before expiry — rather than re-minted on every proxied
		// request. Entries are removed in sessionCleanup.
		gcpTokenSources sync.Map
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
		serviceName        string
		insecure           bool
		options            string
		postgresSSLMode    string
		connectionString   string
		httpProxyRemoteURL string
		httpProxyHeaders   map[string]string
		// gcpServiceAccountJSON carries the GCP service-account key configured
		// on a claude-code connection that federates to Google Vertex AI. When
		// present (and the experimental.claude_code_vertex flag is on) the agent
		// mints a short-lived OAuth bearer from it and injects it as the upstream
		// Authorization header. Empty for every other connection.
		gcpServiceAccountJSON string
		awsRegion             string
		awsSecretAccessKey    string
		awsAccessKeyID        string

		kubernetesClusterURL         string
		kubernetesToken              string
		kubernetesInsecureSkipVerify bool

		experimentalRedactRows string
	}
	ioMetricFlush struct {
		client pb.ClientTransport
		sid    string
	}
)

func newIoMetricFlush(client pb.ClientTransport, sessionID string) io.Writer {
	return &ioMetricFlush{client: client, sid: sessionID}
}

func (i *ioMetricFlush) Write(data []byte) (int, error) {
	err := i.client.Send(&pb.Packet{
		Type:    pbclient.SessionAnalyzerMetrics,
		Payload: data,
		Spec: map[string][]byte{
			pb.SpecGatewaySessionID: []byte(i.sid),
		},
	})
	if err != nil {
		log.With("sid", string(i.sid)).Warnf("failed sending analyzer metrics to gateway, err=%v", err)
		return 0, err
	}
	return len(data), nil
}

func (e *connEnv) Get(key string) string {
	values, _ := url.ParseQuery(e.options)
	if values == nil {
		return ""
	}
	return values.Get(key)
}

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

// sessionStateFor returns the per-session lock-and-state record, creating
// it on first reference. Entries persist for the lifetime of the agent
// process so that a late packet for a closed session always finds
// closed=true rather than racing through a freshly-allocated state.
func (a *Agent) sessionStateFor(sessionID string) *sessionState {
	if v, ok := a.sessionStates.Load(sessionID); ok {
		return v.(*sessionState)
	}
	state := &sessionState{}
	actual, _ := a.sessionStates.LoadOrStore(sessionID, state)
	return actual.(*sessionState)
}

// connWriteLockFor returns the per-connection write mutex. It serializes
// writes to a single upstream proxy when packet dispatch is async, so
// consecutive packets for the same (sessionID, connectionID) do not
// reorder at the libhoop layer.
func (a *Agent) connWriteLockFor(key string) *sync.Mutex {
	if v, ok := a.connWriteLocks.Load(key); ok {
		return v.(*sync.Mutex)
	}
	mu := &sync.Mutex{}
	actual, _ := a.connWriteLocks.LoadOrStore(key, mu)
	return actual.(*sync.Mutex)
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

func (a *Agent) processPacket(pkt *pb.Packet) {
	sid := string(pkt.Spec[pb.SpecGatewaySessionID])
	log.With("sid", sid).Debugf("received client packet [%v]", pkt.Type)
	switch pkt.Type {
	case pbagent.GatewayConnectOK:
		log.Infof("connected with success to %v", a.config.URL)
	case pbgateway.FeatureFlagUpdate:
		featureflagstate.Update(pkt.Spec)
	case pbagent.SessionOpen:
		a.processSessionOpen(pkt)

	// terminal exec
	case pbagent.ExecWriteStdin:
		a.doExec(pkt)

	case pbagent.SSMConnectionWrite:
		a.processSSMProtocol(pkt)

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

	// Oracle Protocol
	case pbagent.OracleConnectionWrite:
		a.processOracleProtocol(pkt)

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

	case pbsystem.BareExecRequestType:
		bareexec.ProcessRequest(a.client, pkt)

	case pbsystem.PgManagerPlanRequestType:
		pgmanager.ProcessPlanRequest(a.client, pkt)

	case pbsystem.PgManagerApplyRequestType:
		pgmanager.ProcessApplyPlan(a.client, pkt)
	}
}

func (a *Agent) Run() error {
	a.client.StartKeepAlive()

	for {
		pkt, err := a.client.Recv()
		if err != nil {
			return err
		}

		select {
		case <-a.shutdownCtx.Done():
			return context.Cause(a.shutdownCtx)
		default:
		}

		// SSH connection writes and the SessionClose that ends them are
		// dispatched concurrently so a slow upstream on one session does
		// not stall packet processing for other sessions on this agent.
		// processSSHProtocol uses the session RW lock, per-connection
		// write mutex, and singleflight group on the Agent to keep
		// concurrent handlers correct.
		//
		// SessionClose is included so the recv loop is not blocked when
		// it has to wait for an in-flight SSH handler to drain before
		// running session cleanup.
		if pkt.Type == pbagent.SSHConnectionWrite || pkt.Type == pbagent.SessionClose {
			go a.processPacket(pkt)
			continue
		}

		a.processPacket(pkt)
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
	// By having all environment variables in the context of execution
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
		client, _ := obj.(libhoop.Proxy)
		if client != nil {
			go func() {
				if err := client.FlushMetrics(newIoMetricFlush(a.client, string(sessionID))); err != nil {
					log.With("sid", string(sessionID)).Warnf("failed flushing io metrics, err=%v", err)
				}
			}()
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

	// Acquire the session's write lock and mark it closed before iterating
	// the connStore. RLock holders (in-flight packet handlers) drain here,
	// guaranteeing that no handler is mid-build when we start tearing down
	// proxies. Late packets arriving after Unlock find closed=true under
	// their own RLock and return without touching the store.
	state := a.sessionStateFor(sessionID)
	state.mu.Lock()
	state.closed.Store(true)
	defer state.mu.Unlock()

	filterFn := func(k string) bool { return strings.Contains(k, sessionID) }
	for key, obj := range a.connStore.Filter(filterFn) {
		if p, ok := obj.(libhoop.Proxy); ok {
			go func() {
				if err := p.FlushMetrics(newIoMetricFlush(a.client, sessionID)); err != nil {
					log.With("sid", sessionID).Warnf("failed flushing io metrics, err=%v", err)
				}
			}()
		}
		if v, ok := obj.(io.Closer); ok {
			go func() {
				if err := v.Close(); err != nil {
					log.With("sid", sessionID).Warnf("failed closing connection, err=%v", err)
				}
			}()
		}
		a.connStore.Del(key)
		a.connWriteLocks.Delete(key)
	}
	// Drop any cached Vertex token source so the service-account-derived
	// credential does not outlive the session in agent memory.
	a.gcpTokenSources.Delete(sessionID)
}

func (a *Agent) sendClientSessionClose(sessionID string, errMsg string) {
	// if it doesn't contain any error message, it has ended with success
	exitCode := "0"
	if errMsg != "" {
		exitCode = internalExitCode
	}
	a.sendClientSessionCloseWithExitCode(sessionID, errMsg, exitCode)
}

func (a *Agent) sendClientSessionCloseFromError(sessionID string, err error) {
	if err == nil {
		a.sendClientSessionCloseWithExitCode(sessionID, "", "0")
		return
	}

	var guardrailErr *redactortypes.ErrGuardrailsValidation
	if errors.As(err, &guardrailErr) {
		a.sendClientSessionCloseWithGuardRailsInfo(sessionID, "", internalExitCode, guardrailErr.Info())
		return
	}

	a.sendClientSessionCloseWithExitCode(sessionID, err.Error(), internalExitCode)
}

func (a *Agent) sendClientSessionCloseWithExitCode(sessionID string, errMsg, exitCode string) {
	a.sendClientSessionCloseWithGuardRailsInfo(sessionID, errMsg, exitCode, nil)
}

func (a *Agent) sendClientSessionCloseWithGuardRailsInfo(sessionID string, errMsg, exitCode string, guardRailsInfo []redactortypes.GuardRailsInfo) {
	if sessionID == "" {
		return
	}
	var errPayload []byte
	if errMsg != "" {
		errPayload = []byte(errMsg)
	}
	spec := map[string][]byte{
		pb.SpecGatewaySessionID:  []byte(sessionID),
		pb.SpecClientExitCodeKey: []byte(exitCode),
	}
	if len(guardRailsInfo) > 0 {
		if rawInfo, err := json.Marshal(guardRailsInfo); err == nil {
			spec[pb.SpecClientGuardRailsInfoKey] = rawInfo
		} else {
			log.With("sid", sessionID).Warnf("failed marshaling guardrails info for session close, err=%v", err)
		}
	}

	_ = a.client.Send(&pb.Packet{
		Type:    pbclient.SessionClose,
		Payload: errPayload,
		Spec:    spec,
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

	// Override MSPresidio configuration
	if analyzerURL, anonymizerURL, dlpMode, isSet := parseMSPresidioOverrideConfig(); isSet {
		log.Infof("overriding MS Presidio configuration, dlp-mode=%v", dlpMode)
		connParams.DlpPresidioAnalyzerURL = analyzerURL
		connParams.DlpPresidioAnonymizerURL = anonymizerURL
		connParams.DlpMode = dlpMode
	}

	// expose agent envs to session
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

	// EKS integration
	if _, ok := connParams.EnvVars["envvar:EKS_CLUSTER"]; ok {
		eksClusterName, eksAwsRegion, eksSessionRole, roleArn, err := parseEksIntegrationEnvs(connParams.EnvVars)
		if err != nil {
			return nil, err
		}
		log.With("sid", sessionIDKey).
			Infof("generating eks token, cluster=%v, region=%v, session-role=%v", eksClusterName, eksAwsRegion, eksSessionRole)
		token, err := awseks.CreateEKSToken(eksClusterName, eksAwsRegion, roleArn, eksSessionRole)
		if err != nil {
			return nil, fmt.Errorf("failed to generate eks token: %v", err)
		}
		tokenBearer := fmt.Sprintf("Bearer %s", token)
		connParams.EnvVars["envvar:KUBERNETES_BEARER_TOKEN"] = b64Enc([]byte(tokenBearer))
	}

	// RDS iam auth token
	userValue, ok := connParams.EnvVars["envvar:USER"]
	if ok {
		d, err := base64.StdEncoding.DecodeString(fmt.Sprintf("%v", userValue))
		if err != nil {
			log.With("sid", sessionIDKey).Warnf("failed decoding USER env var, err=%v", err)
			return nil, fmt.Errorf("failed decoding USER env var, err=%v", err)
		}

		if strings.HasPrefix(string(d), "_aws_iam_rds:") {
			rdsAuthEnv, err := rds.BuildRdsEnvAuth(connParams.EnvVars)
			if err != nil {
				log.With("sid", sessionIDKey).Warnf("failed generating aws rds auth token, err=%v", err)
				return nil, fmt.Errorf("failed generating aws rds auth token, err=%v", err)
			}

			//overwrite env vars with rds auth
			connParams.EnvVars = rdsAuthEnv
			if connParams.ConnectionType == "mysql" {
				// look for --enable-cleartext-plugin
				hasEnableClearTextPlugin := slices.Contains(connParams.CmdList, "--enable-cleartext-plugin")
				//https://docs.aws.amazon.com/AmazonRDS/latest/AuroraUserGuide/UsingWithRDS.IAMDBAuth.Connecting.AWSCLI.html#UsingWithRDS.IAMDBAuth.Connecting.AWSCLI.Connect
				//--enable-cleartext-plugin – A value that specifies that AWSAuthenticationPlugin must be used for this connection
				//If you are using a MariaDB client, the --enable-cleartext-plugin option isn't required.
				if !hasEnableClearTextPlugin {
					connParams.CmdList = append(connParams.CmdList, "--enable-cleartext-plugin")
				}
			}
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
		serviceName:       envVarS.Getenv("SID"),
		insecure:          envVarS.Getenv("INSECURE") == "true",
		postgresSSLMode:   envVarS.Getenv("SSLMODE"),
		options:           envVarS.Getenv("OPTIONS"),
		// this option is only used by mongodb at the momento
		connectionString:      envVarS.Getenv("CONNECTION_STRING"),
		httpProxyRemoteURL:    envVarS.Getenv("REMOTE_URL"),
		httpProxyHeaders:      httpProxyHeaders,
		gcpServiceAccountJSON: envVarS.Getenv("GCP_SERVICE_ACCOUNT_JSON"),

		kubernetesClusterURL:         envVarS.Getenv("KUBERNETES_CLUSTER_URL"),
		kubernetesToken:              envVarS.Getenv("KUBERNETES_BEARER_TOKEN"),
		kubernetesInsecureSkipVerify: envVarS.Getenv("KUBERNETES_INSECURE_SKIP_VERIFY") == "true",

		experimentalRedactRows: envVarS.Getenv("EXPERIMENTAL_REDACT_ROWS"),
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
	case pb.ConnectionTypeOracleDB:
		if env.port == "" {
			env.port = "1521"
		}
		if env.host == "" || env.pass == "" || env.user == "" || env.serviceName == "" {
			return nil, errors.New("missing required secrets for oracledb connection [HOST, USER, PASS, SID]")
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
	case pb.ConnectionTypeKubernetes:
		if env.kubernetesToken == "" {
			return nil, errors.New("missing required environment for connection [KUBERNETES_BEARER_TOKEN]")
		}
		if env.kubernetesClusterURL == "" {
			// default url when running in-cluster
			env.kubernetesClusterURL = "https://kubernetes.default.svc.cluster.local"
		}
		env.httpProxyHeaders["HEADER_AUTHORIZATION"] = env.kubernetesToken
		if strings.HasPrefix("Bearer ", env.kubernetesToken) {
			env.httpProxyHeaders["HEADER_AUTHORIZATION"] = fmt.Sprintf("Bearer %s", env.kubernetesToken)
		}
	case pb.ConnectionTypeHttpProxy:
		if env.httpProxyRemoteURL == "" {
			return nil, fmt.Errorf("missing required environment for connection [REMOTE_URL]")
		}

		if _, err := url.Parse(env.httpProxyRemoteURL); err != nil {
			return nil, fmt.Errorf("failed parsing REMOTE_URL env, reason=%v", err)
		}
	case pb.ConnectionTypeSSM:
		env.awsAccessKeyID = envVarS.Getenv("AWS_ACCESS_KEY_ID")
		env.awsSecretAccessKey = envVarS.Getenv("AWS_SECRET_ACCESS_KEY")
		env.awsRegion = envVarS.Getenv("AWS_REGION")
		if env.awsAccessKeyID == "" || env.awsSecretAccessKey == "" {
			return nil, fmt.Errorf("missing required secrets for SSM connection [AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY]")
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

func parseEksIntegrationEnvs(envVar map[string]any) (cluster, awsRegion, roleSession, roleArn string, err error) {
	awsRegion = os.Getenv("AWS_REGION")
	for key, encVal := range envVar {
		val, _ := base64.StdEncoding.DecodeString(fmt.Sprintf("%v", encVal))
		switch key {
		case "envvar:EKS_CLUSTER":
			cluster = string(val)
		case "envvar:EKS_AWS_REGION":
			awsRegion = string(val)
		case "envvar:EKS_ROLE_SESSION", "envvar:EKS_BINDING_USER_ROLE":
			roleSession = string(val)
		case "envvar:EKS_ROLE_ARN":
			roleArn = string(val)
		}
	}
	if cluster == "" || awsRegion == "" {
		return "", "", "", "", fmt.Errorf("missing required envs [EKS_CLUSTER EKS_AWS_REGION]")
	}
	return
}

func parseMSPresidioOverrideConfig() (analyzerURL, anonymizerURL, dlpMode string, isSet bool) {
	analyzerURL = os.Getenv("MSPRESIDIO_ANALYZER_URL")
	anonymizerURL = os.Getenv("MSPRESIDIO_ANONYMIZER_URL")
	dlpMode = os.Getenv("DLP_MODE")
	if _, err := url.Parse(analyzerURL); err != nil {
		log.Warnf("MSPRESIDIO_ANALYZER_URL failed loading override configuration, invalid url: %v", err)
		return
	}
	if _, err := url.Parse(anonymizerURL); err != nil {
		log.Warnf("MSPRESIDIO_ANONYMIZER_URL failed loading override configuration, invalid url: %v", err)
		return
	}
	if dlpMode != "strict" && dlpMode != "best-effort" {
		log.Warnf("DLP_MODE unknown value (%q), fallback to best-effort", dlpMode)
	}
	isSet = analyzerURL != "" && anonymizerURL != ""
	return
}
