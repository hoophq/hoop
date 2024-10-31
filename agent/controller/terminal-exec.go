package controller

import (
	"fmt"
	"io"
	"libhoop"
	"strconv"
	"strings"

	"github.com/hoophq/hoop/agent/guardrails"
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

	stdoutw, stderrw, err := a.loadDefaultWriter(sessionID, connParams, pkt)
	if err != nil {
		log.With("sid", sessionID).Error(err)
		a.sendClientSessionClose(sessionID, err.Error())
		return
	}

	// spec := map[string][]byte{pb.SpecGatewaySessionID: []byte(sessionID)}
	// stdoutw := pb.NewStreamWriter(a.client, pbclient.WriteStdout, spec)
	// stderrw := pb.NewStreamWriter(a.client, pbclient.WriteStderr, spec)
	opts := map[string]string{
		"dlp_provider":              a.getDlpProvider(),
		"mspresidio_analyzer_url":   a.getMSPresidioAnalyzerURL(),
		"mspresidio_anonymizer_url": a.getMSPresidioAnonymizerURL(),
		"dlp_gcp_credentials":       a.getGCPCredentials(),
		"dlp_info_types":            strings.Join(connParams.DLPInfoTypes, ","),
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
		string(sessionID), len(pkt.Payload), args)
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

func (a *Agent) loadDefaultWriter(sessionID string, connParams *pb.AgentConnectionParams, pkt *pb.Packet) (stdout, stderr io.WriteCloser, err error) {
	hasInputRules, hasOutputRules := len(connParams.GuardRailInputRules) > 0, len(connParams.GuardRailOutputRules) > 0
	log.Infof("output rules=%v, input rules=%v", string(connParams.GuardRailInputRules), string(connParams.GuardRailOutputRules))
	log.With("sid", sessionID).Infof("loading default writer, input-rules=%v, output-rules=%v", hasInputRules, hasOutputRules)
	if hasInputRules {
		err := guardrails.Validate(connParams.GuardRailInputRules, pkt.Payload)
		if err != nil {
			return nil, nil, fmt.Errorf("internal error, unable to decode guard rail inputs data, reason=%v", err)
		}
	}

	if hasOutputRules {
		stdout = guardrails.NewWriter(sessionID, a.client, pbclient.WriteStdout, connParams.GuardRailOutputRules)
		stderr = guardrails.NewWriter(sessionID, a.client, pbclient.WriteStderr, connParams.GuardRailOutputRules)
		return
	}
	spec := map[string][]byte{pb.SpecGatewaySessionID: []byte(sessionID)}
	stdout = pb.NewStreamWriter(a.client, pbclient.WriteStdout, spec)
	stderr = pb.NewStreamWriter(a.client, pbclient.WriteStderr, spec)
	return
}
