package controller

import (
	"context"
	"fmt"

	"github.com/hoophq/pluginhooks"
	"github.com/runopsio/hoop/agent/mysql"
	authmiddleware "github.com/runopsio/hoop/agent/mysql/middleware/auth"
	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
	pbclient "github.com/runopsio/hoop/common/proto/client"
)

func (a *Agent) processMySQLProtocol(pkt *pb.Packet) {
	sessionID := string(pkt.Spec[pb.SpecGatewaySessionID])
	streamClient := pb.NewStreamWriter(a.client, pbclient.MySQLConnectionWrite, pkt.Spec)
	connParams, pluginHooks := a.connectionParams(sessionID)
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
	if proxyServerWriter, ok := clientObj.(mysql.Proxy); ok {
		mutatePayload, err := pluginHooks.ExecRPCOnRecv(&pluginhooks.Request{
			SessionID:  sessionID,
			PacketType: pkt.Type,
			Payload:    pkt.Payload,
		})
		if err != nil {
			log.Errorf("failed processing plugin, err=%v", err)
			a.sendClientSessionClose(sessionID, fmt.Sprintf("plugin error, err=%v", err))
			return
		}
		if len(mutatePayload) > 0 {
			pkt.Payload = mutatePayload
		}
		if _, err := proxyServerWriter.Write(pkt.Payload); err != nil {
			log.Errorf("failed sending packet, err=%v", err)
			a.sendClientSessionClose(sessionID, "fail to write packet")
			_ = proxyServerWriter.Close()
		}
		return
	}

	connenv, err := parseConnectionEnvVars(connParams.EnvVars, pb.ConnectionTypeMySQL)
	if err != nil {
		log.Error("mysql credentials not found in memory, err=%v", err)
		a.sendClientSessionClose(sessionID, "credentials are empty, contact the administrator")
		return
	}

	log.Infof("session=%v - starting mysql connection at %v:%v", sessionID, connenv.host, connenv.port)
	mysqlServer, err := newTCPConn(connenv)
	if err != nil {
		errMsg := fmt.Sprintf("failed connecting with mysql server, err=%v", err)
		log.Errorf(errMsg)
		a.sendClientSessionClose(sessionID, errMsg)
		return
	}
	proxyServerWriter := mysql.NewProxy(
		context.Background(),
		mysqlServer,
		streamClient,
		authmiddleware.New(connenv.user, connenv.pass).Handler,
	).Run()
	a.connStore.Set(clientConnectionIDKey, proxyServerWriter)
}
