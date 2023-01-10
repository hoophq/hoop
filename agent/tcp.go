package agent

import (
	"fmt"
	"io"
	"log"

	"github.com/hoophq/pluginhooks"
	pb "github.com/runopsio/hoop/common/proto"
	pbclient "github.com/runopsio/hoop/common/proto/client"
)

func (a *Agent) processTCPWriteServer(pkt *pb.Packet) {
	sessionID := string(pkt.Spec[pb.SpecGatewaySessionID])
	clientConnectionID := string(pkt.Spec[pb.SpecClientConnectionID])
	if clientConnectionID == "" {
		log.Println("connection id not found in memory")
		return
	}
	connParams, pluginHooks := a.connectionParams(sessionID)
	if connParams == nil {
		log.Printf("session=%s - connection params not found", sessionID)
		return
	}
	clientConnectionIDKey := fmt.Sprintf("%s:%s", sessionID, string(clientConnectionID))
	if tcpServer, ok := a.connStore.Get(clientConnectionIDKey).(io.WriteCloser); ok {
		mutatePayload, err := pluginHooks.ExecRPCOnRecv(&pluginhooks.Request{
			SessionID:  sessionID,
			PacketType: pkt.Type,
			Payload:    pkt.Payload,
		})
		if err != nil {
			_ = a.client.Send(&pb.Packet{
				Type:    pbclient.SessionClose,
				Payload: []byte(err.Error()),
				Spec:    map[string][]byte{pb.SpecGatewaySessionID: []byte(sessionID)},
			})
			return
		}
		if len(mutatePayload) > 0 {
			pkt.Payload = mutatePayload
		}
		if _, err := tcpServer.Write(pkt.Payload); err != nil {
			log.Printf("session=%v - failed writing first packet, err=%v", sessionID, err)
			_ = tcpServer.Close()
		}
		return
	}
	tcpClient := pb.NewHookStreamWriter(a.client, pbclient.TCPConnectionWrite, map[string][]byte{
		pb.SpecGatewaySessionID:   []byte(sessionID),
		pb.SpecClientConnectionID: []byte(clientConnectionID),
	}, pluginHooks)
	connenv, _ := connParams.EnvVars[connEnvKey].(*connEnv)
	if connenv == nil {
		log.Printf("session=%s - missing connection credentials in memory", sessionID)
		return
	}
	tcpServer, err := newTCPConn(connenv.host, connenv.port)
	if err != nil {
		log.Printf("session=%s - failed connecting to %v, err=%v", sessionID, connenv.host, err)
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
				log.Printf("session=%v - failed writing first packet, err=%v", sessionID, err)
				return
			}
		}
		if _, err := io.Copy(tcpClient, tcpServer); err != nil {
			if err != io.EOF {
				log.Printf("session=%v, done copying tcp connection", sessionID)
			}
		}
	}()
}
