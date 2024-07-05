package controller

import (
	"fmt"
	"libhoop"
	"strconv"
	"strings"

	"github.com/hoophq/hoop/common/log"

	"github.com/creack/pty"
	"github.com/getsentry/sentry-go"
	pb "github.com/hoophq/hoop/common/proto"
	pbclient "github.com/hoophq/hoop/common/proto/client"
)

func (a *Agent) doTerminalWriteAgentStdin(pkt *pb.Packet) {
	sessionID := string(pkt.Spec[pb.SpecGatewaySessionID])
	connParams := a.connectionParams(sessionID)
	if connParams == nil {
		log.Infof("session=%s - connection params not found", sessionID)
		a.sendCloseTerm(sessionID, "internal error, connection params not found", "1")
		return
	}
	sessionIDKey := fmt.Sprintf(cmdStoreKey, sessionID)
	cmdObj := a.connStore.Get(sessionIDKey)
	cmd, ok := cmdObj.(libhoop.Proxy)
	if ok {
		// Write to tty stdin content
		if _, err := cmd.Write(pkt.Payload); err != nil {
			log.Infof("session=%v | tty=true - failed copying stdin to tty, err=%v", string(sessionID), err)
			a.sendCloseTerm(sessionID, "", "0")
		}
		return
	}

	spec := map[string][]byte{pb.SpecGatewaySessionID: []byte(sessionID)}
	stdoutWriter := pb.NewStreamWriter(a.client, pbclient.WriteStdout, spec)
	opts := map[string]string{
		"dlp_gcp_credentials": a.getGCPCredentials(),
		"dlp_info_types":      strings.Join(connParams.DLPInfoTypes, ","),
	}
	args := append(connParams.CmdList, connParams.ClientArgs...)
	cmd, err := libhoop.NewConsole(connParams.EnvVars, args, stdoutWriter, opts)
	// cmd, err := term.NewCommand(connParams.EnvVars, append(connParams.CmdList, connParams.ClientArgs...)...)
	if err != nil {
		// should we propagate the error back to the client?
		// In case of sensitive contents in the error this could be propagate back
		// to the client.
		errMsg := fmt.Sprintf("failed executing command, reason=%v", err)
		log.Infof("session=%s, tty=true - %s", sessionID, errMsg)
		a.sendCloseTerm(sessionID, errMsg, "1")
		return
	}
	log.Infof("session=%s, tty=true - executing command:%v", sessionID, cmd)
	cmd.Run(func(exitCode int, msg string) {
		if msg != "" {
			log.Infof("session=%s, tty=true, exitcode=%v - err=%v", string(sessionID), exitCode, msg)
		}
		a.sendCloseTerm(sessionID, msg, strconv.Itoa(exitCode))
	})
	a.connStore.Set(sessionIDKey, cmd)
}

func (a *Agent) doTerminalResizeTTY(pkt *pb.Packet) {
	sessionID := string(pkt.Spec[pb.SpecGatewaySessionID])
	sessionIDKey := fmt.Sprintf(cmdStoreKey, sessionID)
	cmdObj := a.connStore.Get(sessionIDKey)
	cmd, ok := cmdObj.(libhoop.Terminal)
	if ok {
		winSize, err := parsePttyWinSize(pkt.Payload)
		if err != nil {
			sentry.CaptureException(err)
			log.Infof("session=%s, tty=true, winsize=%v - %v", sessionID, string(pkt.Payload), err)
			return
		}
		if err := cmd.ResizeTTY(winSize); err != nil {
			sentry.CaptureException(err)
			log.Infof("session=%s, tty=true - failed resizing tty, err=%v", sessionID, err)
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
