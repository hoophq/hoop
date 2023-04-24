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
	"github.com/runopsio/hoop/gateway/plugin"
	"go.uber.org/zap"
)

const (
	Name               string = "audit"
	StorageWriterParam string = "audit_storage_writer"
)

type (
	auditPlugin struct {
		name            string
		storageWriter   StorageWriter
		walSessionStore memory.Store
		started         bool
		mu              sync.RWMutex
		log             *zap.SugaredLogger
	}

	StorageWriter interface {
		Write(config plugin.Config) error
	}
)

func New() *auditPlugin {
	return &auditPlugin{
		name:            Name,
		walSessionStore: memory.New(),
		log:             log.With("plugin", "audit"),
	}
}

func (p *auditPlugin) Name() string {
	return p.name
}

func (p *auditPlugin) OnStartup(config plugin.Config) error {
	if p.started {
		return nil
	}

	if fi, _ := os.Stat(plugin.AuditPath); fi == nil || !fi.IsDir() {
		return fmt.Errorf("failed to retrieve audit path info, path=%v", plugin.AuditPath)
	}

	storageWriterObj := config.ParamsData[StorageWriterParam]
	storageWriter, ok := storageWriterObj.(StorageWriter)

	if !ok {
		return fmt.Errorf("audit_storage_writer config must be an pluginscore.StorageWriter instance")
	}
	p.started = true
	p.storageWriter = storageWriter
	return nil
}

func (p *auditPlugin) OnConnect(config plugin.Config) error {
	p.log.With("session", config.SessionId).Infof("processing on-connect")
	if config.Org == "" || config.SessionId == "" {
		return fmt.Errorf("failed processing audit plugin, missing org_id and session_id params")
	}

	if err := p.writeOnConnect(config); err != nil {
		return err
	}
	config.ParamsData["start_date"] = func() *time.Time { d := time.Now().UTC(); return &d }()
	// Persist the session in the storage
	if err := p.storageWriter.Write(config); err != nil {
		return err
	}
	p.mu = sync.RWMutex{}
	return nil
}

func (p *auditPlugin) OnReceive(pluginConfig plugin.Config, config []string, pkt *pb.Packet) error {
	dlpCount := decodeDlpSummary(pkt)
	switch pb.PacketType(pkt.GetType()) {
	case pbagent.PGConnectionWrite:
		isSimpleQuery, queryBytes, err := pg.SimpleQueryContent(pkt.Payload)
		if !isSimpleQuery {
			break
		}
		if err != nil {
			return fmt.Errorf("session=%v - failed obtaining simple query data, err=%v", pluginConfig.SessionId, err)
		}
		return p.writeOnReceive(pluginConfig.SessionId, 'i', dlpCount, queryBytes)
	case pbclient.WriteStdout,
		pbclient.WriteStderr:
		return p.writeOnReceive(pluginConfig.SessionId, 'o', dlpCount, pkt.Payload)
	case pbclient.SessionClose:
		defer p.closeSession(pluginConfig.SessionId)
		if len(pkt.Payload) > 0 {
			return p.writeOnReceive(pluginConfig.SessionId, 'e', dlpCount, pkt.Payload)
		}
	case pbagent.ExecWriteStdin,
		pbagent.TerminalWriteStdin,
		pbagent.TCPConnectionWrite:
		return p.writeOnReceive(pluginConfig.SessionId, 'i', dlpCount, pkt.Payload)
	}
	return nil
}

func (p *auditPlugin) OnDisconnect(config plugin.Config, errMsg error) error {
	p.log.With("session", config.SessionId, "origin", config.GetString("client")).
		Debugf("processing disconnect")
	switch config.GetString("client") {
	case pb.ConnectionOriginClient:
		defer p.closeSession(config.SessionId)
		if errMsg != nil {
			_ = p.writeOnReceive(config.SessionId, 'e', 0, []byte(errMsg.Error()))
			return nil
		}
	case pb.ConnectionOriginClientAPI:
		if errMsg != nil {
			// on errors, close the session right away
			_ = p.writeOnReceive(config.SessionId, 'e', 0, []byte(errMsg.Error()))
			p.closeSession(config.SessionId)
			return nil
		}
		// keep the connection open to let packets flow async
	case pb.ConnectionOriginAgent:
		agentID := config.GetString("disconnect-agent-id")
		if agentID != "" {
			p.log.Warnf("agent %v was shutdown, graceful closing sessions", agentID)
			// always close all sessions of this agent when it disconnects
			// there's no capability of recovering the state of execution
			// when this condition is present.
			for key := range config.ParamsData {
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
					p.closeSession(sessionID)
					continue
				}
				p.closeSession(sessionID)
			}
			return nil
		}
		// it close sessions that are being processed async
		// e.g.: when it receives a session close packet
		defer p.closeSession(config.SessionId)
		if errMsg != nil {
			_ = p.writeOnReceive(config.SessionId, 'e', 0, []byte(errMsg.Error()))
			return nil
		}
	}
	return nil
}

func (p *auditPlugin) closeSession(sessionID string) {
	go func() {
		if err := p.writeOnClose(sessionID); err != nil {
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
