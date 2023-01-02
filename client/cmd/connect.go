package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"runtime"
	"strconv"
	"time"

	"github.com/briandowns/spinner"
	"github.com/muesli/termenv"
	"github.com/runopsio/hoop/client/cmd/styles"
	"github.com/runopsio/hoop/client/proxy"
	"github.com/runopsio/hoop/common/memory"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/common/terminal"
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
	waitingReview  *pb.Packet
	waitingJit     *pb.Packet
	loader         *spinner.Spinner
}

func runConnect(args []string) {
	config := getClientConfig()

	loader := spinner.New(spinner.CharSets[78], 70*time.Millisecond)
	loader.Color("green")
	loader.Start()
	loader.Suffix = " connecting to gateway..."

	c := newClientConnect(config, loader, args, pb.ClientVerbConnect)

	if err := c.client.Send(&pb.Packet{
		Type: pb.PacketClientGatewayConnectType.String(),
		Spec: newClientArgsSpec(c.clientArgs),
	}); err != nil {
		_, _ = c.client.Close()
		c.printErrorAndExit("failed connecting to gateway, err=%v", err)
	}

	for {
		pkt, err := c.client.Recv()
		c.processGracefulExit(err)
		if pkt != nil {
			c.processPacket(pkt, config, loader)
		}
	}
}

