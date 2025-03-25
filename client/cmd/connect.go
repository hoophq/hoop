package cmd

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/briandowns/spinner"
	"github.com/hoophq/hoop/client/cmd/styles"
	clientconfig "github.com/hoophq/hoop/client/config"
	"github.com/hoophq/hoop/client/proxy"
	"github.com/hoophq/hoop/common/grpc"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/memory"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	pbclient "github.com/hoophq/hoop/common/proto/client"
	"github.com/hoophq/hoop/common/version"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
)

type ConnectFlags struct {
	proxyPort string
	duration  string
}

var connectFlags = ConnectFlags{}
var inputEnvVars []string

var (
	connectCmd = &cobra.Command{
		Use:   "connect CONNECTION",
		Short: "Connect to a remote resource",
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if len(args) < 1 {
				return fmt.Errorf("missing connection name")
			}
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
	connectCmd.Flags().StringSliceVarP(&inputEnvVars, "env", "e", nil, "Input environment variables to send")
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

func runConnect(args []string, clientEnvVars map[string]string) {
	config := clientconfig.GetClientConfigOrDie()
	loader := spinner.New(spinner.CharSets[11], 70*time.Millisecond)
	loader.Color("green")
	loader.Start()
	defer func() { loader.Stop() }()
	ossig := &osInterrupt{shutdownFn: func() { loader.Stop() }}
	ossig.handleOsInterrupt()
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

	var sshHostKeySigner ssh.Signer
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
			sshHostKeyEnc := pkt.Spec[pb.SpecClientSSHHostKey]
			if len(sshHostKeyEnc) > 0 {
				sshHostKeySigner, err = parseHostKey(sshHostKeyEnc)
			}
			if sshHostKeySigner == nil || err != nil {
				log.Debug("unable to parse SSH host key received from server, using random key")
				log.Debug("parse host key error=%v", err)
			}
			connnectionType := pb.ConnectionType(pkt.Spec[pb.SpecConnectionType])
			switch connnectionType {
			case pb.ConnectionTypePostgres:
				srv := proxy.NewPGServer(c.proxyPort, c.client)
				if err := srv.Serve(string(sessionID)); err != nil {
					c.processGracefulExit(err)
				}
				c.loader.Stop()
				c.client.StartKeepAlive()
				c.connStore.Set(string(sessionID), srv)
				c.printHeader(string(sessionID))
				fmt.Println()
				fmt.Println("--------------------postgres-credentials--------------------")
				fmt.Printf("      host=%s port=%s user=noop password=noop\n", srv.Host().Host, srv.Host().Port)
				fmt.Println("------------------------------------------------------------")
				fmt.Println("ready to accept connections!")
			case pb.ConnectionTypeMySQL:
				srv := proxy.NewMySQLServer(c.proxyPort, c.client)
				if err := srv.Serve(string(sessionID)); err != nil {
					c.processGracefulExit(err)
				}
				c.loader.Stop()
				c.client.StartKeepAlive()
				c.connStore.Set(string(sessionID), srv)
				c.printHeader(string(sessionID))
				fmt.Println()
				fmt.Println("---------------------mysql-credentials----------------------")
				fmt.Printf("      host=%s port=%s user=noop password=noop\n", srv.Host().Host, srv.Host().Port)
				fmt.Println("------------------------------------------------------------")
				fmt.Println("ready to accept connections!")
			case pb.ConnectionTypeMSSQL:
				srv := proxy.NewMSSQLServer(c.proxyPort, c.client)
				if err := srv.Serve(string(sessionID)); err != nil {
					c.processGracefulExit(err)
				}
				c.loader.Stop()
				c.client.StartKeepAlive()
				c.connStore.Set(string(sessionID), srv)
				c.printHeader(string(sessionID))
				fmt.Println()
				fmt.Println("---------------------mssql-credentials----------------------")
				fmt.Printf("      host=%s port=%s user=noop password=noop\n", srv.Host().Host, srv.Host().Port)
				fmt.Println("------------------------------------------------------------")
				fmt.Println("ready to accept connections!")
			case pb.ConnectionTypeMongoDB:
				srv := proxy.NewMongoDBServer(c.proxyPort, c.client)
				if err := srv.Serve(string(sessionID)); err != nil {
					c.processGracefulExit(err)
				}
				c.loader.Stop()
				c.client.StartKeepAlive()
				c.connStore.Set(string(sessionID), srv)
				c.printHeader(string(sessionID))
				fmt.Println()
				fmt.Println("---------------------mongo-credentials----------------------")
				fmt.Printf(" mongodb://noop:noop@%s:%s/?directConnection=true\n", srv.Host().Host, srv.Host().Port)
				fmt.Println("------------------------------------------------------------")
				fmt.Println("ready to accept connections!")
			case pb.ConnectionTypeTCP:
				tcp := proxy.NewTCPServer(c.proxyPort, c.client, pbagent.TCPConnectionWrite)
				if err := tcp.Serve(string(sessionID)); err != nil {
					c.processGracefulExit(err)
				}
				c.loader.Stop()
				c.client.StartKeepAlive()
				c.connStore.Set(string(sessionID), tcp)
				c.printHeader(string(sessionID))
				fmt.Println()
				fmt.Println("--------------------tcp-connection--------------------")
				fmt.Printf("               host=%s port=%s\n", tcp.Host().Host, tcp.Host().Port)
				fmt.Println("------------------------------------------------------")
				fmt.Println("ready to accept connections!")
			case pb.ConnectionTypeSSH:
				c.loader.Stop()
				srv := proxy.NewSSHServer(c.proxyPort, c.client, sshHostKeySigner)
				if err := srv.Serve(string(sessionID)); err != nil {
					c.processGracefulExit(err)
				}
				c.loader.Stop()
				c.client.StartKeepAlive()
				c.connStore.Set(string(sessionID), srv)
				c.printHeader(string(sessionID))
				fmt.Println()
				fmt.Println("--------------------ssh-connection--------------------")
				fmt.Printf("      host=%s port=%s user=noop password=noop\n", srv.Host().Host, srv.Host().Port)
				fmt.Println("------------------------------------------------------")
				fmt.Println("ready to accept connections!")
			case pb.ConnectionTypeHttpProxy:
				proxyPort := "8081"
				if c.proxyPort != "" {
					proxyPort = c.proxyPort
				}
				httpProxy := proxy.NewHttpProxy(proxyPort, c.client, pbagent.HttpProxyConnectionWrite)
				if err := httpProxy.Serve(string(sessionID)); err != nil {
					c.processGracefulExit(err)
				}
				c.loader.Stop()
				c.client.StartKeepAlive()
				c.connStore.Set(string(sessionID), httpProxy)
				c.printHeader(string(sessionID))
				fmt.Println()
				fmt.Println("--------------------http-connection--------------------")
				fmt.Printf("               host=127.0.0.1 port=%s\n", httpProxy.ListenPort())
				fmt.Println("------------------------------------------------------")
				fmt.Println("ready to accept connections!")
			case pb.ConnectionTypeCommandLine:
				c.loader.Stop()
				// https://github.com/creack/pty/issues/95
				if runtime.GOOS == "windows" {
					fmt.Println("Your current terminal environment (Windows/DOS) is not compatible with the Linux-based connection you're trying to access. To proceed, please use one of these options: ")
					fmt.Println("1. Windows Subsystem for Linux (WSL)")
					fmt.Println("2. Any Linux-compatible terminal emulator")
					fmt.Println("For more information, please visit https://hoop.dev/docs/getting-started/cli or contact us if you need further assistance.")
					os.Exit(1)
				}
				c.client.StartKeepAlive()
				term := proxy.NewTerminal(c.client)
				c.printHeader(string(sessionID))
				c.connStore.Set(string(sessionID), term)
				if err := term.ConnectWithTTY(); err != nil {
					c.processGracefulExit(err)
				}
				ossig.shutdownFn = func() { loader.Stop(); term.Close() }
			default:
				errMsg := fmt.Errorf(`connection type %q not implemented`, connnectionType.String())
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
				c.processGracefulExit(errMsg)
			}
		case pbclient.MongoDBConnectionWrite:
			sessionID := pkt.Spec[pb.SpecGatewaySessionID]
			srvObj := c.connStore.Get(string(sessionID))
			srv, ok := srvObj.(*proxy.MongoDBServer)
			if !ok {
				return
			}
			connectionID := string(pkt.Spec[pb.SpecClientConnectionID])
			_, err := srv.PacketWriteClient(connectionID, pkt)
			if err != nil {
				errMsg := fmt.Errorf("failed writing to client, err=%v", err)
				c.processGracefulExit(errMsg)
			}
		case pbclient.HttpProxyConnectionWrite:
			sessionID := pkt.Spec[pb.SpecGatewaySessionID]
			connectionID := string(pkt.Spec[pb.SpecClientConnectionID])
			if srv, ok := c.connStore.Get(string(sessionID)).(*proxy.HttpProxy); ok {
				_, err := srv.PacketWriteClient(connectionID, pkt)
				if err != nil {
					errMsg := fmt.Errorf("failed writing to client, err=%v", err)
					c.processGracefulExit(errMsg)
				}
			}
		case pbclient.TCPConnectionWrite:
			sessionID := pkt.Spec[pb.SpecGatewaySessionID]
			connectionID := string(pkt.Spec[pb.SpecClientConnectionID])
			if tcp, ok := c.connStore.Get(string(sessionID)).(*proxy.TCPServer); ok {
				_, err := tcp.PacketWriteClient(connectionID, pkt)
				if err != nil {
					errMsg := fmt.Errorf("failed writing to client, err=%v", err)
					c.processGracefulExit(errMsg)
				}
			}
		case pbclient.SSHConnectionWrite:
			sessionID := pkt.Spec[pb.SpecGatewaySessionID]
			connectionID := string(pkt.Spec[pb.SpecClientConnectionID])
			if srv, ok := c.connStore.Get(string(sessionID)).(*proxy.SSHServer); ok {
				_, err := srv.PacketWriteClient(connectionID, pkt)
				if err != nil {
					errMsg := fmt.Errorf("failed writing to client, err=%v", err)
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
			if err != nil {
				exitCode = 1
			}
			os.Exit(exitCode)
		}
	}
}

func (c *connect) processGracefulExit(err error) {
	if err == nil {
		return
	}
	if c.loader != nil {
		c.loader.Stop()
	}
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
	if c.loader != nil {
		c.loader.Stop()
	}
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

func parseClientEnvVars() (map[string]string, error) {
	envVar := map[string]string{}
	var invalidEnvs []string
	for _, envvarStr := range inputEnvVars {
		key, val, found := strings.Cut(envvarStr, "=")
		if !found {
			invalidEnvs = append(invalidEnvs, envvarStr)
			continue
		}
		envKey := fmt.Sprintf("envvar:%s", key)
		envVar[envKey] = base64.StdEncoding.EncodeToString([]byte(val))
	}
	if len(invalidEnvs) > 0 {
		return nil, fmt.Errorf("invalid client env vars, expected env=var. found=%v", invalidEnvs)
	}
	return envVar, nil
}

func parseHostKey(encHostKey []byte) (ssh.Signer, error) {
	hostKeyBytes, err := base64.StdEncoding.DecodeString(string(encHostKey))
	if err != nil {
		return nil, fmt.Errorf("failed decoding base64 hosts key: %v", err)
	}
	block, _ := pem.Decode(hostKeyBytes)
	if block == nil {
		return nil, fmt.Errorf("failed decoding host key PEM block")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed parsing private key to PKCS#8 format: %v", err)
	}
	signerKey, err := ssh.NewSignerFromKey(key)
	if err != nil {
		return nil, fmt.Errorf("failed parsing private key to ssh signer: %v", err)
	}
	return signerKey, nil
}

type osInterrupt struct {
	shutdownFn context.CancelFunc
}

func (o *osInterrupt) handleOsInterrupt() {
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc,
		syscall.SIGHUP,
		syscall.SIGINT,
		syscall.SIGTERM,
		syscall.SIGQUIT)
	go func() {
		sigval := <-sigc
		log.Debugf("received interrupt signal %v, executing shutdown ...", sigval.String())
		o.shutdownFn()
		switch sigval {
		case syscall.SIGHUP:
			os.Exit(int(syscall.SIGHUP))
		case syscall.SIGINT:
			os.Exit(int(syscall.SIGINT))
		case syscall.SIGTERM:
			os.Exit(int(syscall.SIGTERM))
		case syscall.SIGQUIT:
			os.Exit(int(syscall.SIGQUIT))
		}
	}()
}
