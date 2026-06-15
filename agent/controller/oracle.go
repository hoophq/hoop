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

func (a *Agent) processOracleProtocol(pkt *pb.Packet) {
	sessionID := string(pkt.Spec[pb.SpecGatewaySessionID])
	streamClient := pb.NewStreamWriter(a.client, pbclient.OracleConnectionWrite, pkt.Spec)
	connParams := a.connectionParams(sessionID)
	if connParams == nil {
		log.Errorf("session=%s - connection params not found", sessionID)
		a.sendClientSessionClose(sessionID, "connection params not found, contact the administrator")
		return
	}

	clientConnectionID := string(pkt.Spec[pb.SpecClientConnectionID])
	if clientConnectionID == "" {
		log.Println("connection id not found in memory")
		a.sendClientSessionClose(sessionID, "connection id not found, contact the administrator")
		return
	}

	clientConnectionIDKey := fmt.Sprintf("%s:%s", sessionID, clientConnectionID)
	clientObj := a.connStore.Get(clientConnectionIDKey)
	if serverWriter, ok := clientObj.(io.WriteCloser); ok {
		if _, err := serverWriter.Write(pkt.Payload); err != nil {
			log.Errorf("failed sending packet, err=%v", err)
			a.sendClientSessionClose(sessionID, "fail to write packet")
			_ = serverWriter.Close()
		}
		return
	}

	connenv, err := parseConnectionEnvVars(connParams.EnvVars, pb.ConnectionTypeOracleDB)
	if err != nil {
		log.Errorf("oracle credentials not found in memory, err=%v", err)
		a.sendClientSessionClose(sessionID, "credentials are empty, contact the administrator")
		return
	}

	log.Infof("session=%v - starting oracle connection at %v:%v", sessionID, connenv.host, connenv.port)

	var dataMaskingEntityTypesData string
	if connParams.DataMaskingEntityTypesData != nil {
		dataMaskingEntityTypesData = string(connParams.DataMaskingEntityTypesData)
	}
	var guardRailRules string
	if connParams.GuardRailRules != nil {
		guardRailRules = string(connParams.GuardRailRules)
	}
	var analyzerMetricsRules string
	if connParams.AnalyzerMetricsRules != nil {
		analyzerMetricsRules = string(connParams.AnalyzerMetricsRules)
	}

	opts := map[string]string{
		"sid":                       sessionID,
		"hostname":                  connenv.host,
		"port":                      connenv.port,
		"username":                  connenv.user,
		"password":                  connenv.pass,
		// The hoop client always presents the local Oracle proxy with the fixed
		// placeholder noop/noop (see client/cmd/connect.go). Oracle auth is
		// mutual, so the proxy needs the placeholder password to re-key the
		// handshake and keep the server's response verifiable by real OCI
		// clients (sqlplus).
		"client_password":           "noop",
		"service_name":              connenv.serviceName,
		"dlp_provider":              connParams.DlpProvider,
		"dlp_mode":                  connParams.DlpMode,
		"mspresidio_analyzer_url":   connParams.DlpPresidioAnalyzerURL,
		"mspresidio_anonymizer_url": connParams.DlpPresidioAnonymizerURL,
		"dlp_gcp_credentials":       connParams.DlpGcpRawCredentialsJSON,
		"dlp_info_types":            strings.Join(connParams.DLPInfoTypes, ","),
		"dlp_masking_character":     "#",
		"data_masking_entity_data":  dataMaskingEntityTypesData,
		"guard_rail_rules":          guardRailRules,
		"analyzer_metrics_rules":    analyzerMetricsRules,
	}

	serverWriter, err := libhoop.NewDBCore(context.Background(), streamClient, opts).Oracle()
	if err != nil {
		errMsg := fmt.Sprintf("failed connecting with oracle server, err=%v", err)
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
