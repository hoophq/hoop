package resourcemanager

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"libhoop"
	"sync"
	"text/template"
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
	var req pbsystem.ResourceManagerRequest
	if err := json.Unmarshal(pkt.Payload, &req); err != nil {
		sendResponse(client, newError(sid, "unable to decode payload: %v", err))
		return
	}

	renderedScript, err := renderScript(req.Script, req.TemplateData)
	if err != nil {
		sendResponse(client, newError(sid, "failed rendering script template: %v", err))
		return
	}

	stdout, stdoutw := io.Pipe()
	stderr, stderrw := io.Pipe()
	cmd, err := libhoop.NewAdHocExec(
		toLibhoopEnvVars(req.EnvVars),
		req.Command,
		[]byte(renderedScript),
		stdoutw,
		stderrw,
		nil)
	if err != nil {
		sendResponse(client, newError(sid, "failed executing script: %v", err))
		return
	}

	log.With("sid", sid).Infof("executing resource manager script, command=%v, scriptsize=%v",
		req.Command, len(renderedScript))

	startedAt := time.Now().UTC()
	output := &outputSafeWriter{buf: bytes.NewBufferString("")}

	// CAUTION: stdout and stderr streams are not merged based on their actual arrival time.
	// Due to limitations in the underlying terminal package, the output may display stderr
	// content out of sequence relative to stdout.
	stdoutCh := copyBuffer(output, stdout, 4096, "stdout-reader")
	stderrCh := copyBuffer(output, stderr, 4096, "stderr-reader")
	cmd.Run(func(exitCode int, errMsg string) {
		_ = stdoutw.Close()
		_ = stderrw.Close()

		outputContent := output.String()
		if len(outputContent) > maxOutputBytes {
			remainingBytes := len(outputContent[maxOutputBytes:])
			outputContent = outputContent[:maxOutputBytes]
			outputContent += fmt.Sprintf(" [truncated %v byte(s)]", remainingBytes)
		}

		status := pbsystem.StatusCompletedType
		if exitCode != 0 {
			status = pbsystem.StatusFailedType
		}

		log.With("sid", sid).Infof("resource manager script finished, exit_code=%v, elapsed=%v, err=%v",
			exitCode, time.Since(startedAt).Round(time.Millisecond), errMsg)

		sendResponse(client, &pbsystem.ResourceManagerResponse{
			SessionID: sid,
			Status:    status,
			Message:   outputContent,
		})
	})
	<-stdoutCh
	<-stderrCh
}

func renderScript(script string, data map[string]any) (string, error) {
	tmpl, err := template.New("script").Parse(script)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// toLibhoopEnvVars converts the plain KEY=value map from the request into the
// "envvar:KEY" → base64(value) format expected by libhoop.NewAdHocExec.
func toLibhoopEnvVars(envVars map[string]string) map[string]any {
	result := make(map[string]any, len(envVars))
	for k, v := range envVars {
		result["envvar:"+k] = base64.StdEncoding.EncodeToString([]byte(v))
	}
	return result
}

func sendResponse(client pb.ClientTransport, resp *pbsystem.ResourceManagerResponse) {
	payload, pbType, err := resp.Encode()
	if err != nil {
		log.With("sid", resp.SessionID).Warnf("failed encoding resource manager response: %v", err)
		return
	}
	if err := client.Send(&pb.Packet{
		Type:    pbType,
		Payload: payload,
		Spec:    map[string][]byte{pb.SpecGatewaySessionID: []byte(resp.SessionID)},
	}); err != nil {
		log.With("sid", resp.SessionID).Warnf("failed sending resource manager response: %v", err)
	}
}

func newError(sid, format string, a ...any) *pbsystem.ResourceManagerResponse {
	return pbsystem.NewResourceManagerError(sid, format, a...)
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
		log.Infof("[%s] done copying resource manager stream, written=%v, err=%v", stream, wb, err)
		close(doneCh)
	}()
	return doneCh
}
