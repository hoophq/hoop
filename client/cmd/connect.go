package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/briandowns/spinner"
	"github.com/muesli/termenv"
	"github.com/runopsio/hoop/client/proxy"
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
	config := getClientConfig()

	loader := spinner.New(spinner.CharSets[78], 70*time.Millisecond)
	loader.Color("green")
	loader.Start()
	loader.Suffix = " connecting to gateway..."

	c, spec := newClientConnect(config, loader, args, pb.ClientVerbConnect)

	if err := c.client.Send(&pb.Packet{
		Type: pb.PacketClientGatewayConnectType.String(),
		Spec: spec,
	}); err != nil {
		_, _ = c.client.Close()
		c.printErrorAndExit("failed connecting to gateway, err=%v", err)
	}

	for {
		pkt, err := c.client.Recv()
		c.processGracefulExit(err)
		if pkt != nil {
			c.processPacket(pkt)
		}
	}
}

func (c *connect) processPacket(pkt *pb.Packet) {
	switch pb.PacketType(pkt.Type) {

	// connect
	case pb.PacketClientAgentConnectOKType:
		sessionID, ok := pkt.Spec[pb.SpecGatewaySessionID]
		if !ok || sessionID == nil {
			c.processGracefulExit(fmt.Errorf("internal error, session not found"))
		}
		connnectionType := pkt.Spec[pb.SpecConnectionType]
		switch string(connnectionType) {
		case "postgres":
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
		case "command-line":
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
			if err := term.ConnecWithTTY(); err != nil {
				c.processGracefulExit(err)
			}
		default:
			c.processGracefulExit(fmt.Errorf(`connection type %q not implemented`, string(connnectionType)))
		}
	case pb.PacketClientAgentConnectErrType:
		sessionID := pkt.Spec[pb.SpecGatewaySessionID]
		errMsg := fmt.Errorf("session=%s - failed connecting with gateway, err=%v",
			string(sessionID), string(pkt.GetPayload()))
		c.processGracefulExit(errMsg)

	// exec
	case pb.PacketClientExecAgentOfflineType:
		fmt.Print("Agent is offline. Do you want to try again?\n (y/n) [y] ")
		reader := bufio.NewReader(os.Stdin)
		result := "y"
		for {
			c, _ := reader.ReadByte()
			result = strings.Trim(string(c), " \n")
			break
		}

		if result == "y" {
			pkt.Type = string(pb.PacketClientGatewayExecType)
			_ = c.client.Send(pkt)
			return
		}
		c.processGracefulExit(errors.New("user aborted"))
	case pb.PacketClientGatewayExecWaitType:
		fmt.Println("This command requires review. We will notify you right here when it is approved")
		return
	case pb.PacketClientGatewayExecAllowType:
		fmt.Print("The command was approved! Do you want to run it now?\n (y/n) [y] ")
		reader := bufio.NewReader(os.Stdin)
		result := "y"
		for {
			c, _ := reader.ReadByte()
			result = strings.Trim(string(c), " \n")
			break
		}

		if result == "y" {
			pkt.Type = string(pb.PacketClientGatewayExecType)
			_ = c.client.Send(pkt)
			return
		}
		c.processGracefulExit(errors.New("user aborted"))

	// pg protocol messages
	case pb.PacketPGWriteClientType:
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
	case pb.PacketCloseTCPConnectionType:
		sessionID := pkt.Spec[pb.SpecGatewaySessionID]
		pgpObj := c.connStore.Get(string(sessionID))
		if pgp, ok := pgpObj.(*proxy.PGServer); ok {
			pgp.PacketCloseConnection(string(pkt.Spec[pb.SpecClientConnectionID]))
		}

	// process exec
	case pb.PacketTerminalClientWriteStdoutType:
		sessionID := pkt.Spec[pb.SpecGatewaySessionID]
		if term, ok := c.connStore.Get(string(sessionID)).(*proxy.Terminal); ok {
			_, _ = term.ProcessPacketWriteStdout(pkt)
		}
	case pb.PacketTerminalCloseType:
		sessionID := pkt.Spec[pb.SpecGatewaySessionID]
		if term, ok := c.connStore.Get(string(sessionID)).(*proxy.Terminal); ok {
			exitCode := term.ProcessPacketCloseTerm(pkt)
			os.Exit(exitCode)
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
	c.loader.Disable()
	p := termenv.ColorProfile()
	out := termenv.String(fmt.Sprintf(format, v...)).
		Foreground(p.Color("0")).
		Background(p.Color("#DBAB79"))
	fmt.Println(out.String())
	os.Exit(1)
}
