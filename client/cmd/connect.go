package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"time"

	"github.com/briandowns/spinner"
	"github.com/getsentry/sentry-go"
	"github.com/muesli/termenv"
	"github.com/runopsio/hoop/client/cmd/styles"
	clientconfig "github.com/runopsio/hoop/client/config"
	"github.com/runopsio/hoop/client/proxy"
	"github.com/runopsio/hoop/common/appruntime"
	"github.com/runopsio/hoop/common/grpc"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/memory"
	"github.com/runopsio/hoop/common/monitoring"
	pb "github.com/runopsio/hoop/common/proto"
	pbagent "github.com/runopsio/hoop/common/proto/agent"
	pbclient "github.com/runopsio/hoop/common/proto/client"
	pbterm "github.com/runopsio/hoop/common/terminal"
	"github.com/runopsio/hoop/common/version"
	"github.com/spf13/cobra"
)

type ConnectFlags struct {
	listenAddr string
	proxyPort  string
	duration   string
}

var connectFlags = ConnectFlags{}

var (
	connectCmd = &cobra.Command{
		Use:   "connect CONNECTION",
		Short: "Connect to a remote resource",
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if len(args) < 1 {
				return cmd.Usage()
			}
			dur, err := time.ParseDuration(connectFlags.duration)
			if err != nil {
				return fmt.Errorf("invalid duration, valid units are 's', 'm', 'h'. E.g.: 60s|3m|1h")
			}
			if dur.Seconds() < 60 {
				return fmt.Errorf("the minimum duration is 60 seconds (60s)")
			}
			monitoring.SentryPreRun(cmd, args)
			return nil
		},
		SilenceUsage: false,
		Run: func(cmd *cobra.Command, args []string) {
			clientEnvVars, err := parseClientEnvVars()
			if err != nil {
				fmt.Println(err)
				os.Exit(1)
			}
			runConnect(args, clientEnvVars)
		},
	}
)

func init() {
	connectCmd.Flags().StringVarP(&connectFlags.proxyPort, "port", "p", "", "The port to listen the proxy")
	// TODO: temporary until we ship a agent as proxy mode
	connectCmd.Flags().StringVar(&connectFlags.listenAddr, "listen", "", "The listen address (host:port) to bind")
	connectCmd.Flags().StringSliceVarP(&inputEnvVars, "env", "e", nil, "Input environment variables to send")
	connectCmd.Flags().StringVarP(&connectFlags.duration, "duration", "d", "30m", "The amount of time that the session will last. Valid time units are 's', 'm', 'h'")
	_ = connectCmd.Flags().MarkHidden("listen")
	rootCmd.AddCommand(connectCmd)
}

type connect struct {
	proxyPort      string
	client         pb.ClientTransport
	connStore      memory.Store
	clientArgs     []string
	connectionName string
	loader         *spinner.Spinner
}

