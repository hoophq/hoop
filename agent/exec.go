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
	gwID := pkt.Spec[pb.SpecGatewayConnectionID]
	encConnectionParams := pkt.Spec[pb.SpecAgentConnectionParamsKey]
	var connParams pb.AgentConnectionParams
	if err := pb.GobDecodeInto(encConnectionParams, &connParams); err != nil {
		// TODO: send error
		log.Printf("failed decoding connection params=%#v, err=%v", encConnectionParams, err)
		_, _ = pb.NewStreamWriter(a.stream.Send, pb.PacketExecCloseTermType, map[string][]byte{
			pb.SpecGatewayConnectionID: gwID,
		}).Write([]byte(`internal error, failed decoding connection params`))
		return
	}
	cmd, err := exec.NewCommand(connParams.EnvVars,
		append(connParams.CmdList, connParams.ClientArgs...)...)
	if err != nil {
		log.Printf("failed executing command, err=%v", err)
		_, _ = pb.NewStreamWriter(a.stream.Send, pb.PacketExecCloseTermType, map[string][]byte{
			pb.SpecGatewayConnectionID: gwID,
		}).Write([]byte(`failed executing command`))
		return
	}
	log.Printf("gatewayid=%v, tty=false - executing command=%q", string(gwID), cmd.String())
	spec := map[string][]byte{pb.SpecGatewayConnectionID: gwID}
	stdoutWriter := pb.NewStreamWriter(a.stream.Send, pb.PacketExecClientWriteStdoutType, spec)
	onExecEnd := func(exitCode int, errMsg string, v ...any) {
		errMsg = fmt.Sprintf(errMsg, v...)
		spec[pb.SpecClientExecExitCodeKey] = []byte(strconv.Itoa(exitCode))
		_, _ = pb.NewStreamWriter(a.stream.Send, pb.PacketExecCloseTermType, spec).
			Write([]byte(errMsg))
	}
	// TODO: add client args
	if err = cmd.Run(stdoutWriter, pkt.Payload, onExecEnd); err != nil {
		log.Printf("gatewayid=%v - err=%v", string(gwID), err)
	}
	a.connStore.Set(fmt.Sprintf("proc:%v", gwID), cmd.Pid())
}

func (a *Agent) doExecWriteAgentStdin(pkt *pb.Packet) {
	gwID := pkt.Spec[pb.SpecGatewayConnectionID]
	log.Printf("gatewayid=%v, tty=true - payload=% X", string(gwID), string(pkt.Payload))
	storeID := fmt.Sprintf("terminal:%s", gwID)
	cmdObj := a.connStore.Get(storeID)
	cmd, ok := cmdObj.(*exec.Command)
	if ok {
		// Write to tty stdin content
		if _, err := cmd.WriteTTY(pkt.Payload); err != nil {
			log.Printf("gatewayid=%v, tty=true - failed copying stdin to tty, err=%v", string(gwID), err)
		}
		return
	}
	var connParams pb.AgentConnectionParams
	encConnectionParams := pkt.Spec[pb.SpecAgentConnectionParamsKey]
	if err := pb.GobDecodeInto(encConnectionParams, &connParams); err != nil {
		log.Printf("gatewayid=%v, tty=true - failed decoding connection params=%#v, err=%v",
			gwID, encConnectionParams, err)
		_, _ = pb.NewStreamWriter(a.stream.Send, pb.PacketExecCloseTermType, map[string][]byte{
			pb.SpecGatewayConnectionID: pkt.Spec[pb.SpecGatewayConnectionID],
		}).Write([]byte(`internal error, failed decoding connection params`))
		return
	}

	cmd, err := exec.NewCommand(connParams.EnvVars,
		append(connParams.CmdList, connParams.ClientArgs...)...)
	if err != nil {
		log.Printf("gatewayid=%v, tty=true - failed executing command, err=%v", gwID, err)
		_, _ = pb.NewStreamWriter(a.stream.Send, pb.PacketExecCloseTermType, map[string][]byte{
			pb.SpecGatewayConnectionID: pkt.Spec[pb.SpecGatewayConnectionID],
		}).Write([]byte(`failed executing command`))
		return
	}
	log.Printf("gatewayid=%v, tty=true - executing command %q", string(gwID), cmd.String())
	spec := map[string][]byte{pb.SpecGatewayConnectionID: gwID}
	onExecEnd := func(exitCode int, errMsg string, v ...any) {
		errMsg = fmt.Sprintf(errMsg, v...)
		spec[pb.SpecClientExecExitCodeKey] = []byte(strconv.Itoa(exitCode))
		_, _ = pb.NewStreamWriter(a.stream.Send, pb.PacketExecCloseTermType, spec).
			Write([]byte(errMsg))
	}
	stdoutWriter := pb.NewStreamWriter(a.stream.Send, pb.PacketExecClientWriteStdoutType, spec)
	if err := cmd.RunOnTTY(stdoutWriter, onExecEnd); err != nil {
		log.Printf("gatewayid=%v, tty=true - err=%v", string(gwID), err)
	}
	a.connStore.Set(storeID, cmd)
}

func (a *Agent) doExecCloseTerm(pkt *pb.Packet) {
	gwID := pkt.Spec[pb.SpecGatewayConnectionID]
	log.Printf("gatewayid=%v - received %v", string(gwID), pb.PacketExecCloseTermType)
	procPidObj := a.connStore.Get(fmt.Sprintf("proc:%s", gwID))
	if procPid, _ := procPidObj.(int); procPid > 0 {
		log.Printf("sending SIGINT signal to process %v ...", procPid)
		go runtime.Kill(procPid, syscall.SIGINT)
	}
}
