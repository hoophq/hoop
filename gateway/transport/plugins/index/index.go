package index

import (
	"fmt"
	"os"
	"time"

	"github.com/go-co-op/gocron"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/memory"
	"github.com/runopsio/hoop/common/pg"
	pb "github.com/runopsio/hoop/common/proto"
	pbagent "github.com/runopsio/hoop/common/proto/agent"
	pbclient "github.com/runopsio/hoop/common/proto/client"
	"github.com/runopsio/hoop/gateway/indexer"
	"github.com/runopsio/hoop/gateway/plugin"
	"github.com/runopsio/hoop/gateway/session"
)

const (
	Name                 = "indexer"
	defaultIndexJobStart = "23:30"
)

type (
	indexPlugin struct {
		name            string
		sessionStore    *session.Storage
		pluginStore     *plugin.Storage
		indexers        memory.Store
		walSessionStore memory.Store
	}
)

func New(sessionStore *session.Storage, pluginStore *plugin.Storage) *indexPlugin {
	p := &indexPlugin{
		name:            Name,
		sessionStore:    sessionStore,
		pluginStore:     pluginStore,
		indexers:        memory.New(),
		walSessionStore: memory.New(),
	}
	scheduler := gocron.NewScheduler(time.UTC).SingletonMode()
	scheduler.Every(1).Day().At(defaultIndexJobStart).Do(func() {
		log.Printf("job=index - starting")
		if err := indexer.StartJobIndex(p.sessionStore, p.pluginStore); err != nil {
			log.Printf("job=index - failed processing, err=%v", err)
		}
	})
	scheduler.StartAsync()
	return p
}

func (p *indexPlugin) Name() string { return p.name }
func (p *indexPlugin) OnStartup(config plugin.Config) error {
	if fi, _ := os.Stat(plugin.IndexPath); fi == nil || !fi.IsDir() {
		return fmt.Errorf("failed to retrieve index path info, path=%v", plugin.IndexPath)
	}
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
				index, err := indexer.NewIndexer(s.OrgID)
				if err != nil {
					log.Printf("session=%v - failed opening index, err=%v", s.ID, err)
					continue
				}
				err = index.Index(s.ID, s)
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
