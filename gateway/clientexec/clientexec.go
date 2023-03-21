package clientexec

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/runopsio/hoop/common/grpc"
	pb "github.com/runopsio/hoop/common/proto"
	pbagent "github.com/runopsio/hoop/common/proto/agent"
	pbclient "github.com/runopsio/hoop/common/proto/client"
	"github.com/runopsio/hoop/gateway/plugin"
	"github.com/tidwall/wal"
)

var (
	walLogPath       = filepath.Join(plugin.AuditPath, "clientexec")
	walFolderTmpl    = `%s/%s-%s-wal`
	maxResponseBytes = 600000 // 600KB
)

func init() {
	_ = os.MkdirAll(walLogPath, 0755)
}

const nilExitCode = -100

type clientExec struct {
	folderName string
	wlog       *wal.Log
	client     pb.ClientTransport
	sessionID  string
}

func (r *clientExec) SessionID() string {
	return r.sessionID
}

// Close the gRPC connection
func (r *clientExec) Close() {
	r.client.Close()
}

type Response struct {
	ExitCode  *int   `json:"exit_code"`
	SessionID string `json:"session_id"`
	Output    string `json:"output"`
	err       error
}

func (r *Response) exitCode(code int) *Response {
	r.ExitCode = &code
	return r
}

func (r *Response) IsError() bool {
	if r.ExitCode == nil {
		return true
	}
	return *r.ExitCode != 0
}

func (r *Response) ErrorMessage() string {
	if r.err != nil {
		return r.err.Error()
	}
	return r.Output
}

func newError(err error) *Response {
	return &Response{err: err}
}

func New(orgID, accessToken, connectionName string) (*clientExec, error) {
	sessionID := uuid.NewString()
	folderName := fmt.Sprintf(walFolderTmpl, walLogPath, orgID, sessionID)
	wlog, err := wal.Open(folderName, wal.DefaultOptions)
	if err != nil {
		return nil, err
	}
	client, err := grpc.Connect("127.0.0.1:8010", accessToken,
		grpc.WithOption(grpc.OptionConnectionName, connectionName),
		grpc.WithOption("origin", pb.ConnectionOriginClientAPI),
		grpc.WithOption("verb", pb.ClientVerbExec),
		grpc.WithOption("session-id", sessionID))
	if err != nil {
		_ = wlog.Close()
		return nil, err
	}
	return &clientExec{
		folderName: folderName,
		wlog:       wlog,
		client:     client,
		sessionID:  sessionID}, nil
}

func (c *clientExec) Run(inputPayload []byte, clientEnvVars map[string]string) *Response {
	openSessionSpec := map[string][]byte{}
	if len(clientEnvVars) > 0 {
		encEnvVars, err := pb.GobEncode(clientEnvVars)
		if err != nil {
			return newError(err)
		}
		openSessionSpec[pb.SpecClientExecEnvVar] = encEnvVars
	}
	resp := c.run(inputPayload, openSessionSpec)
	resp.SessionID = c.sessionID
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
		return newError(err)
	}
	ctxTimeout, cancel := context.WithTimeout(context.Background(), time.Hour*4)
	defer func() { cancel(); c.wlog.Close(); os.RemoveAll(c.folderName) }()
	for {
		pkt, err := c.client.Recv()
		if err != nil {
			return newError(err)
		}
		if pkt == nil {
			continue
		}
		log.Printf("processing packet %v", pkt.Type)
		switch pkt.Type {
		case pbclient.SessionOpenWaitingApproval:
			log.Printf("waiting task to be approved at %v", string(pkt.Payload))
			go func() {
				// It prevents reviewed sessions to stay open forever.
				// Closing the client will make the Recv method to fail
				<-ctxTimeout.Done()
				log.Printf("task timeout, closing gRPC client ...")
				c.client.Close()
			}()
		case pbclient.SessionOpenApproveOK:
			if err := sendOpenSessionPktFn(); err != nil {
				return newError(err)
			}
		case pbclient.SessionOpenAgentOffline:
			return newError(fmt.Errorf("agent is offline"))
		case pbclient.SessionOpenOK:
			stdinPkt := &pb.Packet{
				Type:    pbagent.ExecWriteStdin,
				Payload: inputPayload,
				Spec: map[string][]byte{
					pb.SpecGatewaySessionID: pkt.Spec[pb.SpecGatewaySessionID],
				},
			}
			if err := c.client.Send(stdinPkt); err != nil {
				return newError(fmt.Errorf("failed executing command, err=%v", err))
			}
		case pbclient.WriteStdout,
			pbclient.WriteStderr:
			if err := c.write(pkt.Payload); err != nil {
				return newError(err)
			}
		case pbclient.SessionClose:
			exitCode, err := strconv.Atoi(string(pkt.Spec[pb.SpecClientExitCodeKey]))
			if err != nil {
				exitCode = nilExitCode
			}
			if err := c.write(pkt.Payload); err != nil {
				return newError(err).exitCode(exitCode)
			}
			output, err := c.readAll()
			return &Response{Output: string(output), err: err, ExitCode: &exitCode}
		default:
			return newError(fmt.Errorf("packet type %v not implemented", pkt.Type))
		}
	}
}

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

func (c *clientExec) readAll() ([]byte, error) {
	var stdoutData []byte
	for i := uint64(1); ; i++ {
		data, err := c.wlog.Read(i)
		if err != nil && err != wal.ErrNotFound {
			return nil, err
		}
		if err == wal.ErrNotFound {
			break
		}
		stdoutData = append(stdoutData, data...)
		if len(stdoutData) > maxResponseBytes {
			stdoutData = stdoutData[0:maxResponseBytes]
			break
		}
	}
	return stdoutData, nil
}
