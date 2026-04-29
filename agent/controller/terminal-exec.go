package controller

import (
	"errors"
	"fmt"
	"libhoop"
	redactortypes "libhoop/redactor/types"
	"strconv"
	"strings"

	"github.com/hoophq/hoop/agent/controller/featureflagstate"
	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	pbclient "github.com/hoophq/hoop/common/proto/client"
)

// execInputLogMaxBytes caps the size of the exec input snippet attached to
// structured logs. Keeps memory usage and downstream log volume bounded when
// the experimental.log_exec_input feature flag is enabled.
const execInputLogMaxBytes = 4096

// truncateForLog returns a snippet of payload safe to attach to a log line,
// along with a flag indicating whether truncation happened and the original
// payload size in bytes.
func truncateForLog(payload []byte, max int) (snippet string, truncated bool, originalSize int) {
	originalSize = len(payload)
	if originalSize <= max {
		return string(payload), false, originalSize
	}
	return string(payload[:max]), true, originalSize
}

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

	var analyzerMetricsRules string
	if connParams.AnalyzerMetricsRules != nil {
		analyzerMetricsRules = string(connParams.AnalyzerMetricsRules)
	}

	var guardRailRules string
	if connParams.GuardRailRules != nil {
		guardRailRules = string(connParams.GuardRailRules)
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
		"analyzer_metrics_rules":    analyzerMetricsRules,
		"guard_rail_rules":          guardRailRules,
	}

	args := append(connParams.CmdList, connParams.ClientArgs...)
	cmd, err := libhoop.NewAdHocExec(connParams.EnvVars, args, pkt.Payload, stdoutw, stderrw, opts)
	if err != nil {
		log.With("sid", sid).Infof("failed configuring command, reason=%v", err)
		// Write error to stderr so it's visible in the web UI output
		errMsg := err.Error()
		var guardrailErr *redactortypes.ErrGuardrailsValidation
		if errors.As(err, &guardrailErr) {
			errMsg = guardrailErr.FormattedMessage()
		}
		_, _ = stderrw.Write([]byte(errMsg))
		a.sendClientSessionCloseFromError(sid, err)
		return
	}
	logFields := []any{
		"sid", sid,
		"connection", connParams.ConnectionName,
		"connection_type", connParams.ConnectionType,
		"client_verb", connParams.ClientVerb,
		"client_origin", connParams.ClientOrigin,
		"stdin_size", len(pkt.Payload),
	}
	if featureflagstate.IsEnabled("experimental.log_exec_input") {
		snippet, truncated, size := truncateForLog(pkt.Payload, execInputLogMaxBytes)
		logFields = append(logFields,
			"input", snippet,
			"input_truncated", truncated,
			"input_size", size,
		)
	}
	log.With(logFields...).Infof("tty=false, stdinsize=%v - executing command:%v", len(pkt.Payload), args)
	sessionIDKey := fmt.Sprintf(execStoreKey, sid)
	a.connStore.Set(sessionIDKey, cmd)

	cmd.Run(func(exitCode int, errMsg string) {
		if err := cmd.Close(); err != nil {
			log.With("sid", sid).Warnf("failed closing command, err=%v", err)
		}
		a.connStore.Del(sessionIDKey)
		log.With("sid", sid).Infof("exitcode=%v - err=%v", exitCode, errMsg)

		// Check for guardrails errors (stored on cmd during output validation)
		if cmdGuardrail, ok := cmd.(interface{ GuardrailErr() error }); ok {
			if gErr := cmdGuardrail.GuardrailErr(); gErr != nil {
				a.sendClientSessionCloseFromError(sid, gErr)
				cmd.FlushMetrics(newIoMetricFlush(a.client, sid))
				return
			}
		}
		a.sendClientSessionCloseWithExitCode(sid, errMsg, strconv.Itoa(exitCode))

		// since the doExec kill the connection after the commnad runs we can flush
		cmd.FlushMetrics(newIoMetricFlush(a.client, sid))
	})

}
