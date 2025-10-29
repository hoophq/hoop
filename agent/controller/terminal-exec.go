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
	sid := string(pkt.Spec[pb.SpecGatewaySessionID])
	log.With("sid", sid).Infof("received execution request")

	connParams := a.connectionParams(sid)
	if connParams == nil {
		log.With("sid", sid).Infof("connection params not found")
		a.sendClientSessionClose(sid, "internal error, connection params not found")
		return
	}

	spec := map[string][]byte{pb.SpecGatewaySessionID: []byte(sid)}
	stdoutw := pb.NewStreamWriter(a.client, pbclient.WriteStdout, spec)
	stderrw := pb.NewStreamWriter(a.client, pbclient.WriteStderr, spec)

	var dataMaskingEntityTypesData string
	if connParams.DataMaskingEntityTypesData != nil {
		dataMaskingEntityTypesData = string(connParams.DataMaskingEntityTypesData)
	}

	opts := map[string]string{
		"sid":                       sid,
		"dlp_provider":              connParams.DlpProvider,
		"dlp_mode":                  connParams.DlpMode,
		"mspresidio_analyzer_url":   connParams.DlpPresidioAnalyzerURL,
		"mspresidio_anonymizer_url": connParams.DlpPresidioAnonymizerURL,
		"dlp_gcp_credentials":       connParams.DlpGcpRawCredentialsJSON,
		"dlp_info_types":            strings.Join(connParams.DLPInfoTypes, ","),
		"data_masking_entity_data":  dataMaskingEntityTypesData,
	}

	args := append(connParams.CmdList, connParams.ClientArgs...)
	cmd, err := libhoop.NewAdHocExec(connParams.EnvVars, args, pkt.Payload, stdoutw, stderrw, opts)
	if err != nil {
		errMsg := fmt.Sprintf("failed configuring command, reason=%v", err)
		log.With("sid", sid).Infof(errMsg)
		a.sendClientSessionClose(sid, errMsg)
		return
	}
	log.With("sid", sid).Infof("tty=false, stdinsize=%v - executing command:%v", len(pkt.Payload), args)
	sessionIDKey := fmt.Sprintf(execStoreKey, sid)
	a.connStore.Set(sessionIDKey, cmd)

	cmd.Run(func(exitCode int, errMsg string) {
		if err := cmd.Close(); err != nil {
			log.With("sid", sid).Warnf("failed closing command, err=%v", err)
		}
		a.connStore.Del(sessionIDKey)
		log.With("sid", sid).Infof("exitcode=%v - err=%v", exitCode, errMsg)
		a.sendClientSessionCloseWithExitCode(sid, errMsg, strconv.Itoa(exitCode))
	})
}
