package controller

import (
	"context"
	"fmt"
	"io"
	"libhoop"
	redactortypes "libhoop/redactor/types"
	"strings"

	"github.com/hoophq/hoop/agent/controller/featureflagstate"
	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	pbclient "github.com/hoophq/hoop/common/proto/client"
)

// sshGuardrailsFlag gates exec-command input and session-channel output
// guardrails on native SSH connections. sshInputGuardrailsFlag independently
// gates best-effort guardrails on interactive shell stdin (keystroke line
// reconstruction). When both are off, the agent passes no guardrails/DLP
// options to the proxy, so no validation runs and the connection behaves
// exactly as before.
const (
	sshGuardrailsFlag      = "experimental.ssh_guardrails"
	sshInputGuardrailsFlag = "experimental.ssh_input_guardrails"

	// Proxy option keys mirroring libhoop's proxyssh contract: they tell the
	// proxy which guardrails concern to enforce (the redactor client itself is
	// built whenever either flag is on).
	optSSHGuardrailsExecOutput = "ssh_guardrails_exec_output"
	optSSHGuardrailsInput      = "ssh_guardrails_input"

	// connectionModeProxy proxies to an upstream sshd.
	connectionModeProxy = "proxy"
	// connectionModeLocal terminates the session on the agent host
	connectionModeLocal = "local"
)

// processSSHWriteQueued runs on the agent's packet recv loop. Each packet is
// appended to a per-(session, connection) FIFO drained by a single worker
// goroutine: the recv loop never blocks on a slow upstream, connections
// proceed independently, and — critically — packets for one connection are
// handled in exactly the order they arrived on the gRPC stream. SSH framing
// depends on that order: an exec request dispatched before its OpenChannel
// finished establishing the upstream channel is dropped by libhoop's proxy
// ("unable to obtain channel") and the client hangs waiting for an
// exit-status that will never come (DEP-57).
func (a *Agent) processSSHWriteQueued(pkt *pb.Packet) {
	sid := string(pkt.Spec[pb.SpecGatewaySessionID])
	clientConnectionID := string(pkt.Spec[pb.SpecClientConnectionID])
	queueKey := fmt.Sprintf("%s:%s", sid, clientConnectionID)
	obj, _ := a.sshWriteQueues.LoadOrStore(queueKey, &packetQueue{})
	queue := obj.(*packetQueue)
	startWorker, overflow := queue.push(pkt)
	if overflow {
		// The drain worker is wedged (dead upstream) while the client keeps
		// writing. Fail the session explicitly instead of buffering
		// unbounded payload in agent memory; the queue entry is dropped by
		// sessionCleanup when the SessionClose lands.
		log.With("sid", sid, "conn", clientConnectionID).
			Errorf("SSH packet queue overflow, closing session")
		a.sendClientSessionClose(sid, "ssh connection stalled: too much data buffered while the upstream is unresponsive")
		return
	}
	if startWorker {
		go queue.drain(a.processSSHProtocol)
	}
}

