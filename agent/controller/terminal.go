package controller

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/runopsio/hoop/common/log"

	"github.com/creack/pty"
	"github.com/getsentry/sentry-go"
	"github.com/hoophq/pluginhooks"
	"github.com/runopsio/hoop/agent/dlp"
	term "github.com/runopsio/hoop/agent/terminal"
	pb "github.com/runopsio/hoop/common/proto"
	pbclient "github.com/runopsio/hoop/common/proto/client"
)

func (a *Agent) doTerminalWriteAgentStdin(pkt *pb.Packet) {
	sessionID := string(pkt.Spec[pb.SpecGatewaySessionID])
	connParams, pluginHooks := a.connectionParams(sessionID)
	if connParams == nil {
		log.Printf("session=%s - connection params not found", sessionID)
		a.sendCloseTerm(sessionID, "internal error, connection params not found", "1")
		return
	}
	sessionIDKey := fmt.Sprintf(cmdStoreKey, sessionID)
	cmdObj := a.connStore.Get(sessionIDKey)
	cmd, ok := cmdObj.(*term.Command)
	if ok {
		mutatePayload, err := pluginHooks.ExecRPCOnRecv(&pluginhooks.Request{
			SessionID:  sessionID,
			PacketType: pkt.Type,
			Payload:    pkt.Payload,
		})
		if err != nil {
			msg := fmt.Sprintf("failed processing hook, reason=%v", err)
			log.Println(msg)
			a.sendCloseTerm(sessionID, msg, "1")
			return
		}
		if len(mutatePayload) > 0 {
			pkt.Payload = mutatePayload
		}
		// Write to tty stdin content
		if err := cmd.WriteTTY(pkt.Payload); err != nil {
			log.Printf("session=%v | tty=true - failed copying stdin to tty, err=%v", string(sessionID), err)
			a.sendCloseTerm(sessionID, "", "0")
		}
		return
	}

	cmd, err := term.NewCommand(connParams.EnvVars, append(connParams.CmdList, connParams.ClientArgs...)...)
	if err != nil {
		// should we propagate the error back to the client?
		// In case of sensitive contents in the error this could be propagate back
		// to the client.
		errMsg := fmt.Sprintf("failed executing command, reason=%v", err)
		log.Printf("session=%s, tty=true - %s", sessionID, errMsg)
		a.sendCloseTerm(sessionID, errMsg, "1")
		return
	}
	log.Printf("session=%s, tty=true - executing command:%v", sessionID, cmd.String())
	spec := map[string][]byte{pb.SpecGatewaySessionID: []byte(sessionID)}
	stdoutWriter := pb.NewHookStreamWriter(a.client, pbclient.WriteStdout, spec, pluginHooks)
	if dlpClient, ok := a.connStore.Get(dlpClientKey).(dlp.Client); ok {
		stdoutWriter = dlp.NewDLPStreamWriter(
			a.client,
			pluginHooks,
			dlpClient,
			pbclient.WriteStdout,
			spec,
			connParams.DLPInfoTypes)
	}
	cmd.RunOnTTY(stdoutWriter, func(exitCode int, msg string, v ...any) {
		var errMsg string
		if msg != "" {
			errMsg = fmt.Sprintf(msg, v...)
			log.Printf("session=%s, tty=true, exitcode=%v - err=%v", string(sessionID), exitCode, errMsg)
		}
		a.sendCloseTerm(sessionID, errMsg, strconv.Itoa(exitCode))
	})
	a.connStore.Set(sessionIDKey, cmd)
}

func (a *Agent) doTerminalResizeTTY(pkt *pb.Packet) {
	sessionID := string(pkt.Spec[pb.SpecGatewaySessionID])
	sessionIDKey := fmt.Sprintf(cmdStoreKey, sessionID)
	cmdObj := a.connStore.Get(sessionIDKey)
	cmd, ok := cmdObj.(*term.Command)
	if ok {
		winSize, err := parsePttyWinSize(pkt.Payload)
		if err != nil {
			sentry.CaptureException(err)
			log.Printf("session=%s, tty=true, winsize=%v - %v", sessionID, string(pkt.Payload), err)
			return
		}
		if err := cmd.ResizeTTY(winSize); err != nil {
			sentry.CaptureException(err)
			log.Printf("session=%s, tty=true - failed resizing tty, err=%v", sessionID, err)
		}
	}
}

func (a *Agent) sendCloseTerm(sessionID, msg, exitCode string) {
	if msg != "" && exitCode == "" {
		// must have an exit code if it has a msg
		exitCode = "1"
	}
	var exitCodeKeyVal string
	if exitCode != "" {
		exitCodeKeyVal = fmt.Sprintf("%s=%s", pb.SpecClientExitCodeKey, exitCode)
	}
	a.sendClientSessionClose(sessionID, msg, exitCodeKeyVal)
}

func parsePttyWinSize(winSizeBytes []byte) (*pty.Winsize, error) {
	// [rows, cols, x, y]
	winSizeSlice := strings.Split(string(winSizeBytes), ",")
	if len(winSizeSlice) != 4 {
		return nil, fmt.Errorf("winsize doesn't contain required length (4)")
	}
	for i := 0; i < 4; i++ {
		if _, err := strconv.Atoi(winSizeSlice[i]); err != nil {
			return nil, fmt.Errorf("failed parsing size (%v), err=%v", i, err)
		}
	}
	atoiFn := func(strInt32 string) uint16 { v, _ := strconv.Atoi(strInt32); return uint16(v) }
	return &pty.Winsize{
		Rows: atoiFn(winSizeSlice[0]),
		Cols: atoiFn(winSizeSlice[1]),
		X:    atoiFn(winSizeSlice[2]),
		Y:    atoiFn(winSizeSlice[3]),
	}, nil
}
