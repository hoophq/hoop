package cmd

import (
	"context"
	"fmt"

	clientconfig "github.com/runopsio/hoop/client/config"
	"github.com/runopsio/hoop/client/proxy"
	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
	pbagent "github.com/runopsio/hoop/common/proto/agent"
	pbclient "github.com/runopsio/hoop/common/proto/client"
)

func RunConnectV2(ctx context.Context, connection string, config *clientconfig.Config, onSuccessCallback func()) error {
	defer onSuccessCallback()
	c := newClientConnect(config, nil, []string{connection}, pb.ClientVerbConnect)
	sendOpenSessionPktFn := func() error {
		spec := newClientArgsSpec(c.clientArgs, nil)
		spec[pb.SpecJitTimeout] = []byte(connectFlags.duration)
		if err := c.client.Send(&pb.Packet{
			Type: pbagent.SessionOpen,
			Spec: spec,
		}); err != nil {
			_, _ = c.client.Close()
			return fmt.Errorf("failed opening session with gateway, err=%v", err)
		}
		return nil
	}

	go func() {
		<-ctx.Done()
		for _, obj := range c.connStore.List() {
			if srv, ok := obj.(proxy.Closer); ok {
				srv.Close()
			}
			_, _ = c.client.Close()
		}
	}()

	if err := sendOpenSessionPktFn(); err != nil {
		return err
	}
	for {
		pkt, err := c.client.Recv()
		if err != nil {
			return err
		}
		if pkt == nil {
			continue
		}
		switch pb.PacketType(pkt.Type) {
		case pbclient.SessionOpenWaitingApproval:
			log.Infof("waiting task to be approved at %v", string(pkt.Payload))
		case pbclient.SessionOpenOK:
			sessionID, ok := pkt.Spec[pb.SpecGatewaySessionID]
			if !ok || sessionID == nil {
				return fmt.Errorf("internal error, session not found")
			}
			onSuccessCallback()
			connnectionType := pb.ConnectionType(pkt.Spec[pb.SpecConnectionType])
			switch connnectionType {
			case pb.ConnectionTypePostgres:
				srv := proxy.NewPGServer(c.proxyPort, c.client)
				if err := srv.Serve(string(sessionID)); err != nil {
					return fmt.Errorf("connect - failed initializing postgres proxy, err=%v", err)
				}
				c.client.StartKeepAlive()
				c.connStore.Set(string(sessionID), srv)
			case pb.ConnectionTypeMySQL:
				srv := proxy.NewMySQLServer(c.proxyPort, c.client)
				if err := srv.Serve(string(sessionID)); err != nil {
					return fmt.Errorf("connect - failed initializing mysql proxy, err=%v", err)
				}
				c.client.StartKeepAlive()
				c.connStore.Set(string(sessionID), srv)
			case pb.ConnectionTypeMSSQL:
				srv := proxy.NewMSSQLServer(c.proxyPort, c.client)
				if err := srv.Serve(string(sessionID)); err != nil {
					return fmt.Errorf("connect - failed initializing mssql proxy, err=%v", err)
				}
				c.client.StartKeepAlive()
				c.connStore.Set(string(sessionID), srv)
			case pb.ConnectionTypeMongoDB:
				srv := proxy.NewMongoDBServer(c.proxyPort, c.client)
				if err := srv.Serve(string(sessionID)); err != nil {
					return fmt.Errorf("connect - failed initializing mongo proxy, err=%v", err)
				}
				c.client.StartKeepAlive()
				c.connStore.Set(string(sessionID), srv)
			case pb.ConnectionTypeTCP:
				proxyPort := "8999"
				if c.proxyPort != "" {
					proxyPort = c.proxyPort
				}
				tcp := proxy.NewTCPServer(proxyPort, c.client, pbagent.TCPConnectionWrite)
				if err := tcp.Serve(string(sessionID)); err != nil {
					return fmt.Errorf("connect - failed initializing tcp proxy, err=%v", err)
				}
				c.client.StartKeepAlive()
				c.connStore.Set(string(sessionID), tcp)
			default:
				return fmt.Errorf(`connection type %q not supported`, connnectionType.String())
			}
		case pbclient.SessionOpenApproveOK:
			if err := sendOpenSessionPktFn(); err != nil {
				return sendOpenSessionPktFn()
			}
		case pbclient.SessionOpenAgentOffline:
			return pb.ErrAgentOffline
		case pbclient.SessionOpenTimeout:
			return fmt.Errorf("session ended, reached connection duration (%s)", connectFlags.duration)
		// process terminal
		case pbclient.PGConnectionWrite:
			sessionID := pkt.Spec[pb.SpecGatewaySessionID]
			srvObj := c.connStore.Get(string(sessionID))
			srv, ok := srvObj.(*proxy.PGServer)
			if !ok {
				return fmt.Errorf("unable to obtain proxy client from memory")
			}
			connectionID := string(pkt.Spec[pb.SpecClientConnectionID])
			_, err := srv.PacketWriteClient(connectionID, pkt)
			if err != nil {
				return fmt.Errorf("failed writing to client, err=%v", err)
			}
		case pbclient.MySQLConnectionWrite:
			sessionID := pkt.Spec[pb.SpecGatewaySessionID]
			srvObj := c.connStore.Get(string(sessionID))
			srv, ok := srvObj.(*proxy.MySQLServer)
			if !ok {
				return fmt.Errorf("unable to obtain proxy client from memory")
			}
			connectionID := string(pkt.Spec[pb.SpecClientConnectionID])
			_, err := srv.PacketWriteClient(connectionID, pkt)
			if err != nil {
				return fmt.Errorf("failed writing to client, err=%v", err)
			}
		case pbclient.MSSQLConnectionWrite:
			sessionID := pkt.Spec[pb.SpecGatewaySessionID]
			srvObj := c.connStore.Get(string(sessionID))
			srv, ok := srvObj.(*proxy.MSSQLServer)
			if !ok {
				return fmt.Errorf("unable to obtain proxy client from memory")
			}
			connectionID := string(pkt.Spec[pb.SpecClientConnectionID])
			_, err := srv.PacketWriteClient(connectionID, pkt)
			if err != nil {
				return fmt.Errorf("failed writing to client, err=%v", err)
			}
		case pbclient.MongoDBConnectionWrite:
			sessionID := pkt.Spec[pb.SpecGatewaySessionID]
			srvObj := c.connStore.Get(string(sessionID))
			srv, ok := srvObj.(*proxy.MongoDBServer)
			if !ok {
				return fmt.Errorf("unable to obtain proxy client from memory")
			}
			connectionID := string(pkt.Spec[pb.SpecClientConnectionID])
			_, err := srv.PacketWriteClient(connectionID, pkt)
			if err != nil {
				return fmt.Errorf("failed writing to client, err=%v", err)
			}
		case pbclient.TCPConnectionWrite:
			sessionID := pkt.Spec[pb.SpecGatewaySessionID]
			connectionID := string(pkt.Spec[pb.SpecClientConnectionID])
			if tcp, ok := c.connStore.Get(string(sessionID)).(*proxy.TCPServer); ok {
				_, err := tcp.PacketWriteClient(connectionID, pkt)
				if err != nil {
					return fmt.Errorf("failed writing to client, err=%v", err)
				}
			}
		case pbclient.TCPConnectionClose:
			sessionID := pkt.Spec[pb.SpecGatewaySessionID]
			srvObj := c.connStore.Get(string(sessionID))
			if srv, ok := srvObj.(proxy.Closer); ok {
				srv.CloseTCPConnection(string(pkt.Spec[pb.SpecClientConnectionID]))
			}
		case pbclient.SessionClose:
			sessionID := pkt.Spec[pb.SpecGatewaySessionID]
			if srv, ok := c.connStore.Get(string(sessionID)).(proxy.Closer); ok {
				srv.Close()
			}
		}
	}
}
