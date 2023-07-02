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
	"github.com/runopsio/hoop/common/version"
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
				clientConfig.UserAgent = fmt.Sprintf("hoopcli/%v", version.Get().Version)
				client, err = grpc.Connect(clientConfig, grpcClientOptions...)
				if err != nil {
					return backoff.Errorf("failed connecting to gateway, reason=%v", err)
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
					case codes.FailedPrecondition:
						log.Info(pb.ErrAgentOffline)
						return nil
					}
				}
				if err != nil {
					return backoff.Errorf("failed processing proxy manager, reason=%v", err)
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
		log := log.With("phase", pkt.Type, "sid", sid)

		switch pb.PacketType(pkt.Type) {
		case pbclient.ProxyManagerConnectOK:
			log.Infof("received connect response from gateway, waiting for connection")
			_ = client.Send(&pb.Packet{Type: pbgateway.ProxyManagerConnectOKAck})
		case pbclient.SessionOpenWaitingApproval:
			log.Infof("waiting for approval %v", string(pkt.Payload))
		case pbclient.SessionOpenOK:
			proxyPort := string(pkt.Spec[pb.SpecClientRequestPort])
			connnectionType := string(pkt.Spec[pb.SpecConnectionType])
			if sid == "" {
				return fmt.Errorf("session is empty")
			}
			log.With("type", connnectionType).Infof("session opened")
			switch connnectionType {
			case pb.ConnectionTypePostgres:
				srv := proxy.NewPGServer(proxyPort, client)
				if err := srv.Serve(sid); err != nil {
					return err
				}
				defer srv.Close()
				client.StartKeepAlive()
				connStore.Set(sid, srv)
				log.With("type", connnectionType, "port", proxyPort).Infof("ready to accept connections")
			case pb.ConnectionTypeMySQL:
				srv := proxy.NewMySQLServer(proxyPort, client)
				if err := srv.Serve(sid); err != nil {
					return err
				}
				defer srv.Close()
				client.StartKeepAlive()
				connStore.Set(sid, srv)
				log.With("type", connnectionType, "port", proxyPort).Infof("ready to accept connections")
			case pb.ConnectionTypeTCP:
				srv := proxy.NewTCPServer(proxyPort, client, pbagent.TCPConnectionWrite)
				if err := srv.Serve(sid); err != nil {
					return err
				}
				defer srv.Close()
				client.StartKeepAlive()
				connStore.Set(sid, srv)
				log.With("type", connnectionType, "port", proxyPort).
					Infof("ready to accept connections")
			default:
				return fmt.Errorf(`connection type %q not implemented`, string(connnectionType))
			}
		case pbclient.SessionOpenApproveOK:
			log.Infof("session approved")
		case pbclient.SessionOpenAgentOffline:
			return pb.ErrAgentOffline
		case pbclient.SessionOpenTimeout:
			return fmt.Errorf("session ended, reached connection duration")
		case pbclient.PGConnectionWrite:
			srvObj := connStore.Get(sid)
			srv, ok := srvObj.(*proxy.PGServer)
			if !ok {
				return fmt.Errorf("postgres proxy server not found")
			}
			connectionID := string(pkt.Spec[pb.SpecClientConnectionID])
			if _, err := srv.PacketWriteClient(connectionID, pkt); err != nil {
				return fmt.Errorf("failed writing to client, err=%v", err)
			}
		case pbclient.MySQLConnectionWrite:
			srvObj := connStore.Get(sid)
			srv, ok := srvObj.(*proxy.MySQLServer)
			if !ok {
				return fmt.Errorf("msqyl proxy server instance not found")
			}
			connectionID := string(pkt.Spec[pb.SpecClientConnectionID])
			if _, err := srv.PacketWriteClient(connectionID, pkt); err != nil {
				return fmt.Errorf("failed writing to client, err=%v", err)
			}
		case pbclient.TCPConnectionWrite:
			connectionID := string(pkt.Spec[pb.SpecClientConnectionID])
			if tcp, ok := connStore.Get(sid).(*proxy.TCPServer); ok {
				if _, err := tcp.PacketWriteClient(connectionID, pkt); err != nil {
					return fmt.Errorf("failed writing to client, err=%v", err)
				}
			}
		// TODO: most agent protocols implementations are not sending this packet, instead a session close
		// packet is sent that ends the client connection. It's important to implement this cases in the agent
		// to avoid resource leaks in the client.
		case pbclient.TCPConnectionClose:
			if srv, ok := connStore.Get(sid).(proxy.Closer); ok {
				srv.CloseTCPConnection(string(pkt.Spec[pb.SpecClientConnectionID]))
			}
		case pbclient.SessionClose:
			if srv, ok := connStore.Get(sid).(proxy.Closer); ok {
				_ = srv.Close()
				log.Infof("session closed")
			}
		}
	}
}
