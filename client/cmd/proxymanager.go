package cmd

import (
	"fmt"
	"io"

	"github.com/runopsio/hoop/client/backoff"
	clientconfig "github.com/runopsio/hoop/client/config"
	"github.com/runopsio/hoop/client/proxy"
	"github.com/runopsio/hoop/common/grpc"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/memory"
	pb "github.com/runopsio/hoop/common/proto"
	pbagent "github.com/runopsio/hoop/common/proto/agent"
	pbclient "github.com/runopsio/hoop/common/proto/client"
	pbgateway "github.com/runopsio/hoop/common/proto/gateway"
	"github.com/spf13/cobra"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	proxyManagerCmd = &cobra.Command{
		Use:          "proxy-manager",
		Short:        "Proxy manager controls how to expose ports to underline operating system",
		Hidden:       true,
		SilenceUsage: false,
		Run: func(cmd *cobra.Command, args []string) {
			grpcClientOptions := []*grpc.ClientOptions{
				grpc.WithOption("origin", pb.ConnectionOriginClientProxyManager),
				grpc.WithOption("verb", pb.ClientVerbConnect),
			}

			var client pb.ClientTransport
			err := backoff.Exponential2x(func() error {
				config, err := clientconfig.GetClientConfig()
				if err != nil {
					return err
				}
				clientConfig, err := config.GrpcClientConfig()
				if err != nil {
					return err
				}
				client, err = grpc.Connect(clientConfig, grpcClientOptions...)
				if err != nil {
					return backoff.Errorf("failed connecting to gateway, reasons=%v", err)
				}
				defer client.Close()
				err = runAutoConnect(client)
				if status, ok := status.FromError(err); ok {
					switch status.Code() {
					case codes.Canceled:
						log.Infof("grpc client connection canceled")
						return nil
					case codes.NotFound:
						log.Infof("connection not found")
						return nil
					}
				}
				if err != nil {
					return backoff.Errorf("failed processing proxy manager, reasons=%v", err)
				}
				return nil
			})
			if err != nil {
				log.Fatal(err)
			}
		},
	}
)

func init() { rootCmd.AddCommand(proxyManagerCmd) }

func runAutoConnect(client pb.ClientTransport) (err error) {
	connStore := memory.New()
	var pkt *pb.Packet
	for {
		pkt, err = client.Recv()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if pkt == nil {
			continue
		}
		var sid string
		if specSid, ok := pkt.Spec[pb.SpecGatewaySessionID]; ok {
			sid = string(specSid)
		}

		switch pb.PacketType(pkt.Type) {
		case pbclient.ProxyManagerConnectOK:
			log.Infof("received connect response from gateway, waiting for connection")
			_ = client.Send(&pb.Packet{Type: pbgateway.ProxyManagerConnectOKAck})
		case pbclient.SessionOpenWaitingApproval:
			log.With("sid", sid).Infof("waiting for approval %v", string(pkt.Payload))
			// TODO: review flow
		case pbclient.SessionOpenOK:
			proxyPort := string(pkt.Spec[pb.SpecClientRequestPort])
			connnectionType := string(pkt.Spec[pb.SpecConnectionType])
			if sid == "" {
				return fmt.Errorf("session is empty")
			}
			log.With("sid", sid, "type", connnectionType).Infof("session opened")
			switch connnectionType {
			case pb.ConnectionTypePostgres:
				srv := proxy.NewPGServer(proxyPort, client)
				if err := srv.Serve(sid); err != nil {
					return err
				}
				defer srv.PacketCloseConnection("")
				client.StartKeepAlive()
				connStore.Set(sid, srv)
				log.With("sid", sid, "type", connnectionType, "port", proxyPort).
					Infof("ready to accept connections")
			case pb.ConnectionTypeMySQL:
				srv := proxy.NewMySQLServer(proxyPort, client)
				if err := srv.Serve(sid); err != nil {
					return err
				}
				defer srv.PacketCloseConnection("")
				client.StartKeepAlive()
				connStore.Set(sid, srv)
				log.With("sid", sid, "type", connnectionType, "port", proxyPort).
					Infof("ready to accept connections")
			case pb.ConnectionTypeTCP:
				srv := proxy.NewTCPServer(proxyPort, client, pbagent.TCPConnectionWrite)
				if err := srv.Serve(sid); err != nil {
					return err
				}
				defer srv.PacketCloseConnection("")
				client.StartKeepAlive()
				connStore.Set(sid, srv)
				log.With("sid", sid, "type", connnectionType, "port", proxyPort).
					Infof("ready to accept connections")
			default:
				return fmt.Errorf(`connection type %q not implemented`, string(connnectionType))
			}
		case pbclient.SessionOpenApproveOK:
			log.With("sid", sid).Infof("session approved")
		case pbclient.SessionOpenAgentOffline:
			return fmt.Errorf("agent is offline")
		case pbclient.SessionOpenTimeout:
			return fmt.Errorf("session ended, reached connection duration")
		case pbclient.PGConnectionWrite:
			sessionID := pkt.Spec[pb.SpecGatewaySessionID]
			srvObj := connStore.Get(string(sessionID))
			srv, ok := srvObj.(*proxy.PGServer)
			if !ok {
				return fmt.Errorf("postgres proxy server not found")
			}
			connectionID := string(pkt.Spec[pb.SpecClientConnectionID])
			if _, err := srv.PacketWriteClient(connectionID, pkt); err != nil {
				return fmt.Errorf("failed writing to client, err=%v", err)
			}
		case pbclient.MySQLConnectionWrite:
			sessionID := pkt.Spec[pb.SpecGatewaySessionID]
			srvObj := connStore.Get(string(sessionID))
			srv, ok := srvObj.(*proxy.MySQLServer)
			if !ok {
				return fmt.Errorf("msqyl proxy server instance not found")
			}
			connectionID := string(pkt.Spec[pb.SpecClientConnectionID])
			if _, err := srv.PacketWriteClient(connectionID, pkt); err != nil {
				return fmt.Errorf("failed writing to client, err=%v", err)
			}
		case pbclient.TCPConnectionWrite:
			sessionID := pkt.Spec[pb.SpecGatewaySessionID]
			connectionID := string(pkt.Spec[pb.SpecClientConnectionID])
			if tcp, ok := connStore.Get(string(sessionID)).(*proxy.TCPServer); ok {
				if _, err := tcp.PacketWriteClient(connectionID, pkt); err != nil {
					return fmt.Errorf("failed writing to client, err=%v", err)
				}
			}
		case pbclient.TCPConnectionClose, pbclient.SessionClose:
			sessionID := pkt.Spec[pb.SpecGatewaySessionID]
			pgpObj := connStore.Get(string(sessionID))
			if pgp, ok := pgpObj.(*proxy.PGServer); ok {
				pgp.PacketCloseConnection(string(pkt.Spec[pb.SpecClientConnectionID]))
			}
		}
	}
}
