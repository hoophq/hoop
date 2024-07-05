package controller

import (
	"fmt"
	"io"

	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	pbclient "github.com/hoophq/hoop/common/proto/client"
)

func (a *Agent) processTCPWriteServer(pkt *pb.Packet) {
	sessionID := string(pkt.Spec[pb.SpecGatewaySessionID])
	clientConnectionID := string(pkt.Spec[pb.SpecClientConnectionID])
	if clientConnectionID == "" {
		log.Println("connection not found in packet specfication")
		a.sendClientSessionClose(sessionID, "tcp connection id not found")
		return
	}
	connParams := a.connectionParams(sessionID)
	if connParams == nil {
		log.Printf("session=%s - connection params not found", sessionID)
		a.sendClientSessionClose(sessionID, "connection params not found, contact the administrator")
		return
	}
	clientConnectionIDKey := fmt.Sprintf("%s:%s", sessionID, string(clientConnectionID))
	if tcpServer, ok := a.connStore.Get(clientConnectionIDKey).(io.WriteCloser); ok {
		if _, err := tcpServer.Write(pkt.Payload); err != nil {
			log.Printf("session=%v - failed writing first packet, err=%v", sessionID, err)
			_ = tcpServer.Close()
			a.sendClientSessionClose(sessionID, fmt.Sprintf("failed writing to tcp connection, reason=%v", err))
		}
		return
	}
	tcpClient := pb.NewStreamWriter(a.client, pbclient.TCPConnectionWrite, pkt.Spec)
	connenv, err := parseConnectionEnvVars(connParams.EnvVars, pb.ConnectionTypeTCP)
	if err != nil {
		log.Printf("session=%s - missing connection credentials in memory, err=%v", sessionID, err)
		a.sendClientSessionClose(sessionID, "credentials are empty, contact the administrator")
		return
	}
	tcpServer, err := newTCPConn(connenv)
	if err != nil {
		log.Printf("session=%s - failed connecting to %v, err=%v", sessionID, connenv.host, err)
		a.sendClientSessionClose(sessionID, fmt.Sprintf("failed connecting to internal service, reason=%v", err))
		return
	}
	a.connStore.Set(clientConnectionIDKey, tcpServer)
	go func() {
		defer a.connStore.Del(clientConnectionIDKey)
		// the connect key is a noop packet. It's useful when
		// a client needs the response of the server first to initate
		// the protocol negotiation. e.g.: mysql
		if _, ok := pkt.Spec[pb.SpecTCPServerConnectKey]; !ok {
			if _, err := tcpServer.Write(pkt.Payload); err != nil {
				log.Error("session=%v - failed writing first packet, err=%v", sessionID, err)
				a.sendClientTCPConnectionClose(sessionID, string(clientConnectionID))
				return
			}
		}
		if _, err := io.Copy(tcpClient, tcpServer); err != nil {
			if err != io.EOF {
				log.Infof("session=%v, done copying tcp connection, reason=%v", sessionID, err)
			}
			a.sendClientTCPConnectionClose(sessionID, string(clientConnectionID))
		}
	}()
}
