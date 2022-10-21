package audit

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"os"
	"sync"
	"time"

	"github.com/runopsio/hoop/common/memory"
	"github.com/runopsio/hoop/common/pg"
	pgtypes "github.com/runopsio/hoop/common/pg/types"
	pb "github.com/runopsio/hoop/common/proto"
	pluginscore "github.com/runopsio/hoop/gateway/transport/plugins"
)

const (
	Name             string = "audit"
	defaultAuditPath string = "/opt/hoop/auditdb"
	defaultFlushSec  int    = 30
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

type auditPlugin struct {
	storageWriter   pluginscore.StorageWriter
	walSessionStore memory.Store
	enabled         bool
	started         bool
	mu              sync.RWMutex
}

func New() pluginscore.Plugin {
	return &auditPlugin{walSessionStore: memory.New()}
}

func (p *auditPlugin) Name() string {
	return Name
}

func (p *auditPlugin) OnStartup(c pluginscore.PluginConfig) error {
	if !c.Enabled() || p.started {
		return nil
	}

	if err := os.MkdirAll(pluginAuditPath, 0755); err != nil {
		return fmt.Errorf("failed creating audit path %v, err=%v", pluginAuditPath, err)
	}

	storageWriterObj := c.Config().Get("audit_storage_writer")
	storageWriter, ok := storageWriterObj.(pluginscore.StorageWriter)

	if !ok {
		return fmt.Errorf("audit_storage_writer config must be an pluginscore.StorageWriter instance")
	}
	p.enabled = true
	p.started = true
	p.storageWriter = storageWriter
	return nil
}

func (p *auditPlugin) OnConnect(i pluginscore.ParamsData) error {
	if !p.enabled {
		return nil
	}
	orgID := i.GetString("org_id")
	sessionID := i.GetString("session_id")
	log.Printf("sessionid=%v | audit | processing on-connect", sessionID)
	if orgID == "" || sessionID == "" {
		return fmt.Errorf("failed processing audit plugin, missing org_id and session_id params")
	}

	if err := p.writeOnConnect(orgID, sessionID, i.GetString("user_id"),
		i.GetString("connection_name"), i.GetString("connection_type")); err != nil {
		return err
	}
	i["start_date"] = func() *time.Time { d := time.Now().UTC(); return &d }()
	// Persist the session in the storage
	if err := p.storageWriter.Write(i); err != nil {
		return err
	}
	p.mu = sync.RWMutex{}
	return nil
}

func (p *auditPlugin) OnReceive(sessionID string, pkt pluginscore.PacketData) error {
	if !p.enabled {
		return nil
	}
	switch pb.PacketType(pkt.GetType()) {
	case pb.PacketPGWriteServerType:
		isSimpleQuery, queryBytes, err := simpleQueryContent(pkt.GetPayload())
		if !isSimpleQuery {
			break
		}
		if err != nil {
			return fmt.Errorf("session-id=%v - failed obtaining simple query data, err=%v", sessionID, err)
		}
		return p.writeOnReceive(sessionID, 'i', queryBytes)
	case pb.PacketExecClientWriteStdoutType:
		return p.writeOnReceive(sessionID, 'o', pkt.GetPayload())
	case pb.PacketExecWriteAgentStdinType, pb.PacketExecRunProcType:
		return p.writeOnReceive(sessionID, 'i', pkt.GetPayload())
	}
	return nil
}

func (p *auditPlugin) OnDisconnect(i pluginscore.ParamsData) error {
	if !p.enabled || i.GetString("client") == "agent" {
		return nil
	}
	orgID := i.GetString("org_id")
	sessionID := i.GetString("session_id")
	if orgID == "" || sessionID == "" {
		return fmt.Errorf("missing org_id and session_id")
	}
	go func() {
		// give some time to disconnect it, otherwise the on-receive process will
		// catch up a wal close file
		time.Sleep(time.Second * 3)
		if err := p.writeOnClose(sessionID); err != nil {
			log.Printf("sessionid=%v audit - %v", sessionID, err)
		}
	}()
	return nil
}

func (p *auditPlugin) OnShutdown() {}

func simpleQueryContent(payload []byte) (bool, []byte, error) {
	r := pg.NewReader(bytes.NewBuffer(payload))
	typ, err := r.ReadByte()
	if err != nil {
		return false, nil, fmt.Errorf("failed reading first byte: %v", err)
	}
	if pgtypes.PacketType(typ) != pgtypes.ClientSimpleQuery {
		return false, nil, nil
	}

	header := [4]byte{}
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return true, nil, fmt.Errorf("failed reading header, err=%v", err)
	}
	pktLen := binary.BigEndian.Uint32(header[:]) - 4 // don't include header size (4)
	queryFrame := make([]byte, pktLen)
	if _, err := io.ReadFull(r, queryFrame); err != nil {
		return true, nil, fmt.Errorf("failed reading query, err=%v", err)
	}
	return true, queryFrame, nil
}
