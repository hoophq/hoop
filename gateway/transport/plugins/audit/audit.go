package audit

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/memory"
	mssqltypes "github.com/runopsio/hoop/common/mssql/types"
	pgtypes "github.com/runopsio/hoop/common/pgtypes"
	pb "github.com/runopsio/hoop/common/proto"
	pbagent "github.com/runopsio/hoop/common/proto/agent"
	pbclient "github.com/runopsio/hoop/common/proto/client"
	eventlogv0 "github.com/runopsio/hoop/gateway/session/eventlog/v0"
	"github.com/runopsio/hoop/gateway/storagev2"
	sessionstorage "github.com/runopsio/hoop/gateway/storagev2/session"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	plugintypes "github.com/runopsio/hoop/gateway/transport/plugins/types"
	"go.uber.org/zap"
)

type (
	auditPlugin struct {
		walSessionStore memory.Store
		started         bool
		mu              sync.RWMutex
		log             *zap.SugaredLogger
	}
)

func New() *auditPlugin {
	return &auditPlugin{
		walSessionStore: memory.New(),
		log:             log.With("plugin", "audit"),
	}
}

func (p *auditPlugin) Name() string { return plugintypes.PluginAuditName }
func (p *auditPlugin) OnStartup(pctx plugintypes.Context) error {
	if p.started {
		return nil
	}

	if fi, _ := os.Stat(plugintypes.AuditPath); fi == nil || !fi.IsDir() {
		return fmt.Errorf("failed to retrieve audit path info, path=%v", plugintypes.AuditPath)
	}
	p.started = true
	return nil
}
func (p *auditPlugin) OnUpdate(_, _ *types.Plugin) error { return nil }
func (p *auditPlugin) OnConnect(pctx plugintypes.Context) error {
	p.log.With("session", pctx.SID).Infof("processing on-connect")
	if pctx.OrgID == "" || pctx.SID == "" {
		return fmt.Errorf("failed processing audit plugin, missing org_id and session_id params")
	}
	startDate := time.Now().UTC()
	pctx.ParamsData["status"] = types.SessionStatusOpen
	pctx.ParamsData["start_date"] = &startDate
	if err := p.writeOnConnect(pctx); err != nil {
		return err
	}
	// Persist the session in the storage
	ctx := storagev2.NewContext(pctx.UserID, pctx.OrgID, storagev2.NewStorage(nil))
	err := sessionstorage.Put(ctx, types.Session{
		ID:               pctx.SID,
		OrgID:            pctx.OrgID,
		UserEmail:        pctx.UserEmail,
		UserID:           pctx.UserID,
		UserName:         pctx.UserName,
		Type:             pctx.ConnectionType,
		Connection:       pctx.ConnectionName,
		Verb:             pctx.ClientVerb,
		Status:           types.SessionStatusOpen,
		Script:           types.SessionScript{"data": pctx.Script},
		Labels:           pctx.Labels,
		Metadata:         pctx.Metadata,
		NonIndexedStream: nil,
		EventSize:        0,
		StartSession:     startDate,
		EndSession:       nil,
		DlpCount:         0,
	})
	if err != nil {
		return fmt.Errorf("failed persisting sessino to store, reason=%v", err)
	}
	p.mu = sync.RWMutex{}
	return nil
}

func (p *auditPlugin) OnReceive(pctx plugintypes.Context, pkt *pb.Packet) (*plugintypes.ConnectResponse, error) {
	redactCount := decodeDlpSummary(pkt)
	switch pb.PacketType(pkt.GetType()) {
	case pbclient.PGConnectionWrite, pbclient.MySQLConnectionWrite:
		if redactCount > 0 {
			return nil, p.writeOnReceive(pctx.SID, eventlogv0.OutputType, redactCount, nil)
		}
	case pbagent.PGConnectionWrite:
		isSimpleQuery, queryBytes, err := pgtypes.SimpleQueryContent(pkt.Payload)
		if !isSimpleQuery {
			break
		}
		if err != nil {
			log.With("sid", pctx.SID).Errorf("failed parsing simple query data, err=%v", err)
			return nil, fmt.Errorf("failed obtaining simple query data, reason=%v", err)
		}
		return nil, p.writeOnReceive(pctx.SID, eventlogv0.InputType, 0, queryBytes)
	case pbagent.MySQLConnectionWrite:
		if queryBytes := decodeMySQLCommandQuery(pkt.Payload); queryBytes != nil {
			return nil, p.writeOnReceive(pctx.SID, eventlogv0.InputType, 0, queryBytes)
		}
	case pbagent.MSSQLConnectionWrite:
		var mssqlPacketType mssqltypes.PacketType
		if len(pkt.Payload) > 0 {
			mssqlPacketType = mssqltypes.PacketType(pkt.Payload[0])
		}
		switch mssqlPacketType {
		case mssqltypes.PacketSQLBatchType:
			query, err := mssqltypes.DecodeSQLBatchToRawQuery(pkt.Payload)
			if err != nil {
				return nil, err
			}
			if query != "" {
				return nil, p.writeOnReceive(pctx.SID, eventlogv0.InputType, 0, []byte(query))
			}
		}
	case pbclient.WriteStdout,
		pbclient.WriteStderr:
		err := p.writeOnReceive(pctx.SID, eventlogv0.OutputType, redactCount, pkt.Payload)
		if err != nil {
			log.Warnf("failed writing agent packet response, err=%v", err)
		}
		return nil, nil
	case pbclient.SessionClose:
		defer p.closeSession(pctx)
		if len(pkt.Payload) > 0 {
			return nil, p.writeOnReceive(pctx.SID, eventlogv0.ErrorType, redactCount, pkt.Payload)
		}
	case pbagent.ExecWriteStdin,
		pbagent.TerminalWriteStdin,
		pbagent.TCPConnectionWrite:
		return nil, p.writeOnReceive(pctx.SID, eventlogv0.InputType, redactCount, pkt.Payload)
	}
	return nil, nil
}

