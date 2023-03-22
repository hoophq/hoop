package agent

import (
	"fmt"
	"strconv"

	"github.com/runopsio/hoop/common/log"

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
		a.sendClientSessionClose(sessionID, "internal error, connection params not found")
		return
	}
	mutatePayload, err := pluginHooks.ExecRPCOnRecv(&pluginhooks.Request{
		SessionID:  sessionID,
		PacketType: pkt.Type,
		Payload:    pkt.Payload})
	if err != nil {
		a.sendClientSessionClose(sessionID, fmt.Sprintf("failed executing plugin/onrecv phase, reason=%v", err))
		return
	}
	if len(mutatePayload) > 0 {
		pkt.Payload = mutatePayload
	}

	cmd, err := term.NewCommand(connParams.EnvVars, append(connParams.CmdList, connParams.ClientArgs...)...)
	if err != nil {
		errMsg := fmt.Sprintf("failed configuring command, reason=%v", err)
		log.Printf("session=%s - %s", sessionID, errMsg)
		a.sendClientSessionClose(sessionID, errMsg)
		return
	}
	log.Printf("session=%v, tty=false - executing command:%v", string(sessionID), cmd.String())

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

	cmd.Run(stdoutw, stderrw, pkt.Payload, func(exitCode int, errMsg string, v ...any) {
		errMsg = fmt.Sprintf(errMsg, v...)
		if errMsg != "" {
			log.Printf("session=%v, exitcode=%v - err=%v", string(sessionID), exitCode, errMsg)
		}
		_, _ = pb.NewHookStreamWriter(
			a.client,
			pbclient.SessionClose,
			map[string][]byte{
				pb.SpecGatewaySessionID:  []byte(sessionID),
				pb.SpecClientExitCodeKey: []byte(strconv.Itoa(exitCode)),
			},
			pluginHooks,
		).Write([]byte(errMsg))
	})
}
