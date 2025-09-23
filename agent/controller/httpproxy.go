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

func (a *Agent) processHttpProxyWriteServer(pkt *pb.Packet) {
	sessionID := string(pkt.Spec[pb.SpecGatewaySessionID])
	clientConnectionID := string(pkt.Spec[pb.SpecClientConnectionID])
	log := log.With("sid", sessionID, "conn", clientConnectionID)
	if clientConnectionID == "" {
		log.Info("connection not found in packet specfication")
		a.sendClientSessionClose(sessionID, "http proxy connection id not found")
		return
	}
	connParams := a.connectionParams(sessionID)
	if connParams == nil {
		log.Infof("connection params not found")
		a.sendClientSessionClose(sessionID, "connection params not found, contact the administrator")
		return
	}
	clientConnectionIDKey := fmt.Sprintf("%s:%s", sessionID, string(clientConnectionID))
	if httpServer, ok := a.connStore.Get(clientConnectionIDKey).(io.WriteCloser); ok {
		if _, err := httpServer.Write(pkt.Payload); err != nil {
			log.Infof("failed writing packet, err=%v", err)
			_ = httpServer.Close()
			a.sendClientSessionClose(sessionID, fmt.Sprintf("failed writing to http proxy connection, reason=%v", err))
		}
		return
	}
	httpStreamClient := pb.NewStreamWriter(a.client, pbclient.HttpProxyConnectionWrite, pkt.Spec)
	connenv, err := parseConnectionEnvVars(connParams.EnvVars, pb.ConnectionTypeHttpProxy)
	if err != nil {
		log.Infof("missing connection credentials in memory, err=%v", err)
		a.sendClientSessionClose(sessionID, "credentials are empty, contact the administrator")
		return
	}

	log.Infof("starting http proxy connection at %v", connenv.httpProxyRemoteURL)

	connenv.httpProxyHeaders["remote_url"] = connenv.httpProxyRemoteURL
	connenv.httpProxyHeaders["connection_id"] = clientConnectionID
	connenv.httpProxyHeaders["sid"] = sessionID
	connenv.httpProxyHeaders["insecure"] = fmt.Sprintf("%v", connenv.insecure)
	httpProxy, err := libhoop.NewHttpProxy(context.Background(), httpStreamClient, connenv.httpProxyHeaders)
	if err != nil {
		log.Infof("failed connecting to %v, err=%v", sessionID, connenv.host, err)
		a.sendClientSessionClose(sessionID, fmt.Sprintf("failed connecting to internal service, reason=%v", err))
		return
	}
	// write the first packet when establishing the connection
	_, _ = httpProxy.Write(pkt.Payload)
	a.connStore.Set(clientConnectionIDKey, httpProxy)
}
