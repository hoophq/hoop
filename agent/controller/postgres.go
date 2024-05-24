package controller

import (
	"context"
	"fmt"
	"io"

	"github.com/runopsio/hoop/agent/dlp"
	"github.com/runopsio/hoop/agent/pgproxy"
	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
	pbclient "github.com/runopsio/hoop/common/proto/client"
	"github.com/xo/dburl"
)

func (a *Agent) processPGProtocol(pkt *pb.Packet) {
	sessionID := string(pkt.Spec[pb.SpecGatewaySessionID])
	streamClient := pb.NewStreamWriter(a.client, pbclient.PGConnectionWrite, pkt.Spec)
	connParams, _ := a.connectionParams(sessionID)
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

	connenv, err := parseConnectionEnvVars(connParams.EnvVars, pb.ConnectionTypePostgres)
	if err != nil {
		log.Error("postgres credentials not found in memory, err=%v", err)
		a.sendClientSessionClose(sessionID, "credentials are empty, contact the administrator")
		return
	}

	log.Infof("session=%v - starting postgres connection at %v:%v", sessionID, connenv.host, connenv.port)
	pgServer, err := newTCPConn(connenv)
	if err != nil {
		errMsg := fmt.Sprintf("failed connecting with postgres server, err=%v", err)
		log.Errorf(errMsg)
		a.sendClientSessionClose(sessionID, errMsg)
		return
	}
	connString, _ := dburl.Parse(fmt.Sprintf("postgres://%s:%s@%s:%v?sslmode=%s",
		connenv.user, connenv.pass, connenv.host, connenv.port, connenv.postgresSSLMode))
	if connString == nil {
		log.Error("postgres connection string is empty")
		a.sendClientSessionClose(sessionID, "internal error, postgres connection string is empty")
		return
	}

	serverWriter := pgproxy.New(context.Background(), connString, pgServer, streamClient)
	if dlpc, ok := a.connStore.Get(dlpClientKey).(dlp.Client); ok {
		serverWriter.WithDataLossPrevention(dlpc, connParams.DLPInfoTypes)
	}
	serverWriter.Run(func(errMsg string) {
		a.sendClientSessionClose(sessionID, errMsg)
	})
	// write the first packet when establishing the connection
	_, _ = serverWriter.Write(pkt.Payload)
	a.connStore.Set(clientConnectionIDKey, serverWriter)
}
