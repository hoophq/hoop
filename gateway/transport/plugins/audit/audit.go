package audit

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/getsentry/sentry-go"
	pbdlp "github.com/runopsio/hoop/common/dlp"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/memory"
	"github.com/runopsio/hoop/common/pg"
	pb "github.com/runopsio/hoop/common/proto"
	pbagent "github.com/runopsio/hoop/common/proto/agent"
	pbclient "github.com/runopsio/hoop/common/proto/client"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	plugintypes "github.com/runopsio/hoop/gateway/transport/plugins/types"
	"go.uber.org/zap"
)

const StorageWriterParam string = "audit_storage_writer"

type (
	auditPlugin struct {
		storageWriter   StorageWriter
		walSessionStore memory.Store
		started         bool
		mu              sync.RWMutex
		log             *zap.SugaredLogger
	}

	StorageWriter interface {
		Write(pctx plugintypes.Context) error
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

	storageWriterObj := pctx.ParamsData[StorageWriterParam]
	storageWriter, ok := storageWriterObj.(StorageWriter)

	if !ok {
		return fmt.Errorf("audit_storage_writer config must be an pluginscore.StorageWriter instance")
	}
	p.started = true
	p.storageWriter = storageWriter
	return nil
}
func (p *auditPlugin) OnUpdate(_, _ *types.Plugin) error { return nil }
func (p *auditPlugin) OnConnect(pctx plugintypes.Context) error {
	p.log.With("session", pctx.SID).Infof("processing on-connect")
	if pctx.OrgID == "" || pctx.SID == "" {
		return fmt.Errorf("failed processing audit plugin, missing org_id and session_id params")
	}
	pctx.ParamsData["status"] = "open"

	if err := p.writeOnConnect(pctx); err != nil {
		return err
	}
	pctx.ParamsData["start_date"] = func() *time.Time { d := time.Now().UTC(); return &d }()
	// Persist the session in the storage
	if err := p.storageWriter.Write(pctx); err != nil {
		return err
	}
	p.mu = sync.RWMutex{}
	return nil
}

func (p *auditPlugin) OnReceive(pctx plugintypes.Context, pkt *pb.Packet) (*plugintypes.ConnectResponse, error) {
	dlpCount := decodeDlpSummary(pkt)
	switch pb.PacketType(pkt.GetType()) {
	case pbagent.PGConnectionWrite:
		isSimpleQuery, queryBytes, err := pg.SimpleQueryContent(pkt.Payload)
		if !isSimpleQuery {
			break
		}
		if err != nil {
			log.With("sid", pctx.SID).Warnf("failed parsing simple query data, err=%v", err)
			log.With("sid", pctx.SID).Warnf("% X", pkt.Payload)
			return nil, fmt.Errorf("failed obtaining simple query data, err=%v", err)
		}
		return nil, p.writeOnReceive(pctx.SID, 'i', dlpCount, queryBytes)
	case pbagent.MySQLConnectionWrite:
		if queryBytes := decodeMySQLCommandQuery(pkt.Payload); queryBytes != nil {
			return nil, p.writeOnReceive(pctx.SID, 'i', dlpCount, queryBytes)
		}
	case pbclient.WriteStdout,
		pbclient.WriteStderr:
		err := p.writeOnReceive(pctx.SID, 'o', dlpCount, pkt.Payload)
		if err != nil {
			log.Warnf("failed writing agent packet response, err=%v", err)
		}
		return nil, nil
	case pbclient.SessionClose:
		defer p.closeSession(pctx)
		if len(pkt.Payload) > 0 {
			return nil, p.writeOnReceive(pctx.SID, 'e', dlpCount, pkt.Payload)
		}
	case pbagent.ExecWriteStdin,
		pbagent.TerminalWriteStdin,
		pbagent.TCPConnectionWrite:
		return nil, p.writeOnReceive(pctx.SID, 'i', dlpCount, pkt.Payload)
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
			_ = p.writeOnReceive(pctx.SID, 'e', 0, []byte(errMsg.Error()))
			return nil
		}
	case pb.ConnectionOriginClientAPI:
		if errMsg != nil {
			// on errors, close the session right away
			_ = p.writeOnReceive(pctx.SID, 'e', 0, []byte(errMsg.Error()))
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
					_ = p.writeOnReceive(sessionID, 'e', 0, []byte(errMsg.Error()))
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
			_ = p.writeOnReceive(pctx.SID, 'e', 0, []byte(errMsg.Error()))
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

func decodeDlpSummary(pkt *pb.Packet) int64 {
	tsEnc := pkt.Spec[pb.SpecDLPTransformationSummary]
	if tsEnc == nil {
		return 0
	}
	var ts []*pbdlp.TransformationSummary
	if err := pb.GobDecodeInto(tsEnc, &ts); err != nil {
		log.With("plugin", "audit").Errorf("failed decoding dlp transformation summary, err=%v", err)
		sentry.CaptureException(err)
		return 0
	}
	counter := int64(0)
	for _, t := range ts {
		sr := t.SummaryResult
		for _, s := range sr {
			if len(s) > 0 {
				countStr := s[0]
				if n, err := strconv.Atoi(countStr); err == nil {
					counter += int64(n)
				}
			}
		}
	}
	return counter
}
