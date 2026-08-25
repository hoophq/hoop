package controller

import (
	"context"
	"fmt"
	"io"
	"github.com/hoophq/libhoop/v2"

	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	pbclient "github.com/hoophq/hoop/common/proto/client"
)

func (a *Agent) processMSSQLProtocol(pkt *pb.Packet) {
	sessionID := string(pkt.Spec[pb.SpecGatewaySessionID])
	streamClient := pb.NewStreamWriter(a.client, pbclient.MSSQLConnectionWrite, pkt.Spec)
	connParams := a.connectionParams(sessionID)
	if connParams == nil {
		log.Errorf("session=%s - connection params not found", sessionID)
		a.sendClientSessionClose(sessionID, "connection params not found, contact the administrator")
		return
	}

	clientConnectionID := string(pkt.Spec[pb.SpecClientConnectionID])
	if clientConnectionID == "" && pkt.Payload != nil {
		log.Errorf("connection id not found in memory")
		a.sendClientSessionClose(sessionID, "connection id not found, contact the administrator")
		return
	}
	clientConnectionIDKey := fmt.Sprintf("%s:%s", sessionID, string(clientConnectionID))
	clientObj := a.connStore.Get(clientConnectionIDKey)
	if serverWriter, ok := clientObj.(io.WriteCloser); ok {
		if _, err := serverWriter.Write(pkt.Payload); err != nil {
			log.Errorf("failed sending packet, err=%v", err)
			a.sendClientSessionClose(sessionID, "fail to write packet")
			_ = serverWriter.Close()
		}
		return
	}

	connenv, err := parseConnectionEnvVars(connParams.EnvVars, pb.ConnectionTypeMSSQL)
	if err != nil {
		log.Error("mssql credentials not found in memory, err=%v", err)
		a.sendClientSessionClose(sessionID, "credentials are empty, contact the administrator")
		return
	}

	log.Infof("session=%v - starting mssql connection at %v:%v", sessionID, connenv.host, connenv.port)
	opts := map[string]string{
		"sid":      sessionID,
		"hostname": connenv.host,
		"port":     connenv.port,
		"username": connenv.user,
		"password": connenv.pass,
		"insecure": fmt.Sprintf("%v", connenv.insecure),
	}
	addMSSQLGuardRailsOpts(opts, connParams)
	serverWriter, err := libhoop.NewDBCore(context.Background(), streamClient, opts).MSSQL()
	if err != nil {
		errMsg := fmt.Sprintf("failed connecting with mssql server, err=%v", err)
		log.Errorf(errMsg)
		a.sendClientSessionClose(sessionID, errMsg)
		return
	}
	serverWriter.Run(func(_ int, errMsg string) {
		a.sendClientSessionClose(sessionID, errMsg)
	})
	// write the first packet when establishing the connection
	_, _ = serverWriter.Write(pkt.Payload)
	a.connStore.Set(clientConnectionIDKey, serverWriter)
}

// addMSSQLGuardRailsOpts threads the guardrail/DLP options into the native
// MSSQL proxy options. Unlike addGuardRailsOpts (used by SSH and mirrored by
// the masking-capable database proxies), it forwards ONLY the keys libhoop
// needs to select the guardrail engine: with provider "mspresidio" and both
// Presidio URLs set, libhoop evaluates input rules through Presidio (full PCRE,
// e.g. lookahead); otherwise it uses the built-in local engine (substring
// deny-words + Go RE2), which cannot compile PCRE-only patterns. Before this,
// MSSQL forwarded none of these and was permanently stuck on the local engine,
// unlike every other database proxy (DEP-89 / DEP-90 / #1615).
//
// The masking/GCP keys (dlp_gcp_credentials, dlp_info_types, data-masking
// entity data, masking character) are deliberately omitted: the native MSSQL
// proxy enforces INPUT guardrails only and never redacts output, so they are
// inert here — and forwarding active GCP redaction config alongside guardrails
// makes libhoop fail closed ("guardrails require MSPresidio"). Omitting them
// keeps GCP-DLP orgs on the local guardrail engine instead of refusing the
// session. An empty guard_rail_rules value is a plain passthrough.
func addMSSQLGuardRailsOpts(opts map[string]string, connParams *pb.AgentConnectionParams) {
	var guardRailRules string
	if connParams.GuardRailRules != nil {
		guardRailRules = string(connParams.GuardRailRules)
	}
	opts["dlp_provider"] = connParams.DlpProvider
	opts["dlp_mode"] = connParams.DlpMode
	opts["mspresidio_analyzer_url"] = connParams.DlpPresidioAnalyzerURL
	opts["mspresidio_anonymizer_url"] = connParams.DlpPresidioAnonymizerURL
	opts["guard_rail_rules"] = guardRailRules
}
