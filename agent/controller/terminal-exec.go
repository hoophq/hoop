package controller

import (
	"errors"
	"fmt"
	"io"
	"libhoop"
	"libhoop/agent/dbexec"
	redactortypes "libhoop/redactor/types"
	"strconv"
	"strings"

	"github.com/hoophq/hoop/agent/controller/featureflagstate"
	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	pbclient "github.com/hoophq/hoop/common/proto/client"
)

// dbExecDriverFlag gates the driver-based SQL exec path. When off, SQL exec
// connections fall back to spawning the vendor CLI.
const dbExecDriverFlag = "experimental.db_exec_driver"

// dbExecDriver maps a connection type to the in-process driver used by the
// secure SQL exec path, reporting whether the type is supported.
func dbExecDriver(connType string) (string, bool) {
	switch pb.ConnectionType(connType) {
	case pb.ConnectionTypePostgres:
		return string(dbexec.DriverPostgres), true
	case pb.ConnectionTypeMySQL:
		return string(dbexec.DriverMySQL), true
	case pb.ConnectionTypeMSSQL:
		return string(dbexec.DriverMSSQL), true
	case pb.ConnectionTypeOracleDB:
		return string(dbexec.DriverOracle), true
	}
	return "", false
}

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

	// var analyzerMetricsRules string
	// if connParams.AnalyzerMetricsRules != nil {
	// 	analyzerMetricsRules = string(connParams.AnalyzerMetricsRules)
	// }

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
		// TODO: make it disable for now, it consumes too much resources only for collecting metrics
		// on more intensive environments this is a problem, we can enable it later based on specific rules
		// "analyzer_metrics_rules":    analyzerMetricsRules,
		"guard_rail_rules": guardRailRules,
	}

	// SQL exec connections route through the in-process database driver when the
	// flag is on, which removes the vendor CLI's meta-command escape surface
	// (e.g. psql "\!") and keeps the credential out of any spawned process.
	// Every other connection type, and the flag-off case, keeps the CLI path.
	var (
		cmd      libhoop.Proxy
		err      error
		execDesc string
	)
	if driver, ok := dbExecDriver(connParams.ConnectionType); ok && featureflagstate.IsEnabled(dbExecDriverFlag) {
		cmd, err = a.newDBExecProxy(driver, connParams, pkt.Payload, stdoutw, stderrw, opts)
		execDesc = "db-driver=" + driver
	} else {
		args := append(connParams.CmdList, connParams.ClientArgs...)
		cmd, err = libhoop.NewAdHocExec(connParams.EnvVars, args, pkt.Payload, stdoutw, stderrw, opts)
		execDesc = fmt.Sprintf("%v", args)
	}
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
	log.With(logFields...).Infof("tty=false, stdinsize=%v - executing command:%v", len(pkt.Payload), execDesc)
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

// newDBExecProxy resolves the connection credentials and builds the
// driver-based SQL exec proxy. Credentials are passed to libhoop through the
// opts map (the same channel the wire proxies use) so libhoop stays free of
// agent/common imports.
func (a *Agent) newDBExecProxy(driver string, connParams *pb.AgentConnectionParams, payload []byte, stdoutw, stderrw io.Writer, opts map[string]string) (libhoop.Proxy, error) {
	connenv, err := parseConnectionEnvVars(connParams.EnvVars, pb.ConnectionType(connParams.ConnectionType))
	if err != nil {
		return nil, err
	}
	opts[dbexec.OptKeyHost] = connenv.host
	opts[dbexec.OptKeyPort] = connenv.port
	opts[dbexec.OptKeyUser] = connenv.user
	opts[dbexec.OptKeyPassword] = connenv.pass
	opts[dbexec.OptKeyDBName] = connenv.dbname
	opts[dbexec.OptKeySSLMode] = connenv.postgresSSLMode
	opts[dbexec.OptKeyServiceName] = connenv.serviceName
	if connenv.insecure {
		opts[dbexec.OptKeyInsecure] = "true"
	}
	return libhoop.NewAdHocDBExec(driver, payload, stdoutw, stderrw, opts)
}