func runConnect(args []string, clientEnvVars map[string]string) {
	config := clientconfig.GetClientConfigOrDie()
	loader := spinner.New(spinner.CharSets[11], 70*time.Millisecond)
	loader.Color("green")
	loader.Start()
	loader.Suffix = " connecting to gateway..."

	c := newClientConnect(config, loader, args, pb.ClientVerbConnect)
	sendOpenSessionPktFn := func() {
		spec := newClientArgsSpec(c.clientArgs, clientEnvVars)
		spec[pb.SpecJitTimeout] = []byte(connectFlags.duration)
		if err := c.client.Send(&pb.Packet{
			Type: pbagent.SessionOpen,
			Spec: spec,
		}); err != nil {
			_, _ = c.client.Close()
			c.printErrorAndExit("failed opening session with gateway, err=%v", err)
		}
	}

	sendOpenSessionPktFn()
	agentOfflineRetryCounter := 1
	for {
		pkt, err := c.client.Recv()
		c.processGracefulExit(err)
		if pkt == nil {
			continue
		}
		switch pb.PacketType(pkt.Type) {
		case pbclient.SessionOpenWaitingApproval:
			loader.Color("yellow")
			if !loader.Active() {
				loader.Start()
			}
			loader.Suffix = " waiting task to be approved at " +
				styles.Keyword(fmt.Sprintf(" %v ", string(pkt.Payload)))
		case pbclient.SessionOpenOK:
			sessionID, ok := pkt.Spec[pb.SpecGatewaySessionID]
			if !ok || sessionID == nil {
				c.processGracefulExit(fmt.Errorf("internal error, session not found"))
			}
			connnectionType := pkt.Spec[pb.SpecConnectionType]
			switch string(connnectionType) {
			case pb.ConnectionTypePostgres:
				// TODO: temporary until we ship a agent as proxy mode
				listenAddr := connectFlags.listenAddr
				if connectFlags.proxyPort != "" {
					listenAddr = fmt.Sprintf("127.0.0.1:%s", connectFlags.proxyPort)
				}
				srv := proxy.NewPGServer(listenAddr, c.client)
				if err := srv.Serve(string(sessionID)); err != nil {
					sentry.CaptureException(fmt.Errorf("connect - failed initializing postgres proxy, err=%v", err))
					c.processGracefulExit(err)
				}
				c.loader.Stop()
				c.client.StartKeepAlive()
				c.connStore.Set(string(sessionID), srv)
				c.printHeader(string(sessionID))
				fmt.Println()
				fmt.Println("--------------------postgres-credentials--------------------")
				fmt.Printf("      host=127.0.0.1 port=%s user=noop password=noop\n", srv.ListenPort())
				fmt.Println("------------------------------------------------------------")
				fmt.Println("ready to accept connections!")
			case pb.ConnectionTypeMySQL:
				srv := proxy.NewMySQLServer(c.proxyPort, c.client)
				if err := srv.Serve(string(sessionID)); err != nil {
					sentry.CaptureException(fmt.Errorf("connect - failed initializing mysql proxy, err=%v", err))
					c.processGracefulExit(err)
				}
				c.loader.Stop()
				c.client.StartKeepAlive()
				c.connStore.Set(string(sessionID), srv)
				c.printHeader(string(sessionID))
				fmt.Println()
				fmt.Println("---------------------mysql-credentials----------------------")
				fmt.Printf("      host=127.0.0.1 port=%s user=noop password=noop\n", srv.ListenPort())
				fmt.Println("------------------------------------------------------------")
				fmt.Println("ready to accept connections!")
			case pb.ConnectionTypeMSSQL:
				srv := proxy.NewMSSQLServer(c.proxyPort, c.client)
				if err := srv.Serve(string(sessionID)); err != nil {
					sentry.CaptureException(fmt.Errorf("connect - failed initializing mssql proxy, err=%v", err))
					c.processGracefulExit(err)
				}
				c.loader.Stop()
				c.client.StartKeepAlive()
				c.connStore.Set(string(sessionID), srv)
				c.printHeader(string(sessionID))
				fmt.Println()
				fmt.Println("---------------------mssql-credentials----------------------")
				fmt.Printf("      host=127.0.0.1 port=%s user=noop password=noop\n", srv.ListenPort())
				fmt.Println("------------------------------------------------------------")
				fmt.Println("ready to accept connections!")
			case pb.ConnectionTypeTCP:
				proxyPort := "8999"
				if c.proxyPort != "" {
					proxyPort = c.proxyPort
				}
				tcp := proxy.NewTCPServer(proxyPort, c.client, pbagent.TCPConnectionWrite)
				if err := tcp.Serve(string(sessionID)); err != nil {
					sentry.CaptureException(fmt.Errorf("connect - failed initializing tcp proxy, err=%v", err))
					c.processGracefulExit(err)
				}
				c.loader.Stop()
				c.client.StartKeepAlive()
				c.connStore.Set(string(sessionID), tcp)
				c.printHeader(string(sessionID))
				fmt.Println()
				fmt.Println("--------------------tcp-connection--------------------")
				fmt.Printf("               host=127.0.0.1 port=%s\n", tcp.ListenPort())
				fmt.Println("------------------------------------------------------")
				fmt.Println("ready to accept connections!")
			case pb.ConnectionTypeCommandLine:
				// https://github.com/creack/pty/issues/95
				if runtime.GOOS == "windows" {
					fmt.Println("command line is not supported on Windows")
					os.Exit(1)
				}
				// c.handleCmdInterrupt()
				c.loader.Stop()
				c.client.StartKeepAlive()
				term := proxy.NewTerminal(c.client)
				c.printHeader(string(sessionID))
				c.connStore.Set(string(sessionID), term)
				if err := term.ConnectWithTTY(); err != nil {
					sentry.CaptureException(fmt.Errorf("connect - failed initializing terminal, err=%v", err))
					c.processGracefulExit(err)
				}
			default:
				errMsg := fmt.Errorf(`connection type %q not implemented`, string(connnectionType))
				sentry.CaptureException(fmt.Errorf("connect - %v", errMsg))
				c.processGracefulExit(errMsg)
			}
		case pbclient.SessionOpenApproveOK:
			loader.Color("green")
			loader.Suffix = " command approved, running ... "
			sendOpenSessionPktFn()
		case pbclient.SessionOpenAgentOffline:
			if agentOfflineRetryCounter > 60 {
				c.processGracefulExit(errors.New("agent is offline, max retry reached"))
			}
			loader.Color("red")
			loader.Suffix = fmt.Sprintf(" agent is offline, retrying in 30s (%v/60) ... ", agentOfflineRetryCounter)
			time.Sleep(time.Second * 30)
			agentOfflineRetryCounter++
			sendOpenSessionPktFn()
		case pbclient.SessionOpenTimeout:
			c.processGracefulExit(fmt.Errorf("session ended, reached connection duration (%s)",
				connectFlags.duration))
		// process terminal
		case pbclient.WriteStdout:
			sessionID := pkt.Spec[pb.SpecGatewaySessionID]
			if term, ok := c.connStore.Get(string(sessionID)).(*proxy.Terminal); ok {
				_, _ = term.ProcessPacketWriteStdout(pkt)
			}
		case pbclient.PGConnectionWrite:
			sessionID := pkt.Spec[pb.SpecGatewaySessionID]
			srvObj := c.connStore.Get(string(sessionID))
			srv, ok := srvObj.(*proxy.PGServer)
			if !ok {
				return
			}
			connectionID := string(pkt.Spec[pb.SpecClientConnectionID])
			_, err := srv.PacketWriteClient(connectionID, pkt)
			if err != nil {
				errMsg := fmt.Errorf("failed writing to client, err=%v", err)
				sentry.CaptureException(fmt.Errorf("connect - %v - %v", pbclient.PGConnectionWrite, errMsg))
				c.processGracefulExit(errMsg)
			}
		case pbclient.MySQLConnectionWrite:
			sessionID := pkt.Spec[pb.SpecGatewaySessionID]
			srvObj := c.connStore.Get(string(sessionID))
			srv, ok := srvObj.(*proxy.MySQLServer)
			if !ok {
				return
			}
			connectionID := string(pkt.Spec[pb.SpecClientConnectionID])
			_, err := srv.PacketWriteClient(connectionID, pkt)
			if err != nil {
				errMsg := fmt.Errorf("failed writing to client, err=%v", err)
				sentry.CaptureException(fmt.Errorf("connect - %v - %v", pbclient.MySQLConnectionWrite, errMsg))
				c.processGracefulExit(errMsg)
			}
		case pbclient.MSSQLConnectionWrite:
			sessionID := pkt.Spec[pb.SpecGatewaySessionID]
			srvObj := c.connStore.Get(string(sessionID))
			srv, ok := srvObj.(*proxy.MSSQLServer)
			if !ok {
				return
			}
			connectionID := string(pkt.Spec[pb.SpecClientConnectionID])
			_, err := srv.PacketWriteClient(connectionID, pkt)
			if err != nil {
				errMsg := fmt.Errorf("failed writing to client, err=%v", err)
				sentry.CaptureException(fmt.Errorf("connect - %v - %v", pbclient.MySQLConnectionWrite, errMsg))
				c.processGracefulExit(errMsg)
			}
		case pbclient.TCPConnectionWrite:
			sessionID := pkt.Spec[pb.SpecGatewaySessionID]
			connectionID := string(pkt.Spec[pb.SpecClientConnectionID])
			if tcp, ok := c.connStore.Get(string(sessionID)).(*proxy.TCPServer); ok {
				_, err := tcp.PacketWriteClient(connectionID, pkt)
				if err != nil {
					errMsg := fmt.Errorf("failed writing to client, err=%v", err)
					sentry.CaptureException(fmt.Errorf("connect - %v - %v", pbclient.TCPConnectionWrite, errMsg))
					c.processGracefulExit(errMsg)
				}
			}
		// TODO: most agent protocols implementations are not sending this packet, instead a session close
		// packet is sent that ends the client connection. It's important to implement this cases in the agent
		// to avoid resource leaks in the client.
		case pbclient.TCPConnectionClose:
			sessionID := pkt.Spec[pb.SpecGatewaySessionID]
			srvObj := c.connStore.Get(string(sessionID))
			if srv, ok := srvObj.(proxy.Closer); ok {
				srv.CloseTCPConnection(string(pkt.Spec[pb.SpecClientConnectionID]))
			}
		case pbclient.SessionClose:
			// close terminal
			loader.Stop()
			sessionID := pkt.Spec[pb.SpecGatewaySessionID]
			if srv, ok := c.connStore.Get(string(sessionID)).(proxy.Closer); ok {
				srv.Close()
			}
			if len(pkt.Payload) > 0 {
				os.Stderr.Write([]byte(styles.ClientError(string(pkt.Payload)) + "\n"))
			}
			exitCodeStr := string(pkt.Spec[pb.SpecClientExitCodeKey])
			exitCode, err := strconv.Atoi(exitCodeStr)
			if exitCodeStr == "" || err != nil {
				exitCode = pbterm.InternalErrorExitCode
			}
			os.Exit(exitCode)
		}
	}
}

