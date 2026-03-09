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
	sid := string(pkt.Spec[pb.SpecGatewaySessionID])
	connParams := a.connectionParams(sid)
	if connParams == nil {
		log.With("sid", sid).Infof("connection params not found")
		a.sendCloseTerm(sid, "internal error, connection params not found", internalExitCode)
		return
	}
	sessionIDKey := fmt.Sprintf(cmdStoreKey, sid)
	cmdObj := a.connStore.Get(sessionIDKey)
	cmd, ok := cmdObj.(libhoop.Proxy)
	if ok {
		// Write to tty stdin content
		if _, err := cmd.Write(pkt.Payload); err != nil {
			log.With("sid", sid).Infof("tty=true - failed copying stdin to tty, err=%v", err)
			a.sendCloseTerm(sid, "", "0")
		}
		return
	}

	spec := map[string][]byte{pb.SpecGatewaySessionID: []byte(sid)}
	stdoutWriter := pb.NewStreamWriter(a.client, pbclient.WriteStdout, spec)

	var dataMaskingEntityTypesData string
	if connParams.DataMaskingEntityTypesData != nil {
		dataMaskingEntityTypesData = string(connParams.DataMaskingEntityTypesData)
	}

	var guardRailRules string
	if connParams.GuardRailRules != nil {
		guardRailRules = string(connParams.GuardRailRules)
	}

	opts := map[string]string{
		"sid":                       sid,
		"dlp_provider":              connParams.DlpProvider,
		"dlp_mode":                  connParams.DlpMode,
		"mspresidio_analyzer_url":   connParams.DlpPresidioAnalyzerURL,
		"mspresidio_anonymizer_url": connParams.DlpPresidioAnonymizerURL,
		"dlp_gcp_credentials":       connParams.DlpGcpRawCredentialsJSON,
		"dlp_info_types":            strings.Join(connParams.DLPInfoTypes, ","),
		"data_masking_entity_data":  dataMaskingEntityTypesData,
		"guard_rail_rules":          guardRailRules,
	}
	args := append(connParams.CmdList, connParams.ClientArgs...)
	cmd, err := libhoop.NewConsole(connParams.EnvVars, args, stdoutWriter, opts)
	if err != nil {
		// should we propagate the error back to the client?
		// In case of sensitive contents in the error this could be propagate back
		// to the client.
		errMsg := fmt.Sprintf("failed executing command, reason=%v", err)
		log.With("sid", sid).Infof("tty=true - %s", errMsg)
		a.sendCloseTerm(sid, errMsg, internalExitCode)
		return
	}
	log.With("sid", sid).Infof("tty=true - executing command:%v", args)
	cmd.Run(func(exitCode int, msg string) {
		if msg != "" {
			log.With("sid", sid).Infof("tty=true, exitcode=%v - err=%v", exitCode, msg)
		}
		a.sendCloseTerm(sid, msg, strconv.Itoa(exitCode))
	})
	a.connStore.Set(sessionIDKey, cmd)

	// Apply any pending window size that arrived before the PTY was created
	pendingKey := fmt.Sprintf(pendingWinSizeKey, sid)
	pendingSize, ok := a.connStore.Get(pendingKey).(*pty.Winsize)
	if !ok {
		log.With("sid", sid).Infof("tty=true - pending window size not found")
		return
	}
	a.connStore.Del(pendingKey)
	term, ok := cmd.(libhoop.Terminal)
	if !ok {
		log.With("sid", sid).Infof("tty=true - terminal not found")
		return
	}
	if err := term.ResizeTTY(pendingSize); err != nil {
		log.With("sid", sid).Infof("tty=true - failed applying pending winsize, err=%v", err)
	} else {
		log.With("sid", sid).Debugf("tty=true - applied pending winsize %vx%v", pendingSize.Rows, pendingSize.Cols)
	}

}

func (a *Agent) doTerminalResizeTTY(pkt *pb.Packet) {
	sid := string(pkt.Spec[pb.SpecGatewaySessionID])
	winSize, err := parsePttyWinSize(pkt.Payload)
	if err != nil {
		sentry.CaptureException(err)
		log.With("sid", sid).Infof("tty=true, winsize=%v - %v", string(pkt.Payload), err)
		return
	}

	sessionIDKey := fmt.Sprintf(cmdStoreKey, sid)
	cmdObj := a.connStore.Get(sessionIDKey)
	cmd, ok := cmdObj.(libhoop.Terminal)
	// the terminal may not be created yet if the client
	// so when the client opens the terminal size get sent before the terminal is created,
	// in that case we store the window size in the connStore and apply it when the terminal gets created
	if !ok {
		pendingKey := fmt.Sprintf(pendingWinSizeKey, sid)
		a.connStore.Set(pendingKey, winSize)
		log.With("sid", sid).Debugf("tty=true - stored pending winsize %vx%v", winSize.Rows, winSize.Cols)
		return
	}
	if err := cmd.ResizeTTY(winSize); err != nil {
		sentry.CaptureException(err)
		log.With("sid", sid).Infof("tty=true - failed resizing tty, err=%v", err)
	}
}

func (a *Agent) sendCloseTerm(sid, msg, exitCode string) {
	if msg != "" && exitCode == "" {
		// must have an exit code if it has a msg
		exitCode = "1"
	}
	log.With("sid", sid).Infof("exitcode=%v - err=%v", exitCode, msg)
	a.sendClientSessionCloseWithExitCode(sid, msg, exitCode)
}

func parsePttyWinSize(winSizeBytes []byte) (*pty.Winsize, error) {
	// [rows, cols, x, y]
	winSizeSlice := strings.Split(string(winSizeBytes), ",")
	if len(winSizeSlice) != 4 {
		return nil, fmt.Errorf("winsize doesn't contain required length (4)")
	}
	for i := range 4 {
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
