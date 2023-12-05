package index

import (
	"fmt"
	"os"
	"time"

	"github.com/go-co-op/gocron"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/memory"
	mssqltypes "github.com/runopsio/hoop/common/mssql/types"
	pgtypes "github.com/runopsio/hoop/common/pgtypes"
	pb "github.com/runopsio/hoop/common/proto"
	pbagent "github.com/runopsio/hoop/common/proto/agent"
	pbclient "github.com/runopsio/hoop/common/proto/client"
	"github.com/runopsio/hoop/gateway/indexer"
	"github.com/runopsio/hoop/gateway/session"
	eventlogv0 "github.com/runopsio/hoop/gateway/session/eventlog/v0"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	plugintypes "github.com/runopsio/hoop/gateway/transport/plugins/types"
)

const defaultIndexJobStart = "23:30"

type (
	indexPlugin struct {
		sessionStore    *session.Storage
		indexers        memory.Store
		walSessionStore memory.Store
	}
)

func New(sessionStore *session.Storage) *indexPlugin {
	p := &indexPlugin{
		sessionStore:    sessionStore,
		indexers:        memory.New(),
		walSessionStore: memory.New(),
	}
	scheduler := gocron.NewScheduler(time.UTC).SingletonMode()
	scheduler.Every(1).Day().At(defaultIndexJobStart).Do(func() {
		log.Infof("job=index - starting")
		if err := indexer.StartJobIndex(p.sessionStore); err != nil {
			log.Infof("job=index - failed processing, err=%v", err)
		}
	})
	scheduler.StartAsync()
	return p
}

func (p *indexPlugin) Name() string { return plugintypes.PluginIndexName }
func (p *indexPlugin) OnStartup(_ plugintypes.Context) error {
	if fi, _ := os.Stat(plugintypes.IndexPath); fi == nil || !fi.IsDir() {
		return fmt.Errorf("failed to retrieve index path info, path=%v", plugintypes.IndexPath)
	}
	return nil
}
func (p *indexPlugin) OnUpdate(_, _ *types.Plugin) error { return nil }

func (p *indexPlugin) OnConnect(pctx plugintypes.Context) error {
	if ch := p.indexers.Get(pctx.OrgID); ch == nil {
		go func() {
			indexCh := make(chan *indexer.Session)
			p.indexers.Set(pctx.OrgID, indexCh)
			defer func() {
				close(indexCh)
				p.indexers.Del(pctx.OrgID)
				log.Infof("org=%v - closed indexer channel", pctx.OrgID)
			}()
			for s := range indexCh {
				log.With("sid", s.ID).Infof("starting indexing")
				index, err := indexer.NewIndexer(s.OrgID)
				if err != nil {
					log.With("sid", s.ID).Infof("failed opening index, err=%v", err)
					continue
				}
				err = index.Index(s.ID, s)
				log.With("sid", s.ID).Infof("indexed=%v, err=%v", err == nil, err)
			}
		}()
	}
	return p.writeOnConnect(pctx)
}

func (p *indexPlugin) OnReceive(c plugintypes.Context, pkt *pb.Packet) (*plugintypes.ConnectResponse, error) {
	switch pb.PacketType(pkt.GetType()) {
	case pbagent.PGConnectionWrite:
		isSimpleQuery, queryBytes, err := pgtypes.SimpleQueryContent(pkt.Payload)
		if !isSimpleQuery {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("session=%v - failed obtaining simple query data, err=%v", c.SID, err)
		}
		return nil, p.writeOnReceive(c.SID, eventlogv0.InputType, queryBytes)
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
				return nil, p.writeOnReceive(c.SID, eventlogv0.InputType, []byte(query))
			}
		}
	case pbclient.WriteStdout:
		return nil, p.writeOnReceive(c.SID, eventlogv0.OutputType, pkt.Payload)
	case pbclient.WriteStderr:
		return nil, p.writeOnReceive(c.SID, eventlogv0.ErrorType, pkt.Payload)
	case pbagent.ExecWriteStdin, pbagent.TerminalWriteStdin:
		return nil, p.writeOnReceive(c.SID, eventlogv0.InputType, pkt.Payload)
	case pbclient.SessionClose:
		isError := len(pkt.Payload) > 0
		defer p.indexOnClose(c, isError)
		if isError {
			return nil, p.writeOnReceive(c.SID, eventlogv0.ErrorType, pkt.Payload)
		}
	}
	return nil, nil
}

func (p *indexPlugin) OnDisconnect(pctx plugintypes.Context, errMsg error) error {
	if pctx.ClientOrigin == pb.ConnectionOriginClient ||
		pctx.ClientOrigin == pb.ConnectionOriginClientProxyManager {
		p.indexOnClose(pctx, false)
	}
	return nil
}

func (p *indexPlugin) OnShutdown() {}
