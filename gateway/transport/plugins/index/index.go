package index

import (
	"fmt"
	"log"
	"os"

	"github.com/runopsio/hoop/common/memory"
	"github.com/runopsio/hoop/common/pg"
	pb "github.com/runopsio/hoop/common/proto"
	pbagent "github.com/runopsio/hoop/common/proto/agent"
	pbclient "github.com/runopsio/hoop/common/proto/client"
	"github.com/runopsio/hoop/gateway/indexer"
	"github.com/runopsio/hoop/gateway/plugin"
)

const Name string = "indexer"

type (
	indexPlugin struct {
		name            string
		started         bool
		indexers        memory.Store
		walSessionStore memory.Store
	}
)

func New() *indexPlugin {
	return &indexPlugin{
		name:            Name,
		indexers:        memory.New(),
		walSessionStore: memory.New(),
	}
}

func (p *indexPlugin) Name() string { return p.name }
func (p *indexPlugin) OnStartup(config plugin.Config) error {
	if p.started {
		return nil
	}
	log.Printf("session=%v | indexer | processing on-startup", config.SessionId)
	if err := os.MkdirAll(indexer.PluginIndexPath, 0755); err != nil {
		return fmt.Errorf("failed creating index path %v, err=%v", indexer.PluginIndexPath, err)
	}
	p.started = true
	return nil
}

func (p *indexPlugin) OnConnect(config plugin.Config) error {
	if config.Org == "" || config.SessionId == "" {
		return fmt.Errorf("failed processing indexer plugin, missing org_id and session_id params")
	}
	if ch := p.indexers.Get(config.Org); ch == nil {
		go func() {
			indexCh := make(chan *indexer.Session)
			p.indexers.Set(config.Org, indexCh)
			defer func() {
				close(indexCh)
				p.indexers.Del(config.Org)
				log.Printf("org=%v - closed indexer channel", config.Org)
			}()
			for s := range indexCh {
				log.Printf("session=%v - starting indexing", s.ID)
				index, err := indexer.Open(s.OrgID)
				if err != nil {
					log.Printf("session=%v - failed opening index, err=%v", s.ID, err)
					continue
				}
				if err := index.Index(s.ID, s); err != nil {
					log.Printf("session=%v - failed indexing session, err=%v", s.ID, err)
				}
				log.Printf("session=%v - indexed=%v, err=%v", s.ID, err == nil, err)
			}
		}()
	}
	return p.writeOnConnect(config)
}
func (p *indexPlugin) OnReceive(c plugin.Config, config []string, pkt *pb.Packet) error {
	switch pb.PacketType(pkt.GetType()) {
	case pbagent.PGConnectionWrite:
		isSimpleQuery, queryBytes, err := pg.SimpleQueryContent(pkt.Payload)
		if !isSimpleQuery {
			break
		}
		if err != nil {
			return fmt.Errorf("session=%v - failed obtaining simple query data, err=%v", c.SessionId, err)
		}
		return p.writeOnReceive(c.SessionId, "i", queryBytes)
	case pbclient.WriteStdout:
		return p.writeOnReceive(c.SessionId, "o", pkt.Payload)
	case pbclient.WriteStderr:
		return p.writeOnReceive(c.SessionId, "e", pkt.Payload)
	case pbagent.ExecWriteStdin, pbagent.TerminalWriteStdin:
		return p.writeOnReceive(c.SessionId, "i", pkt.Payload)
	case pbclient.SessionClose:
		isError := len(pkt.Payload) > 0
		defer p.indexOnClose(c, isError)
		if isError {
			return p.writeOnReceive(c.SessionId, "e", pkt.Payload)
		}
	}
	return nil
}

func (p *indexPlugin) OnDisconnect(config plugin.Config) error {
	if config.Org == "" || config.SessionId == "" {
		return fmt.Errorf("missing org_id and session_id")
	}
	if config.GetString("client") == pb.ConnectionOriginClient {
		p.indexOnClose(config, false)
	}
	return nil
}

func (p *indexPlugin) OnShutdown() {}
