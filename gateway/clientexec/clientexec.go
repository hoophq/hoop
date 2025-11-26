package clientexec

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/grpc"
	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	pbclient "github.com/hoophq/hoop/common/proto/client"
	"github.com/hoophq/hoop/common/version"
	"github.com/hoophq/hoop/gateway/appconfig"
	sessionwal "github.com/hoophq/hoop/gateway/session/wal"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
	"github.com/tidwall/wal"
)

var (
	walLogPath       = filepath.Join(plugintypes.AuditPath, "clientexec")
	walFolderTmpl    = `%s/%s-%s-wal`
	maxResponseBytes = sessionwal.DefaultMaxRead

	// PlainExecSecretKey is a key to execute plain executions in the gateway securely by this package
	PlainExecSecretKey string = generateSecureRandomKeyOrDie()
)

func init() { _ = os.MkdirAll(walLogPath, 0755) }

const nilExitCode int = -2

func NewTimeoutResponse(sessionID string) *Response {
	return &Response{SessionID: sessionID, ExitCode: nilExitCode, OutputStatus: "running"}
}

type clientExec struct {
	folderName                string
	wlog                      *wal.Log
	client                    pb.ClientTransport
	ctx                       context.Context
	cancelFn                  context.CancelFunc
	connectionCommandOverride []byte
	sessionID                 string
	orgID                     string
}

type Options struct {
	OrgID                     string
	SessionID                 string
	ConnectionName            string
	ConnectionCommandOverride []string
	BearerToken               string
	Origin                    string
	Verb                      string
	UserAgent                 string
}

type Response struct {
	HasReview         bool   `json:"has_review"`
	SessionID         string `json:"session_id"`
	Output            string `json:"output"`
	OutputStatus      string `json:"output_status"`
	Truncated         bool   `json:"truncated"`
	ExecutionTimeMili int64  `json:"execution_time"`
	ExitCode          int    `json:"exit_code"`

	err error
}

func (c *clientExec) GetOrgID() string {
	return c.orgID
}

func (r *Response) String() (s string) {
	s = fmt.Sprintf("exit_code=%v, truncated=%v, has_review=%v, output_length=%v, execution_time_sec=%v",
		fmt.Sprintf("%v", r.ExitCode), r.Truncated, r.HasReview, len(r.Output), r.ExecutionTimeMili/1000)
	// log system errors output
	if r.ExitCode == nilExitCode {
		s = fmt.Sprintf("%s, output=%v", s, r.Output)
	}
	return
}

func (r *Response) setExitCode(code int) *Response {
	r.ExitCode = code
	return r
}

func newRawErr(err error) *Response { return &Response{err: err, ExitCode: nilExitCode} }
func newErr(format string, a ...any) *Response {
	return &Response{err: fmt.Errorf(format, a...), ExitCode: nilExitCode}
}

func newReviewedResponse(reviewURI string) *Response {
	return &Response{
		HasReview: true,
		Output:    reviewURI,
	}
}

func New(opts *Options) (*clientExec, error) {
	if opts.SessionID == "" {
		opts.SessionID = uuid.NewString()
	}

	if opts.Origin == "" {
		opts.Origin = pb.ConnectionOriginClientAPI
	}

	if opts.Verb == "" {
		opts.Verb = pb.ClientVerbExec
	}

	var connectionCommandJson []byte
	if len(opts.ConnectionCommandOverride) > 0 {
		var err error
		connectionCommandJson, err = json.Marshal(opts.ConnectionCommandOverride)
		if err != nil {
			return nil, fmt.Errorf("failed encoding connection command, reason=%v", err)
		}
	}

	folderName := fmt.Sprintf(walFolderTmpl, walLogPath, opts.OrgID, opts.SessionID)
	wlog, err := wal.Open(folderName, wal.DefaultOptions)
	if err != nil {
		return nil, err
	}

	userAgent := fmt.Sprintf("clientexec/%v", version.Get().Version)
	if opts.UserAgent != "" {
		userAgent = opts.UserAgent
	}

	client, err := grpc.Connect(grpc.ClientConfig{
		ServerAddress: grpc.LocalhostAddr,
		Token:         opts.BearerToken,
		UserAgent:     userAgent,
		Insecure:      appconfig.Get().GatewayUseTLS() == false,
		TLSCA:         appconfig.Get().GrpcClientTLSCa(),
		// it should be safe to skip verify here as we are connecting to localhost
		TLSSkipVerify: true,
	},
		grpc.WithOption(grpc.OptionConnectionName, opts.ConnectionName),
		grpc.WithOption("origin", opts.Origin),
		grpc.WithOption("verb", opts.Verb),
		grpc.WithOption("session-id", opts.SessionID),
		grpc.WithOption("plain-exec-key", PlainExecSecretKey),
	)
	if err != nil {
		_ = wlog.Close()
		return nil, err
	}
	ctx, cancelFn := context.WithCancel(context.Background())
	return &clientExec{
		folderName:                folderName,
		wlog:                      wlog,
		client:                    client,
		ctx:                       ctx,
		cancelFn:                  cancelFn,
		sessionID:                 opts.SessionID,
		orgID:                     opts.OrgID,
		connectionCommandOverride: connectionCommandJson,
	}, nil
}

