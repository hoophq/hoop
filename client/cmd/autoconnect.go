package cmd

import (
	"fmt"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/runopsio/hoop/client/proxy"
	"github.com/runopsio/hoop/common/grpc"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/memory"
	pb "github.com/runopsio/hoop/common/proto"
	pbclient "github.com/runopsio/hoop/common/proto/client"
	pbgateway "github.com/runopsio/hoop/common/proto/gateway"
	"github.com/spf13/cobra"
)

var (
	autoConnectCmd = &cobra.Command{
		Use:   "auto-connect",
		Short: "Auto connect listen commands from the API",
		// Hidden:       true,
		Hidden:       false,
		SilenceUsage: false,
		Run: func(cmd *cobra.Command, args []string) {
			runAutoConnect(args)
		},
	}
)

func init() { rootCmd.AddCommand(autoConnectCmd) }

func runAutoConnect(args []string) {
	config := getClientConfigOrDie()
	grpcClientOptions := []*grpc.ClientOptions{
		// grpc.WithOption(grpc.OptionConnectionName, c.connectionName),
		grpc.WithOption("origin", pb.ConnectionOriginClientAutoConnect),
		grpc.WithOption("verb", pb.ClientVerbConnect),
	}
	clientConfig, err := config.GrpcClientConfig()
	if err != nil {
		log.Fatal(err)
	}
	client, err := grpc.Connect(clientConfig, grpcClientOptions...)
	if err != nil {
		log.Fatal(err)
	}
	log.Infof("connected with gateway ...")
	sendOpenSessionPktFn := func() {
		// spec := newClientArgsSpec(c.clientArgs)
		// spec[pb.SpecJitTimeout] = []byte(connectFlags.duration)
		// if err := c.client.Send(&pb.Packet{
		// 	Type: pbagent.SessionOpen,
		// 	Spec: spec,
		// }); err != nil {
		// 	_, _ = c.client.Close()
		// 	c.printErrorAndExit("failed opening session with gateway, err=%v", err)
		// }
	}

	connStore := memory.New()
	// sendOpenSessionPktFn()
	agentOfflineRetryCounter := 1
	var pkt *pb.Packet
	for {
		pkt, err = client.Recv()
		if err != nil {
			// refactor processGracefulExit
			// processGracefulExit(err)
			log.Fatal(err)
		}
		if pkt == nil {
			continue
		}
		switch pb.PacketType(pkt.Type) {

		// auto connect flow
		case pbclient.ConnectOK:
			log.Debugf("connect ok, sending ack ...")
			_ = client.Send(&pb.Packet{Type: pbgateway.ConnectOKAck})

		// case pbclient.DoSubscribe:
		// 	log.Debugf("performing subscribe ...")
		// 	_ = client.Send(&pb.Packet{
		// 		Type: pbgateway.Subscribe,
		// 		Spec: map[string][]byte{pb.SpecConnectionName: pkt.Spec[pb.SpecConnectionName]}})
		// case pbclient.SubscribeOK:
		// 	log.Debugf("subscribe ok, opening session ...")
		// 	sendOpenSessionPktFn()

		case pbclient.SessionOpenWaitingApproval:
			// TODO
		case pbclient.SessionOpenOK:
			log.Debugf("session open ok, processing connection type %v",
				string(pkt.Spec[pb.SpecConnectionType]))
			sessionID, ok := pkt.Spec[pb.SpecGatewaySessionID]
			if !ok || sessionID == nil {
				// c.processGracefulExit(fmt.Errorf("internal error, session not found"))
				log.Fatal(err)
			}
			connnectionType := pkt.Spec[pb.SpecConnectionType]
			switch string(connnectionType) {
			case pb.ConnectionTypePostgres:
				// srv := proxy.NewPGServer(c.proxyPort, c.client)
				// if err := srv.Serve(string(sessionID)); err != nil {
				// 	sentry.CaptureException(fmt.Errorf("connect - failed initializing postgres proxy, err=%v", err))
				// 	c.processGracefulExit(err)
				// }
				// c.client.StartKeepAlive()
				// connStore.Set(string(sessionID), srv)
				// c.printHeader(string(sessionID))
				// fmt.Println()
				// fmt.Println("--------------------postgres-credentials--------------------")
				// fmt.Printf("      host=127.0.0.1 port=%s user=noop password=noop\n", srv.ListenPort())
				// fmt.Println("------------------------------------------------------------")
				// fmt.Println("ready to accept connections!")
			case pb.ConnectionTypeMySQL:
				// srv := proxy.NewMySQLServer(c.proxyPort, c.client)
				// if err := srv.Serve(string(sessionID)); err != nil {
				// 	sentry.CaptureException(fmt.Errorf("connect - failed initializing mysql proxy, err=%v", err))
				// 	c.processGracefulExit(err)
				// }
				// c.client.StartKeepAlive()
				// connStore.Set(string(sessionID), srv)
				// c.printHeader(string(sessionID))
				// fmt.Println()
				// fmt.Println("---------------------mysql-credentials----------------------")
				// fmt.Printf("      host=127.0.0.1 port=%s user=noop password=noop\n", srv.ListenPort())
				// fmt.Println("------------------------------------------------------------")
				// fmt.Println("ready to accept connections!")
			case pb.ConnectionTypeTCP:
				// proxyPort := "8999"
				// if c.proxyPort != "" {
				// 	proxyPort = c.proxyPort
				// }
				// tcp := proxy.NewTCPServer(proxyPort, c.client, pbagent.TCPConnectionWrite)
				// if err := tcp.Serve(string(sessionID)); err != nil {
				// 	sentry.CaptureException(fmt.Errorf("connect - failed initializing tcp proxy, err=%v", err))
				// 	c.processGracefulExit(err)
				// }
				// c.loader.Stop()
				// c.client.StartKeepAlive()
				// connStore.Set(string(sessionID), tcp)
				// c.printHeader(string(sessionID))
				// fmt.Println()
				// fmt.Println("--------------------tcp-connection--------------------")
				// fmt.Printf("               host=127.0.0.1 port=%s\n", tcp.ListenPort())
				// fmt.Println("------------------------------------------------------")
				// fmt.Println("ready to accept connections!")
			default:
				errMsg := fmt.Errorf(`connection type %q not implemented`, string(connnectionType))
				log.Fatal(errMsg)
				// c.processGracefulExit(errMsg)
			}
		case pbclient.SessionOpenApproveOK:
			sendOpenSessionPktFn()
		case pbclient.SessionOpenAgentOffline:
			if agentOfflineRetryCounter > 60 {
				log.Fatal("agent is offline, max retry reached")
			}
			time.Sleep(time.Second * 30)
			agentOfflineRetryCounter++
			sendOpenSessionPktFn()
		case pbclient.SessionOpenTimeout:
			log.Fatalf("session ended, reached connection duration (%s)", connectFlags.duration)
		case pbclient.PGConnectionWrite:
			sessionID := pkt.Spec[pb.SpecGatewaySessionID]
			srvObj := connStore.Get(string(sessionID))
			srv, ok := srvObj.(*proxy.PGServer)
			if !ok {
				return
			}
			connectionID := string(pkt.Spec[pb.SpecClientConnectionID])
			_, err := srv.PacketWriteClient(connectionID, pkt)
			if err != nil {
				errMsg := fmt.Errorf("failed writing to client, err=%v", err)
				sentry.CaptureException(fmt.Errorf("connect - %v - %v", pbclient.PGConnectionWrite, errMsg))
				log.Fatal(errMsg)
			}
		case pbclient.MySQLConnectionWrite:
			sessionID := pkt.Spec[pb.SpecGatewaySessionID]
			srvObj := connStore.Get(string(sessionID))
			srv, ok := srvObj.(*proxy.MySQLServer)
			if !ok {
				return
			}
			connectionID := string(pkt.Spec[pb.SpecClientConnectionID])
			_, err := srv.PacketWriteClient(connectionID, pkt)
			if err != nil {
				errMsg := fmt.Errorf("failed writing to client, err=%v", err)
				sentry.CaptureException(fmt.Errorf("connect - %v - %v", pbclient.MySQLConnectionWrite, errMsg))
				log.Fatal(errMsg)
			}
		case pbclient.TCPConnectionWrite:
			sessionID := pkt.Spec[pb.SpecGatewaySessionID]
			connectionID := string(pkt.Spec[pb.SpecClientConnectionID])
			if tcp, ok := connStore.Get(string(sessionID)).(*proxy.TCPServer); ok {
				_, err := tcp.PacketWriteClient(connectionID, pkt)
				if err != nil {
					errMsg := fmt.Errorf("failed writing to client, err=%v", err)
					sentry.CaptureException(fmt.Errorf("connect - %v - %v", pbclient.TCPConnectionWrite, errMsg))
					log.Fatal(errMsg)
				}
			}
		case pbclient.TCPConnectionClose:
			sessionID := pkt.Spec[pb.SpecGatewaySessionID]
			pgpObj := connStore.Get(string(sessionID))
			if pgp, ok := pgpObj.(*proxy.PGServer); ok {
				pgp.PacketCloseConnection(string(pkt.Spec[pb.SpecClientConnectionID]))
			}
			// TODO: close tcp connection!
		case pbclient.SessionClose:
			sessionID := pkt.Spec[pb.SpecGatewaySessionID]
			if term, ok := connStore.Get(string(sessionID)).(*proxy.Terminal); ok {
				term.Close()
			}
			// if len(pkt.Payload) > 0 {
			// 	os.Stderr.Write([]byte(styles.ClientError(string(pkt.Payload)) + "\n"))
			// }
			// exitCodeStr := string(pkt.Spec[pb.SpecClientExitCodeKey])
			// exitCode, err := strconv.Atoi(exitCodeStr)
			// if exitCodeStr == "" || err != nil {
			// 	exitCode = pbterm.InternalErrorExitCode
			// }
			// os.Exit(exitCode)
		}
	}
}
