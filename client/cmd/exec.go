package cmd

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/briandowns/spinner"
	"github.com/getsentry/sentry-go"
	"github.com/hoophq/hoop/client/cmd/styles"
	clientconfig "github.com/hoophq/hoop/client/config"
	"github.com/hoophq/hoop/common/httpclient"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	pbclient "github.com/hoophq/hoop/common/proto/client"
	"github.com/hoophq/hoop/common/version"
	"github.com/spf13/cobra"
)

var inputFilepath string
var inputStdin string
var autoExec bool
var silentMode bool
var execSessionID string

var execExampleDesc = `hoop exec bash -i 'env'
hoop exec bash -e MYENV=val --input 'env' -- --verbose
hoop exec bash <<< 'env'
hoop exec --session <session_id> --output json
`

// execCmd represents the exec command
var execCmd = &cobra.Command{
	Use:     "exec CONNECTION",
	Short:   "Execute a given input in a remote resource",
	Example: execExampleDesc,
	PreRun: func(cmd *cobra.Command, args []string) {
		if execSessionID != "" {
			return
		}
		if len(args) < 1 {
			cmd.Usage()
			os.Exit(1)
		}
	},
	Run: func(cmd *cobra.Command, args []string) {
		if execSessionID != "" {
			runExecSession(execSessionID)
			return
		}
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
	execCmd.Flags().BoolVarP(&silentMode, "silent", "s", false, "Silent mode")
	execCmd.Flags().StringVarP(&outputFlag, "output", "o", "", "Output format. One of: (json)")
	execCmd.Flags().StringVar(&execSessionID, "session", "", "Execute an approved reviewed session by its ID")
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
	jsonMode := outputFlag == "json"
	if jsonMode {
		autoExec = true
	}

	config := clientconfig.GetClientConfigOrDie()
	loader := spinner.New(spinner.CharSets[11], 70*time.Millisecond,
		spinner.WithWriter(os.Stderr), spinner.WithHiddenCursor(true))
	loader.Color("green")
	loader.Suffix = " running ..."
	if !jsonMode {
		loader.Start()
	}
	done := make(chan os.Signal, 1)
	signal.Notify(done, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		for {
			switch <-done {
			case syscall.SIGTERM, syscall.SIGINT:
				loader.Stop()
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
			fatalErr(jsonMode, "failed opening session with gateway, err=%v", err)
		}
	}
	sendOpenSessionPktFn()

	var stdoutBuf, stderrBuf bytes.Buffer
	agentOfflineRetryCounter := 1
	for {
		pkt, err := c.client.Recv()
		if err != nil && jsonMode {
			fatalErr(true, err.Error())
		}
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
			if jsonMode {
				sessionID := string(pkt.Spec[pb.SpecGatewaySessionID])
				emitWaitingApprovalAndExit(sessionID, string(pkt.Payload),
					"poll status with: hoop admin get sessions "+sessionID+" -o json | check review.status field, then run: hoop exec --session "+sessionID+" --output json")
			} else {
				loader.Color("yellow")
				if !loader.Active() {
					loader.Start()
				}
				loader.Suffix = " waiting command to be approved at " +
					styles.Keyword(fmt.Sprintf(" %v ", string(pkt.Payload)))
			}
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
			if jsonMode {
				emitApproved()
			} else {
				loader.Color("green")
				loader.Suffix = " command approved, running ... "
			}
			sendOpenSessionPktFn()
		case pbclient.SessionOpenAgentOffline:
			if agentOfflineRetryCounter > 60 {
				if jsonMode {
					fatalErr(true, "agent is offline, max retry reached")
				}
				c.processGracefulExit(errors.New("agent is offline, max retry reached"))
			}
			if jsonMode {
				emitAgentOffline(agentOfflineRetryCounter)
			} else {
				loader.Color("red")
				loader.Suffix = fmt.Sprintf(" agent is offline, retrying in 30s (%v/60) ... ", agentOfflineRetryCounter)
			}
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
			if jsonMode {
				sid := string(pkt.Spec[pb.SpecGatewaySessionID])
				cmd := string(pkt.Spec[pb.SpecClientExecCommandKey])
				emitJSONEvent(os.Stdout, JSONEvent{
					Status: "running",
					Data: map[string]string{
						"session_id": sid,
						"command":    cmd,
					},
				})
			} else if !silentMode {
				c.loader.Stop()
				cmd := string(pkt.Spec[pb.SpecClientExecCommandKey])
				sid := string(pkt.Spec[pb.SpecGatewaySessionID])
				out := styles.Fainted("<stdin-input> | %s (the input is piped to this command)", cmd)
				if debugFlag {
					out = styles.Fainted("<stdin-input> | %s (the input is piped to this command) | session: %s", cmd, sid)
				}
				if cmd == "" {
					out = styles.Fainted("session: %s", sid)
				}
				fmt.Fprintln(os.Stderr, out)
				c.loader.Start()
			}
			if err := c.client.Send(stdinPkt); err != nil {
				fatalErr(jsonMode, "failed executing command, err=%v", err)
			}
		case pbclient.WriteStdout:
			loader.Stop()
			if jsonMode {
				stdoutBuf.Write(pkt.Payload)
			} else {
				os.Stdout.Write(pkt.Payload)
			}
		case pbclient.WriteStderr:
			loader.Stop()
			if jsonMode {
				stderrBuf.Write(pkt.Payload)
			} else {
				os.Stderr.Write(pkt.Payload)
			}
		case pbclient.SessionClose:
			loader.Stop()
			exitCode, err := strconv.Atoi(string(pkt.Spec[pb.SpecClientExitCodeKey]))
			if err != nil {
				exitCode = 254
			}
			if jsonMode {
				data := map[string]string{}
				if stdoutBuf.Len() > 0 {
					data["stdout"] = stdoutBuf.String()
				}
				if stderrBuf.Len() > 0 {
					data["stderr"] = stderrBuf.String()
				}
				if len(pkt.Payload) > 0 {
					data["error"] = string(pkt.Payload)
				}
				emitJSONEvent(os.Stdout, JSONEvent{
					Status:   "completed",
					Message:  "session closed",
					ExitCode: &exitCode,
					Data:     data,
				})
				os.Exit(exitCode)
			}
			if len(pkt.Payload) > 0 {
				_, _ = os.Stderr.Write([]byte(styles.ClientError(string(pkt.Payload)) + "\n"))
			}
			os.Exit(exitCode)
		}
	}
}

type execSessionResponse struct {
	ExitCode     int    `json:"exit_code"`
	Output       string `json:"output"`
	OutputStatus string `json:"output_status"`
}

// runExecSession executes an already-approved reviewed session via the API.
// Used by agents to trigger execution after polling for approval.
func runExecSession(sessionID string) {
	jsonMode := outputFlag == "json"
	config := clientconfig.GetClientConfigOrDie()

	req, err := http.NewRequest(http.MethodPost,
		fmt.Sprintf("%s/api/sessions/%s/exec", config.ApiURL, sessionID), nil)
	if err != nil {
		fatalErr(jsonMode, err.Error())
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", config.Token))
	if config.IsApiKey() {
		req.Header.Set("Api-Key", config.Token)
	}
	req.Header.Set("User-Agent", fmt.Sprintf("hoopcli/%v", version.Get().Version))

	resp, err := httpclient.NewHttpClient(config.TlsCA()).Do(req)
	if err != nil {
		fatalErr(jsonMode, err.Error())
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		fatalErr(jsonMode, "failed executing session: %s", string(body))
	}
	if !jsonMode {
		fmt.Print(string(body))
		return
	}

	var apiResp execSessionResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		fatalErr(jsonMode, "parsing response: %v", err)
	}

	status := "completed"
	if resp.StatusCode == http.StatusAccepted {
		status = "running"
	}
	data := map[string]string{"session_id": sessionID}
	if apiResp.Output != "" {
		data["stdout"] = apiResp.Output
	}
	if apiResp.OutputStatus != "" {
		data["output_status"] = apiResp.OutputStatus
	}
	emitJSONEvent(os.Stdout, JSONEvent{
		Status:   status,
		ExitCode: &apiResp.ExitCode,
		Data:     data,
	})
	os.Exit(apiResp.ExitCode)
}
