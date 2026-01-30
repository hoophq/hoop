package controller

import (
	"context"
	"fmt"
	"io"
	"libhoop"
	"strings"

	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	pbclient "github.com/hoophq/hoop/common/proto/client"
)

func (a *Agent) processHttpProxyWriteServer(pkt *pb.Packet) {
	sessionID := string(pkt.Spec[pb.SpecGatewaySessionID])
	clientConnectionID := string(pkt.Spec[pb.SpecClientConnectionID])
	proxyBaseURL := string(pkt.Spec[pb.SpecHttpProxyBaseUrl])
	log := log.With("sid", sessionID, "conn", clientConnectionID)
	if clientConnectionID == "" {
		log.Info("connection not found in packet specfication")
		a.sendClientSessionClose(sessionID, "http proxy connection id not found")
		return
	}
	connParams := a.connectionParams(sessionID)
	if connParams == nil {
		log.Infof("connection params not found")
		a.sendClientSessionClose(sessionID, "connection params not found, contact the administrator")
		return
	}
	clientConnectionIDKey := fmt.Sprintf("%s:%s", sessionID, string(clientConnectionID))
	if httpServer, ok := a.connStore.Get(clientConnectionIDKey).(io.WriteCloser); ok {
		if _, err := httpServer.Write(pkt.Payload); err != nil {
			// ErrWebSocketMode is not an error - it signals WebSocket mode was activated
			if isWebSocketModeError(err) {
				log.Debugf("websocket mode activated, continuing")
				return
			}
			log.Infof("failed writing packet, err=%v", err)
			_ = httpServer.Close()
			// Check if this is a guardrails error - these should close the session with the error message
			if isGuardrailsError(err) {
				log.Infof("guardrails validation failed, closing session: %v", err)
				a.sendClientSessionClose(sessionID, err.Error())
				return
			}
			// If we have and multiple websocket connection open and we kill one of them
			// then we should not close the entire session, just this connection only.
			// the proxy session will be closed when the hoop connect <role> is closed.
			a.sendClientTCPConnectionClose(sessionID, clientConnectionID)
		}
		return
	}
	httpStreamClient := pb.NewStreamWriter(a.client, pbclient.HttpProxyConnectionWrite, pkt.Spec)
	connenv, err := parseConnectionEnvVars(connParams.EnvVars, pb.ConnectionType(connParams.ConnectionType))
	if err != nil {
		log.Infof("missing connection credentials in memory, err=%v", err)
		a.sendClientSessionClose(sessionID, "credentials are empty, contact the administrator")
		return
	}

	log.Infof("starting http proxy connection at %v", connenv.httpProxyRemoteURL)

	var guardRailRules string
	if connParams.GuardRailRules != nil {
		guardRailRules = string(connParams.GuardRailRules)
	}
	var dataMaskingEntityTypesData string
	if connParams.DataMaskingEntityTypesData != nil {
		dataMaskingEntityTypesData = string(connParams.DataMaskingEntityTypesData)
	}
	connenv.httpProxyHeaders["remote_url"] = connenv.httpProxyRemoteURL
	connenv.httpProxyHeaders["connection_id"] = clientConnectionID
	connenv.httpProxyHeaders["sid"] = sessionID
	connenv.httpProxyHeaders["insecure"] = fmt.Sprintf("%v", connenv.insecure)
	connenv.httpProxyHeaders["proxy_base_url"] = proxyBaseURL

	connenv.httpProxyHeaders["dlp_provider"] = connParams.DlpProvider
	connenv.httpProxyHeaders["dlp_mode"] = connParams.DlpMode
	connenv.httpProxyHeaders["dlp_masking_character"] = "#"
	connenv.httpProxyHeaders["mspresidio_anonymizer_url"] = connParams.DlpPresidioAnonymizerURL
	connenv.httpProxyHeaders["mspresidio_analyzer_url"] = connParams.DlpPresidioAnalyzerURL
	connenv.httpProxyHeaders["guard_rail_rules"] = guardRailRules
	connenv.httpProxyHeaders["data_masking_entity_data"] = dataMaskingEntityTypesData

	// add default values for kubernetes type
	if connParams.ConnectionType == pb.ConnectionTypeKubernetes.String() {
		connenv.httpProxyHeaders["remote_url"] = connenv.kubernetesClusterURL
		connenv.httpProxyHeaders["authorization"] = connenv.kubernetesToken
		connenv.httpProxyHeaders["insecure"] = fmt.Sprintf("%v", connenv.kubernetesInsecureSkipVerify)
	}

	httpProxy, err := libhoop.NewHttpProxy(context.Background(), httpStreamClient, connenv.httpProxyHeaders)
	if err != nil {
		log.Infof("failed connecting to %v, err=%v", connenv.host, err)
		a.sendClientSessionClose(sessionID, fmt.Sprintf("failed connecting to internal service, reason=%v", err))
		return
	}

	// write the first packet when establishing the connection
	if _, err := httpProxy.Write(pkt.Payload); err != nil {
		// ErrWebSocketMode is not an error - it signals WebSocket mode was activated
		if isWebSocketModeError(err) {
			log.Infof("websocket mode activated on first request")
			a.connStore.Set(clientConnectionIDKey, httpProxy)
			return
		}
		log.Infof("failed writing first packet, err=%v", err)
		_ = httpProxy.Close()
		// Check if this is a guardrails error - send the actual error message
		if isGuardrailsError(err) {
			log.Infof("guardrails validation failed on first request, closing session: %v", err)
			a.sendClientSessionClose(sessionID, err.Error())
			return
		}
		a.sendClientTCPConnectionClose(sessionID, clientConnectionID)
		return
	}
	a.connStore.Set(clientConnectionIDKey, httpProxy)
}

// isGuardrailsError checks if the error is a guardrails validation failure.
//
// Note: This uses string matching because the error originates from libhoop
// (a separate Go module) and the agent cannot import libhoop's typed errors.
// The error message format is defined in libhoop/redactor/mspresidio/client.go.
//
// When modifying the guardrails error message format in mspresidio/client.go,
// ensure this function is updated accordingly.
func isGuardrailsError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	// Check for the standard guardrails error message format from mspresidio/client.go:
	// "Blocked by the following Hoop Guardrails Rules: <rule_names>"
	return strings.Contains(errStr, "Blocked by the following Hoop Guardrails Rules")
}

// isWebSocketModeError checks if the error is the special ErrWebSocketMode
// which signals that WebSocket mode was activated (not a real error)
func isWebSocketModeError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "websocket mode activated")
}
