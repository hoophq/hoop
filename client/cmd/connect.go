package cmd

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"runtime"
	"time"

	"github.com/briandowns/spinner"
	"github.com/muesli/termenv"
	"github.com/runopsio/hoop/client/cmd/styles"
	"github.com/runopsio/hoop/client/proxy"
	"github.com/runopsio/hoop/common/memory"
	pb "github.com/runopsio/hoop/common/proto"
	pbagent "github.com/runopsio/hoop/common/proto/agent"
	pbclient "github.com/runopsio/hoop/common/proto/client"
	"github.com/spf13/cobra"
)

var (
	connectCmd = &cobra.Command{
		Use:   "connect CONNECTION",
		Short: "Connect to a remote resource",
		PreRunE: func(cmd *cobra.Command, args []string) error {
			dur, err := time.ParseDuration(connectFlags.duration)
			if err != nil {
				return fmt.Errorf("invalid duration, valid units are 's', 'm', 'h'. E.g.: 60s|3m|1h")
			}
			if dur.Seconds() < 60 {
				return fmt.Errorf("the minimum duration is 60 seconds (60s)")
			}
			return nil
		},
		SilenceUsage: false,
		PreRun: func(cmd *cobra.Command, args []string) {
			if len(args) < 1 {
				cmd.Usage()
				os.Exit(1)
			}
		},
		Run: func(cmd *cobra.Command, args []string) {
			runConnect(args)
		},
	}
)

func init() {
	connectCmd.Flags().StringVarP(&connectFlags.proxyPort, "port", "p", "", "The port to listen the proxy")
	connectCmd.Flags().StringVarP(&connectFlags.duration, "duration", "d", "30m", "The amount of time that the session will last. Valid time units are 's', 'm', 'h'")
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

func runConnect(args []string) {
	config := getClientConfig()

	loader := spinner.New(spinner.CharSets[11], 70*time.Millisecond)
	loader.Color("green")
	loader.Start()
	loader.Suffix = " connecting to gateway..."

	c := newClientConnect(config, loader, args, pb.ClientVerbConnect)
	sendOpenSessionPktFn := func() {
		spec := newClientArgsSpec(c.clientArgs)
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
			loader.Start()
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
				// start postgres server
				pgp := proxy.NewPGServer(c.proxyPort, c.client)
				if err := pgp.Serve(string(sessionID)); err != nil {
					c.processGracefulExit(err)
				}
				c.loader.Stop()
				c.client.StartKeepAlive()
				c.connStore.Set(string(sessionID), pgp)
				c.printHeader(string(sessionID))
				fmt.Println()
				fmt.Println("--------------------postgres-credentials--------------------")
				fmt.Printf("      host=127.0.0.1 port=%s user=noop password=noop\n", pgp.ListenPort())
				fmt.Println("------------------------------------------------------------")
				log.Println("ready to accept connections!")
			case pb.ConnectionTypeTCP:
				proxyPort := "8999"
				if c.proxyPort != "" {
					proxyPort = c.proxyPort
				}
				tcp := proxy.NewTCPServer(proxyPort, c.client, pbagent.TCPConnectionWrite)
				if err := tcp.Serve(string(sessionID)); err != nil {
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
				log.Println("ready to accept connections!")
			case pb.ConnectionTypeCommandLine:
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
					c.processGracefulExit(err)
				}
			default:
				c.processGracefulExit(fmt.Errorf(`connection type %q not implemented`, string(connnectionType)))
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
			pgpObj := c.connStore.Get(string(sessionID))
			pgp, ok := pgpObj.(*proxy.PGServer)
			if !ok {
				return
			}
			connectionID := string(pkt.Spec[pb.SpecClientConnectionID])
			_, err := pgp.PacketWriteClient(connectionID, pkt)
			if err != nil {
				c.processGracefulExit(fmt.Errorf("failed writing to client, err=%v", err))
			}
		case pbclient.TCPConnectionWrite:
			sessionID := pkt.Spec[pb.SpecGatewaySessionID]
			connectionID := string(pkt.Spec[pb.SpecClientConnectionID])
			if tcp, ok := c.connStore.Get(string(sessionID)).(*proxy.TCPServer); ok {
				_, err := tcp.PacketWriteClient(connectionID, pkt)
				if err != nil {
					c.processGracefulExit(fmt.Errorf("failed writing to client, err=%v", err))
				}
			}
		case pbclient.TCPConnectionClose:
			sessionID := pkt.Spec[pb.SpecGatewaySessionID]
			pgpObj := c.connStore.Get(string(sessionID))
			if pgp, ok := pgpObj.(*proxy.PGServer); ok {
				pgp.PacketCloseConnection(string(pkt.Spec[pb.SpecClientConnectionID]))
			}
			// TODO: close tcp connection!
		case pbclient.SessionClose:
			// close terminal
			loader.Stop()
			sessionID := pkt.Spec[pb.SpecGatewaySessionID]
			if term, ok := c.connStore.Get(string(sessionID)).(*proxy.Terminal); ok {
				exitCode := term.ProcessPacketCloseTerm(pkt)
				os.Exit(exitCode)
			}
			// in case of connection store is empty,
			// best effort condition to exit properly
			if len(pkt.Payload) > 0 {
				c.processGracefulExit(errors.New(string(pkt.Payload)))
			}
		}
	}
}

func (c *connect) processGracefulExit(err error) {
	if err == nil {
		return
	}
	c.loader.Stop()
	for sessionID, obj := range c.connStore.List() {
		switch v := obj.(type) {
		case *proxy.Terminal:
			v.Close()
			if err == io.EOF {
				os.Exit(0)
			}
			fmt.Printf("\n\n")
			c.printErrorAndExit(err.Error())
		case *proxy.PGServer:
			v.PacketCloseConnection(sessionID)
			time.Sleep(time.Millisecond * 500)
			if err == io.EOF {
				os.Exit(0)
			}
			c.printErrorAndExit(err.Error())
		case *proxy.TCPServer:
			v.PacketCloseConnection(sessionID)
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
	termenv.NewOutput(os.Stdout).ClearScreen()
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
