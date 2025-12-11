package controller

import (
	"context"
	"fmt"
	"io"
	"libhoop"

	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	pbclient "github.com/hoophq/hoop/common/proto/client"
)

func (a *Agent) processMySQLProtocol(pkt *pb.Packet) {
	sid := string(pkt.Spec[pb.SpecGatewaySessionID])
	streamClient := pb.NewStreamWriter(a.client, pbclient.MySQLConnectionWrite, pkt.Spec)
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
	clientConnectionIDKey := fmt.Sprintf("%s:%s", sid, string(clientConnectionID))
	clientObj := a.connStore.Get(clientConnectionIDKey)
	if proxyServerWriter, ok := clientObj.(io.WriteCloser); ok {
		if _, err := proxyServerWriter.Write(pkt.Payload); err != nil {
			log.With("sid", sid).Errorf("failed sending packet, err=%v", err)
			a.sendClientSessionClose(sid, "fail to write packet")
			_ = proxyServerWriter.Close()
		}
		return
	}

	connenv, err := parseConnectionEnvVars(connParams.EnvVars, pb.ConnectionTypeMySQL)
	if err != nil {
		log.With("sid", sid).Error("mysql credentials not found in memory, err=%v", err)
		a.sendClientSessionClose(sid, "credentials are empty, contact the administrator")
		return
	}

	log.With("sid", sid).Infof("starting mysql connection at %v:%v", connenv.host, connenv.port)
	var dataMaskingEntityTypesData string
	if connParams.DataMaskingEntityTypesData != nil {
		dataMaskingEntityTypesData = string(connParams.DataMaskingEntityTypesData)
	}

	var analyzerMetricsRules string
	if connParams.AnalyzerMetricsRules != nil {
		analyzerMetricsRules = string(connParams.AnalyzerMetricsRules)
	}
	opts := map[string]string{
		"sid":                       sid,
		"hostname":                  connenv.host,
		"port":                      connenv.port,
		"username":                  connenv.user,
		"password":                  connenv.pass,
		"connection_id":             clientConnectionID,
		"dlp_provider":              connParams.DlpProvider,
		"dlp_mode":                  connParams.DlpMode,
		"mspresidio_analyzer_url":   connParams.DlpPresidioAnalyzerURL,
		"mspresidio_anonymizer_url": connParams.DlpPresidioAnonymizerURL,
		"data_masking_entity_data":  dataMaskingEntityTypesData,
		"analyzer_metrics_rules":    analyzerMetricsRules,
		// #TODO: (chico) we are connecting with insecure true. To not introduce breaking changes, we will keep it true for now.
		// Later we should add this to connection params and set it accordingly.
		// and we need pass the CA cert too in case of secure connection, to verify server cert and do the tls upgrade.
		"insecure": "true",
	}
	serverWriter, err := libhoop.NewDBCore(context.Background(), streamClient, opts).MySQL()
	if err != nil {
		errMsg := fmt.Sprintf("failed connecting with mysql server, err=%v", err)
		log.Errorf(errMsg)
		a.sendClientSessionClose(sid, errMsg)
		return
	}
	serverWriter.Run(func(_ int, errMsg string) {
		a.sendClientSessionClose(sid, errMsg)
	})
	a.connStore.Set(clientConnectionIDKey, serverWriter)
}
