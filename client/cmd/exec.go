package cmd

import (
	"bufio"
	"bytes"
	"encoding/base64"
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
	"github.com/getsentry/sentry-go"
	"github.com/muesli/termenv"
	"github.com/runopsio/hoop/client/cmd/styles"
	clientconfig "github.com/runopsio/hoop/client/config"
	"github.com/runopsio/hoop/common/monitoring"
	pb "github.com/runopsio/hoop/common/proto"
	pbagent "github.com/runopsio/hoop/common/proto/agent"
	pbclient "github.com/runopsio/hoop/common/proto/client"
	"github.com/runopsio/hoop/common/terminal"
	"github.com/spf13/cobra"
)

var inputFilepath string
var inputStdin string
var autoExec bool
var inputEnvVars []string
var verboseMode bool

// execCmd represents the exec command
var execCmd = &cobra.Command{
	Use:   "exec CONNECTION",
	Short: "Execute a given input in a remote resource",
	PreRun: func(cmd *cobra.Command, args []string) {
		if runtime.GOOS == "windows" {
			fmt.Println("exec is not supported on Windows")
			os.Exit(1)
		}
		if len(args) < 1 {
			cmd.Usage()
			os.Exit(1)
		}
		monitoring.SentryPreRun(cmd, args)
	},
	Run: func(cmd *cobra.Command, args []string) {
		clientEnvVars, err := parseClientEnvVars()
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		runExec(args, clientEnvVars)
	},
}

func init() {
	execCmd.Flags().StringVarP(&inputFilepath, "file", "f", "", "The path of the file containing the command")
	execCmd.Flags().StringVarP(&inputStdin, "input", "i", "", "The input to be executed remotely")
	execCmd.Flags().StringSliceVarP(&inputEnvVars, "env", "e", nil, "Input environment variables to send")
	execCmd.Flags().BoolVar(&autoExec, "auto-approve", false, "Automatically run after a command is approved")
	execCmd.Flags().BoolVarP(&verboseMode, "verbose", "v", false, "Verbose mode")
	rootCmd.AddCommand(execCmd)
}

func parseFlagInputs(c *connect) []byte {
	if inputFilepath != "" && inputStdin != "" {
		sentry.CaptureMessage("exec - client used --file and --input together")
		c.printErrorAndExit("accept only one option: --file (-f) or --input (-i)")
	}
	switch {
	case inputFilepath != "":
		input, err := os.ReadFile(inputFilepath)
		if err != nil {
			sentry.CaptureException(fmt.Errorf("exec - failed parsing input file, err=%v", err))
			c.printErrorAndExit("failed parsing input file [%s], err=%v", inputFilepath, err)
		}
		return input
	case inputStdin != "":
		return []byte(inputStdin)
	}
	return nil
}

func parseExecInput(c *connect) (bool, []byte) {
	info, err := os.Stdin.Stat()
	if err != nil {
		sentry.CaptureException(fmt.Errorf("exec - failed obtaining stdin path info, err=%v", err))
		c.printErrorAndExit(err.Error())
	}
	isStdinInput := false
	var input []byte
	// stdin input
	if info.Mode()&os.ModeCharDevice == 0 || info.Size() > 0 {
		if inputFilepath != "" || inputStdin != "" {
			sentry.CaptureMessage("exec - flags not allowed when reading from stdin")
			c.printErrorAndExit("flags not allowed when reading from stdin")
		}
		isStdinInput = true
		stdinPipe := os.NewFile(uintptr(syscall.Stdin), "/dev/stdin")
		reader := bufio.NewReader(stdinPipe)
		for {
			stdinInput, err := reader.ReadByte()
			if err != nil && err == io.EOF {
				break
			}
			input = append(input, stdinInput)
		}
		stdinPipe.Close()
	}
	if len(input) > 0 {
		return isStdinInput, input
	}
	return isStdinInput, parseFlagInputs(c)
}

