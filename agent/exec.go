package agent

import (
	"fmt"
	"log"
	"strconv"
	"syscall"

	exec "github.com/runopsio/hoop/common/exec"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/common/runtime"
)

const (
	connectionStoreParamsKey string = "params:%s"
	procStoreKey             string = "proc:%s"
	cmdStoreKey              string = "cmd:%s"
)

func (a *Agent) processExec(pkt *pb.Packet) {
	switch pb.PacketType(pkt.Type) {
	case pb.PacketExecRunProcType:
		a.doExecRunProc(pkt)
	case pb.PacketExecWriteAgentStdinType:
		a.doExecWriteAgentStdin(pkt)
	case pb.PacketExecCloseTermType:
		a.doExecCloseTerm(pkt)
	}
}

func (a *Agent) doExecRunProc(pkt *pb.Packet) {
	sessionID := pkt.Spec[pb.SpecGatewaySessionID]
	encConnectionParams := pkt.Spec[pb.SpecAgentConnectionParamsKey]
	var connParams pb.AgentConnectionParams
	if err := pb.GobDecodeInto(encConnectionParams, &connParams); err != nil {
		// TODO: send error
		log.Printf("failed decoding connection params=%#v, err=%v", encConnectionParams, err)
		_, _ = pb.NewStreamWriter(a.client, pb.PacketExecCloseTermType, map[string][]byte{
			pb.SpecGatewaySessionID: sessionID,
		}).Write([]byte(`internal error, failed decoding connection params`))
		return
	}
	cmd, err := exec.NewCommand(connParams.EnvVars,
		append(connParams.CmdList, connParams.ClientArgs...)...)
	if err != nil {
		log.Printf("failed executing command, err=%v", err)
		_, _ = pb.NewStreamWriter(a.client, pb.PacketExecCloseTermType, map[string][]byte{
			pb.SpecGatewaySessionID: sessionID,
		}).Write([]byte(`failed executing command`))
		return
	}
	log.Printf("session=%v, tty=false - executing command=%q", string(sessionID), cmd.String())
	spec := map[string][]byte{pb.SpecGatewaySessionID: sessionID}
	stdoutWriter := pb.NewStreamWriter(a.client, pb.PacketExecClientWriteStdoutType, spec)
	onExecEnd := func(exitCode int, errMsg string, v ...any) {
		errMsg = fmt.Sprintf(errMsg, v...)
		spec[pb.SpecClientExecExitCodeKey] = []byte(strconv.Itoa(exitCode))
		_, _ = pb.NewStreamWriter(a.client, pb.PacketExecCloseTermType, spec).
			Write([]byte(errMsg))
	}
	// TODO: add client args
	if err = cmd.Run(stdoutWriter, pkt.Payload, onExecEnd); err != nil {
		log.Printf("session=%v - err=%v", string(sessionID), err)
	}
	a.connStore.Set(fmt.Sprintf("proc:%v", sessionID), cmd.Pid())
}

func (a *Agent) doExecWriteAgentStdin(pkt *pb.Packet) {
	sessionID := string(pkt.Spec[pb.SpecGatewaySessionID])
	sessionIDKey := fmt.Sprintf(cmdStoreKey, sessionID)
	cmdObj := a.connStore.Get(sessionIDKey)
	cmd, ok := cmdObj.(*exec.Command)
	if ok {
		// Write to tty stdin content
		if _, err := cmd.WriteTTY(pkt.Payload); err != nil {
			log.Printf("session=%v | tty=true - failed copying stdin to tty, err=%v", string(sessionID), err)
			a.sendCloseTerm(sessionID, "", "")
		}
		return
	}
	connParamsObj := a.connStore.Get(fmt.Sprintf(connectionStoreParamsKey, string(sessionID)))
	connParams, ok := connParamsObj.(*pb.AgentConnectionParams)
	if !ok {
		log.Printf("session=%s - connection params not found", sessionID)
		a.sendCloseTerm(sessionID, "internal error, connection params not found", "")
		return
	}

	cmd, err := exec.NewCommand(connParams.EnvVars,
		append(connParams.CmdList, connParams.ClientArgs...)...)
	if err != nil {
		log.Printf("session=%s, tty=true - failed executing command, err=%v", sessionID, err)
		a.sendCloseTerm(sessionID, "failed executing command", "")
		return
	}
	log.Printf("session=%s, tty=true - executing command %q", sessionID, cmd.String())
	spec := map[string][]byte{pb.SpecGatewaySessionID: []byte(sessionID)}
	onExecEnd := func(exitCode int, errMsg string, v ...any) {
		a.sendCloseTerm(sessionID, fmt.Sprintf(errMsg, v...), strconv.Itoa(exitCode))
	}
	stdoutWriter := pb.NewStreamWriter(a.client, pb.PacketExecClientWriteStdoutType, spec)
	if err := cmd.RunOnTTY(stdoutWriter, onExecEnd); err != nil {
		log.Printf("session=%s, tty=true - err=%v", string(sessionID), err)
	}
	a.connStore.Set(sessionIDKey, cmd)
}

func (a *Agent) doExecCloseTerm(pkt *pb.Packet) {
	sessionID := pkt.Spec[pb.SpecGatewaySessionID]
	log.Printf("session=%v - received %v", string(sessionID), pb.PacketExecCloseTermType)
	procPidObj := a.connStore.Get(fmt.Sprintf("proc:%s", sessionID))
	if procPid, _ := procPidObj.(int); procPid > 0 {
		log.Printf("sending SIGINT signal to process %v ...", procPid)
		go runtime.Kill(procPid, syscall.SIGINT)
	}
}

func (a *Agent) sendCloseTerm(sessionID, msg string, exitCode string) {
	spec := map[string][]byte{pb.SpecGatewaySessionID: []byte(sessionID)}
	if exitCode != "" {
		spec[pb.SpecClientExecExitCodeKey] = []byte(exitCode)
	}
	_, _ = pb.NewStreamWriter(a.client, pb.PacketExecCloseTermType, spec).Write([]byte(msg))
}