func (c *connect) processGracefulExit(err error) {
	if err == nil {
		return
	}
	c.loader.Stop()
	for _, obj := range c.connStore.List() {
		switch v := obj.(type) {
		case *proxy.Terminal:
			v.Close()
			if err == io.EOF {
				os.Exit(0)
			}
			fmt.Printf("\n\n")
			c.printErrorAndExit(err.Error())
		case proxy.Closer:
			v.Close()
			time.Sleep(time.Millisecond * 500)
			if err == io.EOF {
				os.Exit(0)
			}
			c.printErrorAndExit(err.Error())
		}
	}
	c.printErrorAndExit(err.Error())
}

func (c *connect) printHeader(sessionID string) {
	// termenv.NewOutput(os.Stdout).ClearScreen()
	s := termenv.String("connection: %s | session: %s").Faint()
	fmt.Printf(s.String(), c.connectionName, string(sessionID))
	fmt.Println()
}

func (c *connect) printErrorAndExit(format string, v ...any) {
	c.loader.Stop()
	errOutput := styles.ClientError(fmt.Sprintf(format, v...))
	fmt.Println(errOutput)
	os.Exit(1)
}

func newClientConnect(config *clientconfig.Config, loader *spinner.Spinner, args []string, verb string) *connect {
	c := &connect{
		proxyPort:      connectFlags.proxyPort,
		connStore:      memory.New(),
		clientArgs:     args[1:],
		connectionName: args[0],
		loader:         loader,
	}
	grpcClientOptions := []*grpc.ClientOptions{
		grpc.WithOption(grpc.OptionConnectionName, c.connectionName),
		grpc.WithOption("origin", pb.ConnectionOriginClient),
		grpc.WithOption("verb", verb),
		grpc.WithOption("apiv2", fmt.Sprintf("%v", appruntime.IsApiV2())),
	}
	clientConfig, err := config.GrpcClientConfig()
	if err != nil {
		c.printErrorAndExit(err.Error())
	}
	clientConfig.UserAgent = fmt.Sprintf("hoopcli/%v", version.Get().Version)
	c.client, err = grpc.Connect(clientConfig, grpcClientOptions...)
	if err != nil {
		c.printErrorAndExit(err.Error())
	}
	return c
}

func newClientArgsSpec(clientArgs []string, clientEnvVars map[string]string) map[string][]byte {
	spec := map[string][]byte{}
	if len(clientArgs) > 0 {
		encArgs, err := pb.GobEncode(clientArgs)
		if err != nil {
			log.Fatalf("failed encoding args, err=%v", err)
		}
		spec[pb.SpecClientExecArgsKey] = encArgs
	}
	if len(clientEnvVars) > 0 {
		encEnvVars, err := pb.GobEncode(clientEnvVars)
		if err != nil {
			log.Fatalf("failed encoding client env vars, err=%v", err)
		}
		spec[pb.SpecClientExecEnvVar] = encEnvVars
	}
	return spec
}
