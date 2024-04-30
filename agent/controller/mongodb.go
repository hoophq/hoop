package controller

import (
	"context"
	"fmt"
	"net/url"

	"github.com/runopsio/hoop/agent/mongoproxy"
	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
	pbclient "github.com/runopsio/hoop/common/proto/client"
)

const nonTLSMsg = `the connection with the remote server is not encrypted, the connection is subject to eavesdropping. Make sure the network is reliable `

func (a *Agent) processMongoDBProtocol(pkt *pb.Packet) {
	sid := string(pkt.Spec[pb.SpecGatewaySessionID])
	streamClient := pb.NewStreamWriter(a.client, pbclient.MongoDBConnectionWrite, pkt.Spec)
	connParams, _ := a.connectionParams(sid)
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
	if serverWriter, ok := clientObj.(mongoproxy.Proxy); ok {
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

	log.With("sid", sid, "conn", clientConnectionID).Infof("starting mongodb connection at %v:%v", connenv.host, connenv.port)
	mongodbSrv, err := newTCPConn(connenv.host, connenv.port)
	if err != nil {
		errMsg := fmt.Sprintf("failed connecting with mongodb server, err=%v", err)
		log.Errorf(errMsg)
		a.sendClientSessionClose(sid, errMsg)
		return
	}
	connString, _ := url.Parse(fmt.Sprintf("mongodb://%s:%s@%s:%v/%v?tls=%v",
		connenv.user, connenv.pass, connenv.host, connenv.port, connenv.dbname, connenv.tls))
	if !connenv.tls {
		log.Warn(nonTLSMsg)
	}
	if connString == nil {
		log.Error("mongodb connection string is empty")
		a.sendClientSessionClose(sid, "internal error, mongodb connection string is empty")
		return
	}

	ctx := context.WithValue(context.Background(), mongoproxy.ConnIDContextKey, clientConnectionID)
	serverWriter := mongoproxy.New(ctx, connString, mongodbSrv, streamClient)
	serverWriter.Run(func(errMsg string) {
		a.sendClientSessionClose(sid, errMsg)
	})
	// write the first packet when establishing the connection
	_, _ = serverWriter.Write(pkt.Payload)
	a.connStore.Set(clientConnectionIDKey, serverWriter)
}