func (p *auditPlugin) OnDisconnect(pctx plugintypes.Context, errMsg error) error {
	p.log.With("session", pctx.SID, "origin", pctx.ClientOrigin).
		Debugf("processing disconnect")
	switch pctx.ClientOrigin {
	case pb.ConnectionOriginClient,
		pb.ConnectionOriginClientProxyManager:
		defer p.closeSession(pctx)
		if errMsg != nil {
			if errMsg == io.EOF {
				errMsg = fmt.Errorf("client disconnected, end-of-file stream")
			}
			_ = p.writeOnReceive(pctx.SID, eventlogv0.ErrorType, 0, []byte(errMsg.Error()))
			return nil
		}
	case pb.ConnectionOriginClientAPI:
		if errMsg != nil {
			// on errors, close the session right away
			if errMsg != io.EOF {
				_ = p.writeOnReceive(pctx.SID, eventlogv0.ErrorType, 0, []byte(errMsg.Error()))
			}
			p.closeSession(pctx)
			return nil
		}
		// keep the connection open to let packets flow async
	case pb.ConnectionOriginAgent:
		agentID := fmt.Sprintf("%v", pctx.ParamsData.GetString("disconnect-agent-id"))
		if agentID != "" {
			p.log.Warnf("agent %v was shutdown, graceful closing sessions", agentID)
			// always close all sessions of this agent when it disconnects
			// there's no capability of recovering the state of execution
			// when this condition is present.
			for key := range pctx.ParamsData {
				if !strings.HasPrefix(key, agentID) {
					continue
				}
				_, sessionID, found := strings.Cut(key, ":")
				if !found {
					continue
				}
				p.log.With("session", sessionID).Infof("closing session, agent %v was shutdown", agentID)
				if errMsg != nil {
					_ = p.writeOnReceive(sessionID, eventlogv0.ErrorType, 0, []byte(errMsg.Error()))
					p.closeSession(pctx)
					continue
				}
				p.closeSession(pctx)
			}
			return nil
		}
		// it close sessions that are being processed async
		// e.g.: when it receives a session close packet
		defer p.closeSession(pctx)
		if errMsg != nil {
			_ = p.writeOnReceive(pctx.SID, eventlogv0.ErrorType, 0, []byte(errMsg.Error()))
			return nil
		}
	}
	return nil
}

func (p *auditPlugin) closeSession(pctx plugintypes.Context) {
	sessionID := pctx.SID
	log.With("session", sessionID).Infof("closing session")
	go func() {
		if err := p.writeOnClose(pctx); err != nil {
			p.log.Warnf("session=%v - failed closing session: %v", sessionID, err)
		}
	}()
}

func (p *auditPlugin) OnShutdown() {}

func decodeDlpSummary(pkt *pb.Packet) (counter int64) {
	tsEnc := pkt.Spec[pb.SpecDLPTransformationSummary]
	if tsEnc == nil {
		return 0
	}
	var ts []*pb.TransformationSummary
	if err := pb.GobDecodeInto(tsEnc, &ts); err != nil {
		log.With("plugin", "audit").Warnf("failed decoding dlp transformation summary, err=%v", err)
		return 0
	}
	for _, t := range ts {
		for _, s := range t.SummaryResult {
			if len(s) > 0 {
				countStr := s[0]
				if n, err := strconv.Atoi(countStr); err == nil {
					counter += int64(n)
				}
			}
		}
	}
	return
}
