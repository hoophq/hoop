package agent

import (
	"fmt"
	"log"
	"strconv"

	term "github.com/runopsio/hoop/agent/terminal"
	pb "github.com/runopsio/hoop/common/proto"
)

func (a *Agent) doExec(pkt *pb.Packet) {
	sessionID := pkt.Spec[pb.SpecGatewaySessionID]
	packetErrType := pb.PacketClientAgentExecErrType
	log.Printf("session=%v - received execution request", string(sessionID))

	connParams, _, err := a.buildConnectionParams(pkt, packetErrType)
	if err != nil {
		_ = a.client.Send(&pb.Packet{
			Type:    packetErrType.String(),
			Payload: []byte(err.Error()),
			Spec:    map[string][]byte{pb.SpecGatewaySessionID: sessionID},
		})
		return
	}

	cmd, err := term.NewCommand(connParams.EnvVars, append(connParams.CmdList, connParams.ClientArgs...)...)
	if err != nil {
		log.Printf("failed executing command, err=%v", err)
		_ = a.client.Send(&pb.Packet{
			Type:    packetErrType.String(),
			Payload: []byte(err.Error()),
			Spec:    map[string][]byte{pb.SpecGatewaySessionID: sessionID},
		})
		return
	}
	log.Printf("session=%v, tty=false - executing command=%q", string(sessionID), cmd.String())

	spec := map[string][]byte{pb.SpecGatewaySessionID: sessionID}
	stdoutWriter := pb.NewStreamWriter(a.client, pb.PacketClientAgentExecOKType, spec)

	onExecErr := func(exitCode int, errMsg string, v ...any) {
		errMsg = fmt.Sprintf(errMsg, v...)
		spec[pb.SpecClientExecExitCodeKey] = []byte(strconv.Itoa(exitCode))
		_, _ = pb.NewStreamWriter(a.client, packetErrType, spec).
			Write([]byte(errMsg))
	}

	if err = cmd.Run(stdoutWriter, pkt.Payload, onExecErr); err != nil {
		log.Printf("session=%v - err=%v", string(sessionID), err)
	}
}
