package audit

import (
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/runopsio/hoop/common/pg"
	"github.com/runopsio/hoop/gateway/plugin"

	"github.com/runopsio/hoop/common/memory"
	pb "github.com/runopsio/hoop/common/proto"
	pbagent "github.com/runopsio/hoop/common/proto/agent"
	pbclient "github.com/runopsio/hoop/common/proto/client"
)

const (
	Name               string = "audit"
	StorageWriterParam string = "audit_storage_writer"
	defaultAuditPath   string = "/opt/hoop/sessions"
)

var pluginAuditPath string

func init() {
	pluginAuditPath = os.Getenv("PLUGIN_AUDIT_PATH")
	if pluginAuditPath == "" {
		pluginAuditPath = defaultAuditPath
	}
	if pluginAuditPath == "" {
		pluginAuditPath = defaultAuditPath
	}
}

type (
	auditPlugin struct {
		name            string
		storageWriter   StorageWriter
		walSessionStore memory.Store
		started         bool
		mu              sync.RWMutex
	}

	StorageWriter interface {
		Write(config plugin.Config) error
	}
)

func New() *auditPlugin {
	return &auditPlugin{name: Name, walSessionStore: memory.New()}
}

func (p *auditPlugin) Name() string {
	return p.name
}

func (p *auditPlugin) OnStartup(config plugin.Config) error {
	if p.started {
		return nil
	}

	if err := os.MkdirAll(pluginAuditPath, 0755); err != nil {
		return fmt.Errorf("failed creating audit path %v, err=%v", pluginAuditPath, err)
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
	log.Printf("session=%v | audit | processing on-connect", config.SessionId)
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
	dlpCount := int64(20) // get dlp count
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

func (p *auditPlugin) OnDisconnect(config plugin.Config) error {
	if config.Org == "" || config.SessionId == "" {
		return fmt.Errorf("missing org_id and session_id")
	}
	switch config.GetString("client") {
	case pb.ConnectionOriginClient:
		p.closeSession(config.SessionId)
	}
	return nil
}

func (p *auditPlugin) closeSession(sessionID string) {
	go func() {
		if err := p.writeOnClose(sessionID); err != nil {
			log.Printf("session=%v audit - failed closing session: %v", sessionID, err)
		}
	}()
}

func (p *auditPlugin) OnShutdown() {}
