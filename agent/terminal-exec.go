package agent

import (
	"fmt"
	"log"
	"strconv"

	term "github.com/runopsio/hoop/agent/terminal"
	pb "github.com/runopsio/hoop/common/proto"
	pbclient "github.com/runopsio/hoop/common/proto/client"
)

func (a *Agent) doExec(pkt *pb.Packet) {
	sessionID := string(pkt.Spec[pb.SpecGatewaySessionID])
	log.Printf("session=%v - received execution request", string(sessionID))

	connParams, _ := a.connectionParams(sessionID)
	if connParams == nil {
		log.Printf("session=%s - connection params not found", sessionID)
		a.sendCloseTerm(sessionID, "internal error, connection params not found", "")
		return
	}

	cmd, err := term.NewCommand(connParams.EnvVars, append(connParams.CmdList, connParams.ClientArgs...)...)
	if err != nil {
		log.Printf("failed executing command, err=%v", err)
		_ = a.client.Send(&pb.Packet{
			Type:    pbclient.SessionClose,
			Payload: []byte(err.Error()),
			Spec:    map[string][]byte{pb.SpecGatewaySessionID: []byte(sessionID)},
		})
		return
	}
	log.Printf("session=%v, tty=false - executing command=%q", string(sessionID), cmd.String())

	spec := map[string][]byte{pb.SpecGatewaySessionID: []byte(sessionID)}
	stdoutw := pb.NewStdoutStreamWriter(a.client, pbclient.WriteStdout, spec)
	stderrw := pb.NewStderrStreamWriter(a.client, pbclient.WriteStderr, spec)

	onExecErr := func(exitCode int, errMsg string, v ...any) {
		errMsg = fmt.Sprintf(errMsg, v...)
		spec[pb.SpecClientExitCodeKey] = []byte(strconv.Itoa(exitCode))
		_, _ = pb.NewStreamWriter(a.client, pbclient.SessionClose, spec).
			Write([]byte(errMsg))
	}

	if err = cmd.Run(stdoutw, stderrw, pkt.Payload, onExecErr); err != nil {
		log.Printf("session=%v - err=%v", string(sessionID), err)
	}
}
