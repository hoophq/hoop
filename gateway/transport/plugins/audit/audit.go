package audit

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"os"
	"sync"
	"time"

	"github.com/runopsio/hoop/common/pg"
	"github.com/runopsio/hoop/gateway/plugin"

	"github.com/runopsio/hoop/common/memory"
	pb "github.com/runopsio/hoop/common/proto"
)

const (
	Name               string = "audit"
	StorageWriterParam string = "audit_storage_writer"
	defaultAuditPath   string = "/opt/hoop/auditdb"
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

	if err := p.writeOnConnect(config.Org, config.SessionId, config.User,
		config.ConnectionName, config.ConnectionType); err != nil {
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
	switch pb.PacketType(pkt.GetType()) {
	case pb.PacketPGWriteServerType:
		isSimpleQuery, queryBytes, err := simpleQueryContent(pkt.GetPayload())
		if !isSimpleQuery {
			break
		}
		if err != nil {
			return fmt.Errorf("session-id=%v - failed obtaining simple query data, err=%v", pluginConfig.SessionId, err)
		}
		return p.writeOnReceive(pluginConfig.SessionId, 'i', queryBytes)
	case pb.PacketTerminalClientWriteStdoutType:
		return p.writeOnReceive(pluginConfig.SessionId, 'o', pkt.GetPayload())
	case pb.PacketTerminalWriteAgentStdinType,
		pb.PacketTCPWriteServerType:
		return p.writeOnReceive(pluginConfig.SessionId, 'i', pkt.GetPayload())
	}
	return nil
}

func (p *auditPlugin) OnDisconnect(config plugin.Config) error {
	if config.GetString("client") == "agent" {
		return nil
	}
	if config.Org == "" || config.SessionId == "" {
		return fmt.Errorf("missing org_id and session_id")
	}
	go func() {
		// give some time to disconnect it, otherwise the on-receive process will
		// catch up a wal close file
		time.Sleep(time.Second * 3)
		if err := p.writeOnClose(config.SessionId); err != nil {
			log.Printf("session=%v audit - %v", config.SessionId, err)
		}
	}()
	return nil
}

func (p *auditPlugin) OnShutdown() {}

func simpleQueryContent(payload []byte) (bool, []byte, error) {
	r := bufio.NewReaderSize(bytes.NewBuffer(payload), pg.DefaultBufferSize)
	typ, err := r.ReadByte()
	if err != nil {
		return false, nil, fmt.Errorf("failed reading first byte: %v", err)
	}
	if pg.PacketType(typ) != pg.ClientSimpleQuery {
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
