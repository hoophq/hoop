package agent

import (
	"fmt"
	"log"
	"strconv"
	"syscall"

	pb "github.com/runopsio/hoop/proto"
	pbexec "github.com/runopsio/hoop/proto/exec"
	"github.com/runopsio/hoop/proto/runtime"
)

func (a *Agent) processExec(pkt *pb.Packet) {
	switch pb.PacketType(pkt.Type) {
	case pb.PacketExecRunProcType:
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
		cmd, err := pbexec.NewCommand(connParams.EnvVars, connParams.CmdList...)
		if err != nil {
			log.Printf("failed executing command, err=%v", err)
			_, _ = pb.NewStreamWriter(a.stream.Send, pb.PacketExecCloseTermType, map[string][]byte{
				pb.SpecGatewayConnectionID: gwID,
			}).Write([]byte(`failed executing command`))
			return
		}
		log.Printf("gatewayid=%v, tty=false - executing command=%q, envs=%v", string(gwID), cmd.String(), cmd.Environ())
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
	case pb.PacketExecWriteAgentStdinType:
		// 1. Create a tty and add to memory
		gwID := pkt.Spec[pb.SpecGatewayConnectionID]
		log.Printf("gatewayid=%v, tty=true - payload=% X", string(gwID), string(pkt.Payload))
		storeID := fmt.Sprintf("terminal:%s", gwID)
		cmdObj := a.connStore.Get(storeID)

		cmd, ok := cmdObj.(*pbexec.Command)
		if ok {
			// Write to tty stdin content
			if _, err := cmd.WriteTTY(pkt.Payload); err != nil {
				log.Printf("gatewayid=%v, tty=true - failed copying stdin to tty, err=%v", string(gwID), err)
			}
			// if _, err := ptty.File().Write(p.GetPayload()); err != nil {
			// 	log.Printf("connection=%v - failed copying stdin to tty, err=%v", p.GetRouterConnectionID(), err)
			// }
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
		cmd, err := pbexec.NewCommand(connParams.EnvVars, connParams.CmdList...)
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

		// rawEnvJSON, err := parseProviderConfig(p)
		// if err != nil {
		// 	log.Errorf("gatewayid=%v - failed obtaining secrets, err=%v", gwID, err)
		// 	_ = s.Send(types.NewProxyPacket().
		// 		Type(types.PacketExecCloseTermType).
		// 		FromSpec(p.GetSpec()).
		// 		Proto())
		// 	return
		// }
		// argsEnc := p.GetSpecVal(types.SpecExecArgsKey)
		// args, err := byteutils.GobDecodeStringSlice([]byte(argsEnc))
		// if err != nil {
		// 	log.Warnf("failed decoding args, err=%v", err)
		// }
		// ptty, err = proxyexec.RunProcessOnTTY(
		// 	p.GetSpecVal(types.SpecTargetTypeKey),
		// 	p.GetSpecVal(types.SpecCustomCommandKey),
		// 	rawEnvJSON,
		// 	args)
		// if err != nil {
		// 	log.Errorf("gatewayid=%v - failed creating tty, err=%v", gwID, err)
		// 	_ = s.Send(types.NewProxyPacket().
		// 		Type(types.PacketExecCloseTermType).
		// 		FromSpec(p.GetSpec()).
		// 		Proto())
		// 	return
		// }
		// store.Set(key, ptty)

		// go func() {
		// 	// Copy stdout from tty to grpc stream
		// 	w := &streamWriter{stream: s, packetType: types.PacketExecWriteStdoutType, spec: p.GetSpec()}
		// 	if _, err := io.Copy(w, ptty.File()); err != nil {
		// 		log.Warnf("gatewayid=%v - failed copying stdout from tty, err=%v", gwID, err)
		// 	}

		// 	exitCode := 0
		// 	if err := ptty.ProcWait(); err != nil {
		// 		if exErr, ok := err.(*exec.ExitError); ok {
		// 			exitCode = exErr.ExitCode()
		// 			// assume that it was killed or interrupted
		// 			// because the process is probably started already
		// 			if exitCode == -1 {
		// 				exitCode = 1
		// 			}
		// 		}
		// 	}
		// 	log.Debugf("gatewayid=%v, exit-code=%v - closing tty ...", gwID, exitCode)
		// 	if err := ptty.Close(); err != nil {
		// 		log.Warnf("gatewayid=%s - failed closing tty, err=%v", gwID, err)
		// 	}
		// 	_ = s.Send(types.NewProxyPacket().
		// 		Type(types.PacketExecCloseTermType).
		// 		FromSpec(p.GetSpec()).
		// 		SetSpecVal(types.SpecExecExitCodeKey, []byte(strconv.Itoa(exitCode))).
		// 		Proto())
		// }()
	case pb.PacketExecCloseTermType:
		gwID := pkt.Spec[pb.SpecGatewayConnectionID]
		log.Printf("gatewayid=%v - received %v", gwID, pb.PacketExecCloseTermType)
		// pttyObj := store.Get(fmt.Sprintf("terminal:%s", gwID))
		// if ptty, ok := pttyObj.(*proxyexec.TTY); ok {
		// 	log.Printf("cleanup tty process ...")
		// 	if err := ptty.Close(); err != nil {
		// 		log.Printf("gatewayid=%s - failed closing tty, err=%v", gwID, err)
		// 	}
		// }
		procPidObj := a.connStore.Get(fmt.Sprintf("proc:%s", gwID))
		if procPid, _ := procPidObj.(int); procPid > 0 {
			log.Printf("sending SIGINT signal to process %v ...", procPid)
			go runtime.Kill(procPid, syscall.SIGINT)
		}
	}
}
