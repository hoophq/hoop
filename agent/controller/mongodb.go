package controller

import (
	"context"
	"fmt"

	"github.com/runopsio/hoop/agent/mongoproxy"
	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
	pbclient "github.com/runopsio/hoop/common/proto/client"
	"go.mongodb.org/mongo-driver/x/mongo/driver/connstring"
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

	log.With("sid", sid, "conn", clientConnectionID, "legacy", connenv.connectionString == "").
		Infof("starting mongodb connection at %v", connenv.Address())
	mongodbSrv, err := newTCPConn(connenv)
	if err != nil {
		errMsg := fmt.Sprintf("failed connecting with mongodb server, err=%v", err)
		log.Errorf(errMsg)
		a.sendClientSessionClose(sid, errMsg)
		return
	}
	connString := &connstring.ConnString{
		// [<host:port>, ...]
		Hosts:      []string{connenv.Address()},
		SSL:        connenv.Get("tls") == "true",
		Username:   connenv.user,
		Password:   connenv.pass,
		Database:   connenv.dbname,
		AuthSource: connenv.Get("authSource"),
	}
	if connenv.connectionString != "" {
		connString, err = connstring.ParseAndValidate(connenv.connectionString)
		if err != nil {
			log.Warnf("failed parsing mongodb connection string, reason=%v", err)
			a.sendClientSessionClose(sid, "internal error, failed parsing mongodb connection string")
			return
		}
	}
	if connString == nil {
		log.Warnf("mongodb connection string is empty")
		a.sendClientSessionClose(sid, "internal error, mongodb connection string is empty")
		return
	}
	if !connString.SSL {
		log.Warn(nonTLSMsg)
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
