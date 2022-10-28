package cmd

import (
	"fmt"
	"io"
	"log"
	"os"
	"runtime"
	"time"

	"github.com/briandowns/spinner"
	"github.com/muesli/termenv"
	"github.com/runopsio/hoop/client/proxyexec"
	"github.com/runopsio/hoop/client/proxypg"
	"github.com/runopsio/hoop/common/grpc"
	"github.com/runopsio/hoop/common/memory"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/spf13/cobra"
)

var (
	connectCmd = &cobra.Command{
		Use:          "connect CONNECTION",
		Short:        "Connect to a remote resource",
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
	connectCmd.Flags().StringVarP(&connectFlags.proxyPort, "port", "p", "", "The port to bind the proxy if it's a native database connection")
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
	config := loadConfig()

	if config.Token == "" {
		if err := doLogin(nil); err != nil {
			panic(err)
		}
		config = loadConfig()
	}

	loader := spinner.New(spinner.CharSets[78], 70*time.Millisecond)
	loader.Color("green")
	loader.Start()
	loader.Suffix = " connecting to gateway..."
	c := &connect{
		proxyPort:      connectFlags.proxyPort,
		connStore:      memory.New(),
		clientArgs:     args[1:],
		connectionName: args[0],
		loader:         loader,
	}

	client, err := grpc.Connect(
		config.Host+":"+config.Port,
		config.Token,
		grpc.WithOption(grpc.OptionConnectionName, args[0]),
		grpc.WithOption("origin", pb.ConnectionOriginClient))
	if err != nil {
		c.printErrorAndExit(err.Error())
	}
	loader.Suffix = " validating connection..."
	spec := map[string][]byte{}
	if len(c.clientArgs) > 0 {
		encArgs, err := pb.GobEncode(c.clientArgs)
		if err != nil {
			log.Fatalf("failed encoding args, err=%v", err)
		}
		spec[string(pb.SpecClientExecArgsKey)] = encArgs
	}
	c.client = client
	if err := client.Send(&pb.Packet{
		Type: pb.PacketGatewayConnectType.String(),
		Spec: spec,
	}); err != nil {
		_, _ = client.Close()
		c.printErrorAndExit("failed connecting to gateway, err=%v", err)
	}
	for {
		pkt, err := client.Recv()
		c.processGracefulExit(err)
		if pkt != nil {
			c.processPacket(pkt)
		}
	}
}

func (c *connect) processPacket(pkt *pb.Packet) {
	c.processAgentConnectPhase(pkt)
	c.processPGProtocol(pkt)
	c.processExec(pkt)
}

func (c *connect) processGracefulExit(err error) {
	if err == nil {
		return
	}
	c.loader.Stop()
	for sessionID, obj := range c.connStore.List() {
		switch v := obj.(type) {
		case *proxyexec.Terminal:
			v.Close()
			if err == io.EOF {
				os.Exit(0)
			}
			fmt.Printf("\n\n")
			c.printErrorAndExit(err.Error())
		case *proxypg.Server:
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

func (c *connect) processAgentConnectPhase(pkt *pb.Packet) {
	switch pb.PacketType(pkt.Type) {
	case pb.PacketGatewayConnectOKType:
		sessionID, ok := pkt.Spec[pb.SpecGatewaySessionID]
		if !ok || sessionID == nil {
			c.processGracefulExit(fmt.Errorf("internal error, session not found"))
		}
		connnectionType := pkt.Spec[pb.SpecConnectionType]
		switch string(connnectionType) {
		case "postgres":
			// start postgres server
			pgp := proxypg.New(c.proxyPort, c.client)
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
		case "command-line":
			if runtime.GOOS == "windows" {
				fmt.Println("command line is not supported on Windows")
				os.Exit(1)
			}
			// c.handleCmdInterrupt()
			c.loader.Stop()
			c.client.StartKeepAlive()
			term := proxyexec.New(c.client)
			c.printHeader(string(sessionID))
			c.connStore.Set(string(sessionID), term)
			if err := term.ConnecWithTTY(); err != nil {
				c.processGracefulExit(err)
			}
		default:
			c.processGracefulExit(fmt.Errorf(`connection type %q not implemented`, string(connnectionType)))
		}
	case pb.PacketGatewayConnectErrType:
		sessionID := pkt.Spec[pb.SpecGatewaySessionID]
		errMsg := fmt.Errorf("session=%s - failed connecting with gateway, err=%v",
			string(sessionID), string(pkt.GetPayload()))
		c.processGracefulExit(errMsg)
	}
}

func (c *connect) processPGProtocol(pkt *pb.Packet) {
	switch pb.PacketType(pkt.Type) {
	case pb.PacketPGWriteClientType:
		sessionID := pkt.Spec[pb.SpecGatewaySessionID]
		pgpObj := c.connStore.Get(string(sessionID))
		pgp, ok := pgpObj.(*proxypg.Server)
		if !ok {
			return
		}
		connectionID := string(pkt.Spec[pb.SpecClientConnectionID])
		_, err := pgp.PacketWriteClient(connectionID, pkt)
		if err != nil {
			c.processGracefulExit(fmt.Errorf("failed writing to client, err=%v", err))
		}
	case pb.PacketCloseConnectionType:
		sessionID := pkt.Spec[pb.SpecGatewaySessionID]
		pgpObj := c.connStore.Get(string(sessionID))
		if pgp, ok := pgpObj.(*proxypg.Server); ok {
			pgp.PacketCloseConnection(string(pkt.Spec[pb.SpecClientConnectionID]))
		}
	}
}

func (c *connect) processExec(pkt *pb.Packet) {
	if pb.PacketType(pkt.Type) != pb.PacketExecCloseTermType &&
		pb.PacketType(pkt.Type) != pb.PacketExecClientWriteStdoutType {
		return
	}
	switch pb.PacketType(pkt.Type) {
	case pb.PacketExecClientWriteStdoutType:
		sessionID := pkt.Spec[pb.SpecGatewaySessionID]
		if term, ok := c.connStore.Get(string(sessionID)).(*proxyexec.Terminal); ok {
			_, _ = term.ProcessPacketWriteStdout(pkt)
		}
	case pb.PacketExecCloseTermType:
		sessionID := pkt.Spec[pb.SpecGatewaySessionID]
		if term, ok := c.connStore.Get(string(sessionID)).(*proxyexec.Terminal); ok {
			exitCode := term.ProcessPacketCloseTerm(pkt)
			os.Exit(exitCode)
		}
	}
}

func (c *connect) printErrorAndExit(format string, v ...any) {
	c.loader.Disable()
	p := termenv.ColorProfile()
	out := termenv.String(fmt.Sprintf(format, v...)).
		Foreground(p.Color("0")).
		Background(p.Color("#DBAB79"))
	fmt.Println(out.String())
	os.Exit(1)
}
