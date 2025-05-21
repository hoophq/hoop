package runbookhook

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"libhoop"
	"sync"
	"time"

	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	pbsystem "github.com/hoophq/hoop/common/proto/system"
)

const maxOutputBytes int = 4096

func ProcessRequest(client pb.ClientTransport, pkt *pb.Packet) {
	go processRequest(client, pkt)
}

func processRequest(client pb.ClientTransport, pkt *pb.Packet) {
	sid := string(pkt.Spec[pb.SpecGatewaySessionID])
	var req pbsystem.RunbookHookRequest
	if err := json.Unmarshal(pkt.Payload, &req); err != nil {
		sendResponse(client, newError(sid, "unable to decode payload: %v", err))
		return
	}

	startedExecutionAt := time.Now().UTC()
	stdout, stdoutw := io.Pipe()
	stderr, stderrw := io.Pipe()
	cmd, err := libhoop.NewAdHocExec(
		map[string]any{
			"envvar:HOOP_RUNBOOK_HOOK_PAYLOAD": base64.StdEncoding.EncodeToString(pkt.Payload),
		},
		req.Command,
		[]byte(req.InputFile),
		stdoutw,
		stderrw,
		nil)
	if err != nil {
		sendResponse(client, newError(sid, "failed executing runbook hook, reason=%v", err))
		return
	}

	log.With("sid", sid).Infof("starting executing runbook hook, command=%v, inputsize=%v",
		req.Command, len(req.InputFile))
	output := &outputSafeWriter{buf: bytes.NewBufferString("")}

	// CAUTION: stdout and stderr streams are not merged based on their actual arrival time.
	// Due to limitations in the underlying terminal package, the output may display stderr
	// content out of sequence relative to stdout. This can make debugging difficult as
	// error messages might appear before or after their triggering output rather than
	// precisely when they occurred during execution.
	stdoutCh := copyBuffer(output, stdout, 4096, "stdout-reader")
	stderrCh := copyBuffer(output, stderr, 4096, "stderr-reader")
	cmd.Run(func(exitCode int, errMsg string) {
		_ = stdoutw.Close()
		_ = stderrw.Close()

		// truncate at 4096 bytes
		outputContent := output.String()
		if len(outputContent) > maxOutputBytes {
			remainingBytes := len(outputContent[maxOutputBytes:])
			outputContent = outputContent[:maxOutputBytes]
			outputContent += fmt.Sprintf(" [truncated %v byte(s)]", remainingBytes)
		}
		log.With("sid", sid).Infof("finish executing runbook hook on callback, exit_code=%v, err=%v, output=%v",
			exitCode, errMsg, outputContent)
		sendResponse(client, &pbsystem.RunbookHookResponse{
			ID:               sid,
			ExitCode:         exitCode,
			Output:           outputContent,
			ExecutionTimeSec: int(time.Since(startedExecutionAt).Seconds()),
		})
	})
	<-stdoutCh
	<-stderrCh

}

func sendResponse(client pb.ClientTransport, response *pbsystem.RunbookHookResponse) {
	payload, err := json.Marshal(response)
	if err != nil {
		log.With("sid", response.ID).Warnf("failed encoding runbook hook response, reason=%v", err)
		return
	}
	err = client.Send(&pb.Packet{
		Type:    pbsystem.RunbookHookResponseType,
		Payload: payload,
		Spec: map[string][]byte{
			pb.SpecGatewaySessionID: []byte(response.ID),
		},
	})
	if err != nil {
		log.With("sid", response.ID).Warnf("failed sending runbook hook response to stream, reason=%v", err)
	}
}

func newError(sid, format string, a ...any) *pbsystem.RunbookHookResponse {
	return &pbsystem.RunbookHookResponse{
		ID:               sid,
		ExitCode:         -2,
		ExecutionTimeSec: 0,
		Output:           fmt.Sprintf(format, a...),
	}
}

type outputSafeWriter struct {
	buf *bytes.Buffer
	mu  sync.Mutex
}

func (w *outputSafeWriter) Write(data []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(data)
}

func (w *outputSafeWriter) String() string { return w.buf.String() }
func (w *outputSafeWriter) Len() int       { return w.buf.Len() }

func copyBuffer(dst io.Writer, src io.Reader, bufSize int, stream string) chan struct{} {
	doneCh := make(chan struct{})
	go func() {
		wb, err := io.CopyBuffer(dst, src, make([]byte, bufSize))
		log.Infof("[%s] - done copying runbook hook stream, written=%v, err=%v", stream, wb, err)
		close(doneCh)
	}()
	return doneCh
}
