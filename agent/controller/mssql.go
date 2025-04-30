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