// processSSHProtocol handles one SSHConnectionWrite packet. It must only be
// invoked from the packetQueue drain worker (see processSSHWriteQueued),
// which serializes calls per (sessionID, connectionID) — the handler relies
// on that for ordering and runs at most once concurrently per connection.
func (a *Agent) processSSHProtocol(pkt *pb.Packet) {
	sid := string(pkt.Spec[pb.SpecGatewaySessionID])

	// Hold the session RLock for the duration of the handler. SessionClose
	// takes the Lock side, which drains in-flight handlers before tearing
	// down state; any packet that arrives after cleanup has begun finds
	// closed=true here and returns without touching the store.
	state := a.sessionStateFor(sid)
	state.mu.RLock()
	defer state.mu.RUnlock()
	if state.closed.Load() {
		log.With("sid", sid).Debugf("session already closed, dropping late SSH packet")
		return
	}

	streamClient := pb.NewStreamWriter(a.client, pbclient.SSHConnectionWrite, pkt.Spec)
	connParams := a.connectionParams(sid)
	if connParams == nil {
		log.With("sid", sid).Errorf("connection params not found")
		a.sendClientSessionClose(sid, "connection params not found, contact the administrator")
		return
	}

	clientConnectionID := string(pkt.Spec[pb.SpecClientConnectionID])
	if clientConnectionID == "" && pkt.Payload != nil {
		log.With("sid", sid).Errorf("connection id not found in memory")
		a.sendClientSessionClose(sid, "connection id not found, contact the administrator")
		return
	}
	clientConnectionIDKey := fmt.Sprintf("%s:%s", sid, clientConnectionID)

	// Fast path: a proxy already exists for this connection. The drain
	// worker serializes handlers per (sid, connID), so the write needs no
	// additional locking to stay ordered.
	if serverWriter, ok := a.connStore.Get(clientConnectionIDKey).(io.WriteCloser); ok {
		if _, err := serverWriter.Write(pkt.Payload); err != nil {
			log.With("sid", sid).Errorf("failed sending packet, err=%v", err)
			a.sendClientSessionClose(sid, fmt.Sprintf("unable to write packet: %v", err))
			_ = serverWriter.Close()
		}
		return
	}

	// Slow path: this is the first packet for the connection — build the
	// libhoop proxy. Per-connection serialization guarantees no concurrent
	// first-packet, so exactly one upstream dial happens per connection.
	buildProxy := func() (io.WriteCloser, error) {
		opts := map[string]string{
			"sid":             sid,
			"connection_id":   clientConnectionID,
			"connection_mode": connectionModeProxy,
		}

		if connParams.ConnectionSubType == "ssh-local" {
			opts["connection_mode"] = connectionModeLocal
		}

		if opts["connection_mode"] == connectionModeProxy {
			connenv, parseErr := parseConnectionEnvVars(connParams.EnvVars, pb.ConnectionTypeSSH)
			if parseErr != nil {
				return nil, fmt.Errorf("SSH credentials not found in memory: %v", parseErr)
			}
			opts["hostname"] = connenv.host
			opts["port"] = connenv.port
			opts["username"] = connenv.user
			opts["password"] = connenv.pass
			opts["authorized_server_keys"] = connenv.authorizedSSHKeys
		}

		log.With("sid", sid, "conn", clientConnectionID).
			Infof("starting SSH proxy connection, mode=%v", opts["connection_mode"])
		// Guardrails enforcement for SSH is split across two independent feature
		// flags: one for exec-command input + session output, one for best-effort
		// interactive-shell stdin. Build the redactor client (and thread the DLP
		// options through) when EITHER is enabled, then tell the proxy which
		// concern to enforce. With both off no options are set, no redactor
		// client is built, and the proxy runs without validation (unchanged).
		execOutputEnabled := featureflagstate.IsEnabled(sshGuardrailsFlag)
		inputEnabled := featureflagstate.IsEnabled(sshInputGuardrailsFlag)
		if execOutputEnabled || inputEnabled {
			addGuardRailsOpts(opts, connParams)
			if execOutputEnabled {
				opts[optSSHGuardrailsExecOutput] = "true"
			}
			if inputEnabled {
				opts[optSSHGuardrailsInput] = "true"
			}
		}
		proxy, proxyErr := libhoop.NewSSHProxy(context.Background(), streamClient, opts)
		if proxyErr != nil {
			return nil, fmt.Errorf("failed initializing SSH proxy connection: %v", proxyErr)
		}

		proxy.Run(func(_ int, errMsg string) {
			a.connStore.Del(clientConnectionIDKey)
			// When the proxy blocked the session on a guardrails violation it
			// records the matched rules. Surface them as structured info so the
			// gateway persists them and the user sees the "Blocked by ...
			// Guardrails Rules" message instead of a generic error.
			if gr, ok := proxy.(interface {
				GuardRailsViolation() []redactortypes.GuardRailsInfo
			}); ok {
				if info := gr.GuardRailsViolation(); len(info) > 0 {
					a.sendClientSessionCloseWithGuardRailsInfo(sid, "", internalExitCode, info)
					return
				}
			}
			a.sendClientSessionClose(sid, errMsg)
		})

		a.connStore.Set(clientConnectionIDKey, proxy)
		return proxy, nil
	}

	serverWriter, err := buildProxy()
	if err != nil {
		log.With("sid", sid, "conn", clientConnectionID).Errorf("%v", err)
		a.sendClientSessionClose(sid, err.Error())
		return
	}

	// Write the first packet's payload. Ordering against subsequent packets
	// is provided by the per-connection drain worker.
	if _, writeErr := serverWriter.Write(pkt.Payload); writeErr != nil {
		errMsg := fmt.Sprintf("unable to connect with remote SSH server, err=%v", writeErr)
		log.With("sid", sid, "conn", clientConnectionID).Errorf(errMsg)
		a.sendClientSessionClose(sid, errMsg)
	}
}

// addGuardRailsOpts threads the DLP/guardrails connection options into the SSH
// proxy options, mirroring the keys used by the database proxies. libhoop uses
// these to build a redactor client that the SSH proxy invokes for guardrails
// validation only (it does not redact SSH traffic). A DLP provider
// (Presidio/GCP) must be configured for guardrails rules to be enforced.
func addGuardRailsOpts(opts map[string]string, connParams *pb.AgentConnectionParams) {
	var dataMaskingEntityTypesData string
	if connParams.DataMaskingEntityTypesData != nil {
		dataMaskingEntityTypesData = string(connParams.DataMaskingEntityTypesData)
	}
	var guardRailRules string
	if connParams.GuardRailRules != nil {
		guardRailRules = string(connParams.GuardRailRules)
	}
	opts["dlp_provider"] = connParams.DlpProvider
	opts["dlp_mode"] = connParams.DlpMode
	opts["mspresidio_analyzer_url"] = connParams.DlpPresidioAnalyzerURL
	opts["mspresidio_anonymizer_url"] = connParams.DlpPresidioAnonymizerURL
	opts["dlp_gcp_credentials"] = connParams.DlpGcpRawCredentialsJSON
	opts["dlp_info_types"] = strings.Join(connParams.DLPInfoTypes, ",")
	opts["dlp_masking_character"] = "#"
	opts["data_masking_entity_data"] = dataMaskingEntityTypesData
	opts["guard_rail_rules"] = guardRailRules
}
