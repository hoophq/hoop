package controller

import (
	"context"
	"fmt"
	"net/url"

	"github.com/runopsio/hoop/agent/mssql"
	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
	pbclient "github.com/runopsio/hoop/common/proto/client"
)

const insecureSkipVerifyMsg = `the connection with the remote host will accept any certificate presented by the server, the connection is subject to man in the middle attacks if the network is not reliable.`

func (a *Agent) processMSSQLProtocol(pkt *pb.Packet) {
	sessionID := string(pkt.Spec[pb.SpecGatewaySessionID])
	streamClient := pb.NewStreamWriter(a.client, pbclient.MSSQLConnectionWrite, pkt.Spec)
	connParams, _ := a.connectionParams(sessionID)
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
	if serverWriter, ok := clientObj.(mssql.Proxy); ok {
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
	mssqlServer, err := newTCPConn(connenv.host, connenv.port)
	if err != nil {
		errMsg := fmt.Sprintf("failed connecting with mssql server, err=%v", err)
		log.Errorf(errMsg)
		a.sendClientSessionClose(sessionID, errMsg)
		return
	}
	connString, _ := url.Parse(fmt.Sprintf("sqlserver://%s:%s@%s:%v?insecure=%v",
		connenv.user, connenv.pass, connenv.host, connenv.port, connenv.insecure))
	if connenv.insecure {
		log.Warn(insecureSkipVerifyMsg)
	}
	if connString == nil {
		log.Error("mssql connection string is empty")
		a.sendClientSessionClose(sessionID, "internal error, mssql connection string is empty")
		return
	}

	serverWriter := mssql.NewProxy(context.Background(), connString, mssqlServer, streamClient)
	serverWriter.Run(func(errMsg string) {
		a.sendClientSessionClose(sessionID, errMsg)
	})
	// write the first packet when establishing the connection
	_, _ = serverWriter.Write(pkt.Payload)
	a.connStore.Set(clientConnectionIDKey, serverWriter)
}