func (c *clientExec) Run(inputPayload []byte, clientEnvVars map[string]string, clientArgs ...string) *Response {
	openSessionSpec := map[string][]byte{}
	if len(clientEnvVars) > 0 {
		encEnvVars, err := pb.GobEncode(clientEnvVars)
		if err != nil {
			return newErr("failed encoding client env vars, reason=%v", err)
		}
		openSessionSpec[pb.SpecClientExecEnvVar] = encEnvVars
	}
	if len(clientArgs) > 0 {
		encClientArgs, err := pb.GobEncode(clientArgs)
		if err != nil {
			return newErr("failed encoding client arguments, reason=%v", err)
		}
		openSessionSpec[pb.SpecClientExecArgsKey] = encClientArgs
	}

	if len(c.connectionCommandOverride) > 0 {
		openSessionSpec[pb.SpecConnectionCommand] = c.connectionCommandOverride
	}

	now := time.Now().UTC()
	resp := c.run(inputPayload, openSessionSpec)
	resp.ExecutionTimeMili = time.Since(now).Milliseconds()
	resp.SessionID = c.sessionID
	resp.OutputStatus = "success"
	if resp.err != nil {
		resp.Output = resp.err.Error()
		resp.OutputStatus = "failed"
	}
	// mark as failed when the exit code is above 0 or different from nil
	if resp.ExitCode != nilExitCode && resp.ExitCode > 0 {
		resp.OutputStatus = "failed"
	}
	return resp
}

func (c *clientExec) run(inputPayload []byte, openSessionSpec map[string][]byte) *Response {
	sendOpenSessionPktFn := func() error {
		return c.client.Send(&pb.Packet{
			Type:    pbagent.SessionOpen,
			Payload: inputPayload,
			Spec:    openSessionSpec,
		})
	}
	if err := sendOpenSessionPktFn(); err != nil {
		return newErr("failed sending open session packet, reason=%v", err)
	}
	defer func() { c.wlog.Close(); os.RemoveAll(c.folderName) }()
	recvCh := grpc.NewStreamRecv(c.ctx, c.client)
	for {
		var dstream *grpc.DataStream
		var ok bool
		select {
		case <-c.ctx.Done():
			return newRawErr(context.Cause(c.ctx))
		case dstream, ok = <-recvCh:
			if !ok {
				return newErr("grpc stream recv closed")
			}
		}

		pkt, err := dstream.Recv()
		if err != nil {
			return newErr(err.Error())
		}
		if pkt == nil {
			continue
		}
		switch pkt.Type {
		case pbclient.SessionOpenWaitingApproval:
			return newReviewedResponse(string(pkt.Payload))
		case pbclient.SessionOpenApproveOK:
			if err := sendOpenSessionPktFn(); err != nil {
				return newErr("failed sending open session packet, reason=%v", err)
			}
		case pbclient.SessionOpenAgentOffline:
			return newErr("agent is offline")
		case pbclient.SessionOpenOK:
			stdinPkt := &pb.Packet{
				Type:    pbagent.ExecWriteStdin,
				Payload: inputPayload,
				Spec: map[string][]byte{
					pb.SpecGatewaySessionID: pkt.Spec[pb.SpecGatewaySessionID],
				},
			}

			if err := c.client.Send(stdinPkt); err != nil {
				return newErr("failed executing command, reason=%v", err)
			}
		case pbclient.WriteStdout, pbclient.WriteStderr:
			if err := c.write(pkt.Payload); err != nil {
				return newErr("failed writing payload to log, reason=%v", err)
			}
		case pbclient.SessionClose:
			exitCode, err := strconv.Atoi(string(pkt.Spec[pb.SpecClientExitCodeKey]))
			if err != nil {
				exitCode = nilExitCode
			}

			if err := c.write(pkt.Payload); err != nil {
				return newErr("failed writing last payload to log, reason=%v", err).
					setExitCode(exitCode)
			}
			output, isTrunc, err := c.readAll()
			if err != nil {
				return newErr("failed reading output response, reason=%v", err).
					setExitCode(exitCode)
			}

			return &Response{
				Output:    string(output),
				ExitCode:  exitCode,
				Truncated: isTrunc,
			}
		default:
			return newErr("packet type %v not implemented", pkt.Type)
		}
	}
}

func (c *clientExec) Close() { c.client.Close(); c.cancelFn() }
func (c *clientExec) write(input []byte) error {
	if len(input) == 0 {
		return nil
	}
	lastIndex, err := c.wlog.LastIndex()
	if err != nil {
		return fmt.Errorf("failed retrieving file content, lastindex=%v, err=%v", lastIndex, err)
	}
	return c.wlog.Write(lastIndex+1, input)
}

func (c *clientExec) readAll() ([]byte, bool, error) {
	var stdoutData []byte
	isTruncated := false
	for i := uint64(1); ; i++ {
		data, err := c.wlog.Read(i)
		if err != nil && err != wal.ErrNotFound {
			return nil, false, err
		}
		if err == wal.ErrNotFound {
			break
		}
		stdoutData = append(stdoutData, data...)
		if len(stdoutData) > maxResponseBytes {
			stdoutData = stdoutData[0:maxResponseBytes]
			isTruncated = true
			break
		}
	}

	return stdoutData, isTruncated, nil
}

func generateSecureRandomKeyOrDie() string {
	secretRandomBytes := make([]byte, 32)
	if _, err := rand.Read(secretRandomBytes); err != nil {
		log.Fatalf("failed generating entropy, err=%v", err)
	}
	secretKey := base64.RawURLEncoding.EncodeToString(secretRandomBytes)
	h := sha256.New()
	if _, err := h.Write([]byte(secretKey)); err != nil {
		log.Fatalf("failed hashing secret key, err=%v", err)
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}
