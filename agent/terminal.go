package agent

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"syscall"

	"github.com/creack/pty"
	"github.com/hoophq/pluginhooks"
	"github.com/runopsio/hoop/agent/dlp"
	term "github.com/runopsio/hoop/agent/terminal"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/common/runtime"
)

func (a *Agent) doTerminalWriteAgentStdin(pkt *pb.Packet) {
	sessionID := string(pkt.Spec[pb.SpecGatewaySessionID])
	connParams, pluginHooks := a.connectionParams(sessionID)
	if connParams == nil {
		log.Printf("session=%s - connection params not found", sessionID)
		a.sendCloseTerm(sessionID, "internal error, connection params not found", "")
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
			a.sendCloseTerm(sessionID, msg, "")
			return
		}
		if len(mutatePayload) > 0 {
			pkt.Payload = mutatePayload
		}
		// Write to tty stdin content
		if err := cmd.WriteTTY(pkt.Payload); err != nil {
			log.Printf("session=%v | tty=true - failed copying stdin to tty, err=%v", string(sessionID), err)
			a.sendCloseTerm(sessionID, "", "")
		}
		return
	}

	cmd, err := term.NewCommand(connParams.EnvVars, append(connParams.CmdList, connParams.ClientArgs...)...)
	if err != nil {
		log.Printf("session=%s, tty=true - failed executing command, err=%v", sessionID, err)
		a.sendCloseTerm(sessionID, "failed executing command", "")
		return
	}
	log.Printf("session=%s, tty=true - executing command %q", sessionID, cmd.String())
	spec := map[string][]byte{pb.SpecGatewaySessionID: []byte(sessionID)}
	onExecErr := func(exitCode int, errMsg string, v ...any) {
		a.sendCloseTerm(sessionID, fmt.Sprintf(errMsg, v...), strconv.Itoa(exitCode))
	}
	stdoutWriter := pb.NewHookStreamWriter(a.client, pb.PacketTerminalClientWriteStdoutType, spec, pluginHooks)
	if dlpClient, ok := a.connStore.Get(dlpClientKey).(dlp.Client); ok {
		stdoutWriter = dlp.NewDLPStreamWriter(
			a.client,
			pluginHooks,
			dlpClient,
			pb.PacketTerminalClientWriteStdoutType,
			spec,
			connParams.DLPInfoTypes)
	}
	if err := cmd.RunOnTTY(stdoutWriter, onExecErr); err != nil {
		log.Printf("session=%s, tty=true - err=%v", string(sessionID), err)
	}
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
			log.Printf("session=%s, tty=true, winsize=%v - %v", sessionID, string(pkt.Payload), err)
			return
		}
		if err := cmd.ResizeTTY(winSize); err != nil {
			log.Printf("session=%s, tty=true - failed resizing tty, err=%v", sessionID, err)
		}
	}
}

func (a *Agent) doTerminalCloseTerm(pkt *pb.Packet) {
	sessionID := pkt.Spec[pb.SpecGatewaySessionID]
	log.Printf("session=%v - received %v", string(sessionID), pb.PacketTerminalCloseType)
	procPidObj := a.connStore.Get(fmt.Sprintf("proc:%s", sessionID))
	if procPid, _ := procPidObj.(int); procPid > 0 {
		log.Printf("sending SIGINT signal to process %v ...", procPid)
		go runtime.Kill(procPid, syscall.SIGINT)
	}
	a.killHookPlugins(string(sessionID))
}

func (a *Agent) sendCloseTerm(sessionID, msg string, exitCode string) {
	spec := map[string][]byte{pb.SpecGatewaySessionID: []byte(sessionID)}
	if exitCode != "" {
		spec[pb.SpecClientExecExitCodeKey] = []byte(exitCode)
	}
	_, _ = pb.NewStreamWriter(a.client, pb.PacketTerminalCloseType, spec).Write([]byte(msg))
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