func runExec(args []string, clientEnvVars map[string]string) {
	config := clientconfig.GetClientConfigOrDie()
	loader := spinner.New(spinner.CharSets[11], 70*time.Millisecond,
		spinner.WithWriter(os.Stderr), spinner.WithHiddenCursor(true))
	loader.Color("green")
	loader.Suffix = " running ..."
	loader.Start()
	done := make(chan os.Signal, 1)
	signal.Notify(done, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		for {
			switch <-done {
			case syscall.SIGTERM, syscall.SIGINT:
				loader.Stop() // this fixes terminal restore
				os.Exit(143)
			}
		}
	}()

	c := newClientConnect(config, loader, args, pb.ClientVerbExec)
	c.client.StartKeepAlive()
	execSpec := newClientArgsSpec(c.clientArgs, clientEnvVars)
	isStdinInput, execInputPayload := parseExecInput(c)
	sendOpenSessionPktFn := func() {
		if err := c.client.Send(&pb.Packet{
			Type:    pbagent.SessionOpen,
			Spec:    execSpec,
			Payload: execInputPayload,
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
		switch pkt.Type {
		case pbclient.SessionOpenWaitingApproval:
			if !autoExec && isStdinInput {
				loader.Stop()
				msg := "require use of --auto-approve option. It's a review command with an invalid device to prompt for execution"
				c.processGracefulExit(errors.New(msg))
			}
			loader.Color("yellow")
			if !loader.Active() {
				loader.Start()
			}
			loader.Suffix = " waiting command to be approved at " +
				styles.Keyword(fmt.Sprintf(" %v ", string(pkt.Payload)))
		case pbclient.SessionOpenApproveOK:
			if !autoExec {
				loader.Stop()
				fmt.Fprintf(os.Stderr, "command approved, press %v to run it ...", styles.Keyword(" <enter> "))
				_, err = io.CopyN(bytes.NewBufferString(""), os.Stdin, 1)
				if err != nil {
					c.processGracefulExit(errors.New("canceled by the user"))
				}
				loader.Start()
			}
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
		case pbclient.SessionOpenOK:
			execSpec[pb.SpecGatewaySessionID] = pkt.Spec[pb.SpecGatewaySessionID]
			stdinPkt := &pb.Packet{
				Type:    pbagent.ExecWriteStdin,
				Payload: execInputPayload,
				Spec:    execSpec,
			}
			if verboseMode {
				c.loader.Stop()
				out := fmt.Sprintf("session: %s", string(pkt.Spec[pb.SpecGatewaySessionID]))
				out = termenv.String(out).Faint().String()
				os.Stderr.Write([]byte(out))
				fmt.Println()
				c.loader.Start()
			}
			if err := c.client.Send(stdinPkt); err != nil {
				c.printErrorAndExit("failed executing command, err=%v", err)
			}
		case pbclient.WriteStdout:
			loader.Stop()
			os.Stdout.Write(pkt.Payload)
		case pbclient.WriteStderr:
			loader.Stop()
			os.Stderr.Write(pkt.Payload)
		case pbclient.SessionClose:
			loader.Stop()
			if len(pkt.Payload) > 0 {
				_, _ = os.Stderr.Write([]byte(styles.ClientError(string(pkt.Payload)) + "\n"))
			}
			exitCode, err := strconv.Atoi(string(pkt.Spec[pb.SpecClientExitCodeKey]))
			if err != nil {
				os.Exit(terminal.InternalErrorExitCode)
			}
			os.Exit(exitCode)
		}
	}
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
		envKey := fmt.Sprintf("envvar:%s", strings.ToUpper(key))
		envVar[envKey] = base64.StdEncoding.EncodeToString([]byte(val))
	}
	if len(invalidEnvs) > 0 {
		return nil, fmt.Errorf("invalid client env vars, expected env=var. found=%v", invalidEnvs)
	}
	return envVar, nil
}
