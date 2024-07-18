package cmd

import (
	"fmt"
	"io"
	"time"

	clientconfig "github.com/hoophq/hoop/client/config"
	"github.com/hoophq/hoop/client/proxy"
	"github.com/hoophq/hoop/common/backoff"
	"github.com/hoophq/hoop/common/grpc"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/memory"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	pbclient "github.com/hoophq/hoop/common/proto/client"
	pbgateway "github.com/hoophq/hoop/common/proto/gateway"
	"github.com/hoophq/hoop/common/version"
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
			err := backoff.Exponential2x(func(v time.Duration) error {
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
					log.Warnf("failed connecting to gateway, reason=%v", err)
					return backoff.Error()
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
						log.Info(err)
						return nil
					}
				}
				if err != nil {
					log.Warnf("failed processing proxy manager, reason=%v", err)
					return backoff.Error()
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
	defer func() {
		for _, obj := range connStore.List() {
			if c, _ := obj.(proxy.Closer); c != nil {
				_ = c.Close()
			}
		}
	}()
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
			connnectionType := pb.ConnectionType(pkt.Spec[pb.SpecConnectionType])
			if sid == "" {
				return fmt.Errorf("session is empty")
			}
			log.With("type", connnectionType).Infof("session opened")
			client.StartKeepAlive()
			switch connnectionType {
			case pb.ConnectionTypePostgres:
				srv := proxy.NewPGServer(proxyPort, client)
				if err := srv.Serve(sid); err != nil {
					return err
				}
				connStore.Set(sid, srv)
			case pb.ConnectionTypeMySQL:
				srv := proxy.NewMySQLServer(proxyPort, client)
				if err := srv.Serve(sid); err != nil {
					return err
				}
				connStore.Set(sid, srv)
			case pb.ConnectionTypeMSSQL:
				srv := proxy.NewMSSQLServer(proxyPort, client)
				if err := srv.Serve(sid); err != nil {
					return err
				}
				connStore.Set(sid, srv)
			case pb.ConnectionTypeMongoDB:
				srv := proxy.NewMongoDBServer(proxyPort, client)
				if err := srv.Serve(sid); err != nil {
					return err
				}
				connStore.Set(sid, srv)
			case pb.ConnectionTypeTCP:
				srv := proxy.NewTCPServer(proxyPort, client, pbagent.TCPConnectionWrite)
				if err := srv.Serve(sid); err != nil {
					return err
				}
				connStore.Set(sid, srv)
			default:
				return fmt.Errorf(`connection type %q not implemented`, string(connnectionType))
			}
			log.With("port", proxyPort).Infof("ready to accept connections")
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
				return fmt.Errorf("mysql proxy server instance not found")
			}
			connectionID := string(pkt.Spec[pb.SpecClientConnectionID])
			if _, err := srv.PacketWriteClient(connectionID, pkt); err != nil {
				return fmt.Errorf("failed writing to client, err=%v", err)
			}
		case pbclient.MSSQLConnectionWrite:
			srvObj := connStore.Get(sid)
			srv, ok := srvObj.(*proxy.MSSQLServer)
			if !ok {
				return fmt.Errorf("mssql proxy server instance not found")
			}
			connectionID := string(pkt.Spec[pb.SpecClientConnectionID])
			if _, err := srv.PacketWriteClient(connectionID, pkt); err != nil {
				return fmt.Errorf("failed writing to client, err=%v", err)
			}
		case pbclient.MongoDBConnectionWrite:
			srvObj := connStore.Get(sid)
			srv, ok := srvObj.(*proxy.MongoDBServer)
			if !ok {
				return fmt.Errorf("mongodb proxy server instance not found")
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
				log.Debugf("closing tcp session")
				srv.CloseTCPConnection(string(pkt.Spec[pb.SpecClientConnectionID]))
			}
		case pbclient.SessionClose:
			if srv, ok := connStore.Get(sid).(proxy.Closer); ok {
				_ = srv.Close()
				log.Infof("session closed")
			}
		default:
			return fmt.Errorf("unknown packet %v", pkt.Type)
		}
	}
}
