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

func (a *Agent) processMongoDBProtocol(pkt *pb.Packet) {
	sid := string(pkt.Spec[pb.SpecGatewaySessionID])
	streamClient := pb.NewStreamWriter(a.client, pbclient.MongoDBConnectionWrite, pkt.Spec)
	connParams := a.connectionParams(sid)
	if connParams == nil {
		log.With("sid", sid).Errorf("connection params not found")
		a.sendClientSessionClose(sid, "connection params not found, contact the administrator")
		return
	}

	clientConnectionID := string(pkt.Spec[pb.SpecClientConnectionID])
	if clientConnectionID == "" && pkt.Payload != nil {
		log.Errorf("connection id not found in memory")
		a.sendClientSessionClose(sid, "connection id not found, contact the administrator")
		return
	}
	clientConnectionIDKey := fmt.Sprintf("%s:%s", sid, string(clientConnectionID))
	clientObj := a.connStore.Get(clientConnectionIDKey)
	if serverWriter, ok := clientObj.(io.WriteCloser); ok {
		if _, err := serverWriter.Write(pkt.Payload); err != nil {
			log.With("sid", sid).Errorf("failed sending packet, err=%v", err)
			a.sendClientSessionClose(sid, "fail to write packet")
			_ = serverWriter.Close()
		}
		return
	}

	connenv, err := parseConnectionEnvVars(connParams.EnvVars, pb.ConnectionTypeMongoDB)
	if err != nil {
		log.With("sid", sid).Error("mongodb credentials not found in memory, err=%v", err)
		a.sendClientSessionClose(sid, "credentials are empty, contact the administrator")
		return
	}

	log.With("sid", sid, "conn", clientConnectionID, "legacy", connenv.connectionString == "").
		Infof("starting mongodb connection at %v", connenv.Address())

	opts := map[string]string{
		"sid":               sid,
		"connection_string": connenv.connectionString,
		"connection_id":     clientConnectionID,
		// Not Implemented yet
		// "dlp_provider":        connParams.DlpProvider,
		// "mspresidio_analyzer_url":   connParams.DlpPresidioAnalyzerURL,
		// "mspresidio_anonymizer_url": connParams.DlpPresidioAnonymizerURL,
		"dlp_mode":              connParams.DlpMode,
		"dlp_gcp_credentials":   connParams.DlpGcpRawCredentialsJSON,
		"dlp_info_types":        strings.Join(connParams.DLPInfoTypes, ","),
		"dlp_masking_character": "#",
	}
	serverWriter, err := libhoop.NewDBCore(context.Background(), streamClient, opts).MongoDB()
	if err != nil {
		errMsg := fmt.Sprintf("failed connecting with mongodb server, err=%v", err)
		log.Errorf(errMsg)
		a.sendClientSessionClose(sid, errMsg)
		return
	}
	serverWriter.Run(func(_ int, errMsg string) {
		a.sendClientSessionClose(sid, errMsg)
	})
	// write the first packet when establishing the connection
	_, _ = serverWriter.Write(pkt.Payload)
	a.connStore.Set(clientConnectionIDKey, serverWriter)
}
