package agent

import (
	"fmt"
	"log"
	"strconv"

	"github.com/hoophq/pluginhooks"
	"github.com/runopsio/hoop/agent/dlp"
	term "github.com/runopsio/hoop/agent/terminal"
	pb "github.com/runopsio/hoop/common/proto"
	pbclient "github.com/runopsio/hoop/common/proto/client"
)

func (a *Agent) doExec(pkt *pb.Packet) {
	sessionID := string(pkt.Spec[pb.SpecGatewaySessionID])
	log.Printf("session=%v - received execution request", string(sessionID))

	connParams, pluginHooks := a.connectionParams(sessionID)
	if connParams == nil {
		log.Printf("session=%s - connection params not found", sessionID)
		a.sendCloseTerm(sessionID, "internal error, connection params not found", "")
		return
	}
	mutatePayload, err := pluginHooks.ExecRPCOnRecv(&pluginhooks.Request{
		SessionID:  sessionID,
		PacketType: pkt.Type,
		Payload:    pkt.Payload})
	if err != nil {
		_ = a.client.Send(&pb.Packet{
			Type:    pbclient.SessionClose,
			Payload: []byte(err.Error()),
			Spec:    map[string][]byte{pb.SpecGatewaySessionID: []byte(sessionID)},
		})
	}
	if len(mutatePayload) > 0 {
		pkt.Payload = mutatePayload
	}

	cmd, err := term.NewCommand(connParams.EnvVars, append(connParams.CmdList, connParams.ClientArgs...)...)
	if err != nil {
		log.Printf("failed configuring command, err=%v", err)
		_ = a.client.Send(&pb.Packet{
			Type:    pbclient.SessionClose,
			Payload: []byte(err.Error()),
			Spec:    map[string][]byte{pb.SpecGatewaySessionID: []byte(sessionID)},
		})
		return
	}
	log.Printf("session=%v, tty=false - executing command=%q", string(sessionID), cmd.String())

	spec := map[string][]byte{pb.SpecGatewaySessionID: []byte(sessionID)}
	stdoutw := pb.NewHookStreamWriter(a.client, pbclient.WriteStdout, spec, pluginHooks)
	stderrw := pb.NewHookStreamWriter(a.client, pbclient.WriteStderr, spec, pluginHooks)
	if dlpClient, ok := a.connStore.Get(dlpClientKey).(dlp.Client); ok {
		stdoutw = dlp.NewDLPStreamWriter(
			a.client,
			pluginHooks,
			dlpClient,
			pbclient.WriteStdout,
			map[string][]byte{pb.SpecGatewaySessionID: []byte(sessionID)},
			connParams.DLPInfoTypes)
		stderrw = dlp.NewDLPStreamWriter(
			a.client,
			pluginHooks,
			dlpClient,
			pbclient.WriteStderr,
			map[string][]byte{pb.SpecGatewaySessionID: []byte(sessionID)},
			connParams.DLPInfoTypes)
	}

	onExecErr := func(exitCode int, errMsg string, v ...any) {
		errMsg = fmt.Sprintf(errMsg, v...)
		_, _ = pb.NewHookStreamWriter(
			a.client,
			pbclient.SessionClose,
			map[string][]byte{
				pb.SpecGatewaySessionID:  []byte(sessionID),
				pb.SpecClientExitCodeKey: []byte(strconv.Itoa(exitCode)),
			},
			pluginHooks,
		).Write([]byte(errMsg))
	}

	if err = cmd.Run(stdoutw, stderrw, pkt.Payload, onExecErr); err != nil {
		log.Printf("session=%v - err=%v", string(sessionID), err)
	}
}
