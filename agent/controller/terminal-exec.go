package controller

import (
	"fmt"
	"libhoop"
	"strconv"
	"strings"

	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	pbclient "github.com/hoophq/hoop/common/proto/client"
)

func (a *Agent) doExec(pkt *pb.Packet) {
	sessionID := string(pkt.Spec[pb.SpecGatewaySessionID])
	log.Printf("session=%v - received execution request", string(sessionID))

	connParams := a.connectionParams(sessionID)
	if connParams == nil {
		log.Printf("session=%s - connection params not found", sessionID)
		a.sendClientSessionClose(sessionID, "internal error, connection params not found")
		return
	}

	spec := map[string][]byte{pb.SpecGatewaySessionID: []byte(sessionID)}
	dlpProvider := pkt.Spec[pb.SpecAgentDlpProvider]
	stdoutw := pb.NewStreamWriter(a.client, pbclient.WriteStdout, spec)
	stderrw := pb.NewStreamWriter(a.client, pbclient.WriteStderr, spec)
	opts := map[string]string{
		"dlp_provider":        string(dlpProvider),
		"dlp_gcp_credentials": a.getGCPCredentials(),
		"dlp_info_types":      strings.Join(connParams.DLPInfoTypes, ","),
	}
	args := append(connParams.CmdList, connParams.ClientArgs...)
	cmd, err := libhoop.NewAdHocExec(connParams.EnvVars, args, pkt.Payload, stdoutw, stderrw, opts)
	if err != nil {
		errMsg := fmt.Sprintf("failed configuring command, reason=%v", err)
		log.Printf("session=%s - %s", sessionID, errMsg)
		a.sendClientSessionClose(sessionID, errMsg)
		return
	}
	log.Printf("session=%v, tty=false, stdinsize=%v - executing command:%v",
		string(sessionID), len(pkt.Payload), cmd)
	sessionIDKey := fmt.Sprintf(execStoreKey, sessionID)
	a.connStore.Set(sessionIDKey, cmd)

	cmd.Run(func(exitCode int, errMsg string) {
		if err := cmd.Close(); err != nil {
			log.Warnf("session=%v - failed closing command, err=%v", string(sessionID), err)
		}
		a.connStore.Del(sessionIDKey)
		if errMsg != "" {
			log.Infof("session=%v, exitcode=%v - err=%v", string(sessionID), exitCode, errMsg)
		}
		_, _ = pb.NewStreamWriter(
			a.client,
			pbclient.SessionClose,
			map[string][]byte{
				pb.SpecGatewaySessionID:  []byte(sessionID),
				pb.SpecClientExitCodeKey: []byte(strconv.Itoa(exitCode)),
			},
		).Write([]byte(errMsg))
	})
}