func (c *connect) processPacket(pkt *pb.Packet, config *Config, loader *spinner.Spinner) {
	switch pb.PacketType(pkt.Type) {

	// connect
	case pb.PacketClientAgentConnectOKType:
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
			tcp := proxy.NewTCPServer(proxyPort, c.client, pb.PacketTCPWriteServerType)
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
	case pb.PacketClientGatewayConnectRequestTimeoutType:
		loader.Stop()
		c.waitingJit = pkt
		tty, err := os.Open("/dev/tty")
		if err != nil {
			log.Fatal(err)
		}
		defer tty.Close()

		oldStdin := os.Stdin
		defer func() { os.Stdin = oldStdin }()

		os.Stdin = tty

		var input string
		fmt.Print("For how many minutes do you want to connect? ")

		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			s := scanner.Text()
			input += s
			break
		}

		if _, err := strconv.Atoi(input); err != nil {
			c.processGracefulExit(fmt.Errorf(`invalid number %q`, input))
		}

		c.waitingJit.Type = string(pb.PacketClientGatewayConnectType)
		c.waitingJit.Spec[pb.SpecJitTimeout] = []byte(input)
		_ = c.client.Send(c.waitingJit)

	case pb.PacketClientGatewayConnectWaitType:
		loader.Stop()
		loader.Start()
		loader.Suffix = " Waiting approval"
		c.waitingJit = pkt
		reviewID := string(pkt.Spec[pb.SpecGatewayJitID])
		fmt.Println("\nThis connection requires review. We will notify you here when it is approved.")
		fmt.Printf("Check the status at: %s\n\n", buildReviewUrl(config, reviewID, "jits"))
	case pb.PacketClientGatewayConnectApproveType:
		loader.Stop()
		tty, err := os.Open("/dev/tty")
		if err != nil {
			log.Fatal(err)
		}
		defer tty.Close()

		oldStdin := os.Stdin
		defer func() { os.Stdin = oldStdin }()

		os.Stdin = tty

		var input string
		fmt.Print("The connection was approved! Do you want to continue?\n (y/n) [y] ")

		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			s := scanner.Text()
			input += s
			break
		}

		if input == "" {
			input = "y"
		}

		input = input[0:1]

		if input == "y" {
			c.waitingJit.Type = string(pb.PacketClientGatewayConnectType)
			_ = c.client.Send(c.waitingJit)
			c.waitingJit = nil
			return
		}
		c.processGracefulExit(errors.New("user cancelled the action"))
	case pb.PacketClientGatewayConnectRejectType:
		c.processGracefulExit(errors.New("connection rejected. Sorry"))
	case pb.PacketClientAgentConnectErrType:
		sessionID := pkt.Spec[pb.SpecGatewaySessionID]
		errMsg := fmt.Errorf("session=%s - failed connecting with gateway, err=%v",
			string(sessionID), string(pkt.GetPayload()))
		c.processGracefulExit(errMsg)
	case pb.PacketClientConnectAgentOfflineType:
		loader.Stop()
		tty, err := os.Open("/dev/tty")
		if err != nil {
			log.Fatal(err)
		}
		defer tty.Close()

		oldStdin := os.Stdin
		defer func() { os.Stdin = oldStdin }()

		os.Stdin = tty

		var input string
		fmt.Print("Agent is offline. Do you want to try again?\n (y/n) [y] ")

		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			s := scanner.Text()
			input += s
			break
		}

		if input == "" {
			input = "y"
		}

		input = input[0:1]

		if input == "y" {
			pkt.Type = string(pb.PacketClientGatewayConnectType)
			_ = c.client.Send(pkt)
			return
		}
		c.processGracefulExit(errors.New("user cancelled the action"))
	case pb.PacketClientConnectTimeoutType:
		fmt.Println("Time is up. Please reconnect to continue using")

	// exec
	case pb.PacketClientExecAgentOfflineType:
		tty, err := os.Open("/dev/tty")
		if err != nil {
			log.Fatal(err)
		}
		defer tty.Close()

		oldStdin := os.Stdin
		defer func() { os.Stdin = oldStdin }()

		os.Stdin = tty

		var input string
		fmt.Print("Agent is offline. Do you want to try again?\n (y/n) [y] ")

		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			s := scanner.Text()
			input += s
			break
		}

		if input == "" {
			input = "y"
		}

		input = input[0:1]

		if input == "y" {
			pkt.Type = string(pb.PacketClientGatewayExecType)
			_ = c.client.Send(pkt)
			return
		}
		c.processGracefulExit(errors.New("user cancelled the action"))
	case pb.PacketClientGatewayExecWaitType:
		c.waitingReview = pkt
		reviewID := string(pkt.Spec[pb.SpecGatewayReviewID])
		fmt.Println("\nThis command requires review. We will notify you here when it is approved.")
		fmt.Printf("Check the status at: %s\n\n", buildReviewUrl(config, reviewID, "reviews"))
	case pb.PacketClientGatewayExecApproveType:
		tty, err := os.Open("/dev/tty")
		if err != nil {
			log.Fatal(err)
		}
		defer tty.Close()

		oldStdin := os.Stdin
		defer func() { os.Stdin = oldStdin }()

		os.Stdin = tty

		var input string
		fmt.Print("The command was approved! Do you want to run it now?\n (y/n) [y] ")

		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			s := scanner.Text()
			input += s
			break
		}

		if input == "" {
			input = "y"
		}

		input = input[0:1]

		if input == "y" {
			c.waitingReview.Type = string(pb.PacketClientGatewayExecType)
			_ = c.client.Send(c.waitingReview)
			c.waitingReview = nil
			return
		}
		c.processGracefulExit(errors.New("user cancelled the action"))
	case pb.PacketClientGatewayExecRejectType:
		c.processGracefulExit(errors.New("task rejected. Sorry"))
	case pb.PacketClientAgentExecOKType:
		c.printOutputAndExit(pkt.Payload)
	case pb.PacketClientAgentExecErrType:
		if len(pkt.Payload) > 0 {
			_, _ = os.Stderr.Write([]byte(styles.ClientError(string(pkt.Payload)) + "\n"))
		}
		exitCode, err := strconv.Atoi(string(pkt.Spec[pb.SpecClientExecExitCodeKey]))
		if err != nil {
			os.Exit(terminal.InternalErrorExitCode)
		}
		os.Exit(exitCode)
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
	case pb.PacketTCPWriteClientType:
		sessionID := pkt.Spec[pb.SpecGatewaySessionID]
		connectionID := string(pkt.Spec[pb.SpecClientConnectionID])
		if tcp, ok := c.connStore.Get(string(sessionID)).(*proxy.TCPServer); ok {
			_, err := tcp.PacketWriteClient(connectionID, pkt)
			if err != nil {
				c.processGracefulExit(fmt.Errorf("failed writing to client, err=%v", err))
			}
		}
	case pb.PacketCloseTCPConnectionType:
		sessionID := pkt.Spec[pb.SpecGatewaySessionID]
		pgpObj := c.connStore.Get(string(sessionID))
		if pgp, ok := pgpObj.(*proxy.PGServer); ok {
			pgp.PacketCloseConnection(string(pkt.Spec[pb.SpecClientConnectionID]))
		}

	// process terminal
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
	c.loader.Disable()
	errOutput := styles.ClientError(fmt.Sprintf(format, v...))
	fmt.Println(errOutput)
	os.Exit(1)
}

func (c *connect) printOutputAndExit(output []byte) {
	c.loader.Disable()
	fmt.Print(string(output))
}
