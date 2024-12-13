package audit

import (
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/memory"
	mssqltypes "github.com/hoophq/hoop/common/mssqltypes"
	pgtypes "github.com/hoophq/hoop/common/pgtypes"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	pbclient "github.com/hoophq/hoop/common/proto/client"
	"github.com/hoophq/hoop/common/proto/spectypes"
	pgsession "github.com/hoophq/hoop/gateway/pgrest/session"
	eventlogv1 "github.com/hoophq/hoop/gateway/session/eventlog/v1"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
)

var memorySessionStore = memory.New()

type (
	auditPlugin struct {
		walSessionStore memory.Store
		started         bool
		mu              sync.RWMutex
	}
)

func New() *auditPlugin             { return &auditPlugin{walSessionStore: memory.New()} }
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
	log.With("sid", pctx.SID).Infof("processing on-connect")
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
	ctx := storagev2.NewContext(pctx.UserID, pctx.OrgID)
	err := pgsession.New().Upsert(ctx, types.Session{
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
		StartSession:     startDate,
		EndSession:       nil,
	})
	if err != nil {
		return fmt.Errorf("failed persisting sessino to store, reason=%v", err)
	}
	p.mu = sync.RWMutex{}
	memorySessionStore.Set(pctx.SID, pctx.AgentID)
	return nil
}

func (p *auditPlugin) OnReceive(pctx plugintypes.Context, pkt *pb.Packet) (*plugintypes.ConnectResponse, error) {
	eventMetadata := parseSpecAsEventMetadata(pkt)
	switch pb.PacketType(pkt.GetType()) {
	case pbagent.SessionOpen:
		// The session is never cleaned properly when the connection has a review
		// and the origin is the api. This is a workaround to remove the state.
		// In the future, we could fix it refactoring the way the api manages the session
		if _, ok := pkt.Spec[pb.SpecHasReviewKey]; ok && pctx.ClientOrigin != pb.ConnectionOriginClient {
			log.With("sid", pctx.SID, "origin", pctx.ClientOrigin, "verb", pctx.ClientVerb).
				Infof("this session is reviewed, cleaning up state")
			p.dropWalLog(pctx.SID)
			memorySessionStore.Del(pctx.SID)
		}
	case pbclient.PGConnectionWrite,
		pbclient.MySQLConnectionWrite,
		pbclient.MongoDBConnectionWrite:
		if len(eventMetadata) > 0 {
			return nil, p.writeOnReceive(pctx.SID, eventlogv1.OutputType, nil, eventMetadata)
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
		return nil, p.writeOnReceive(pctx.SID, eventlogv1.InputType, queryBytes, eventMetadata)
	case pbagent.MySQLConnectionWrite:
		if queryBytes := decodeMySQLCommandQuery(pkt.Payload); queryBytes != nil {
			return nil, p.writeOnReceive(pctx.SID, eventlogv1.InputType, queryBytes, eventMetadata)
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
				return nil, p.writeOnReceive(pctx.SID, eventlogv1.InputType, []byte(query), eventMetadata)
			}
		}
	case pbagent.MongoDBConnectionWrite:
		decJSONPayload, err := decodeClientMongoOpMsgPacket(pkt.Payload)
		if err != nil {
			return nil, err
		}
		if decJSONPayload != nil {
			return nil, p.writeOnReceive(pctx.SID, eventlogv1.InputType, decJSONPayload, eventMetadata)
		}
	case pbclient.WriteStdout,
		pbclient.WriteStderr:
		err := p.writeOnReceive(pctx.SID, eventlogv1.OutputType, pkt.Payload, eventMetadata)
		if err != nil {
			log.Warnf("failed writing agent packet response, err=%v", err)
		}
		return nil, nil
	case pbclient.SessionClose:
		if len(pkt.Payload) > 0 {
			p.closeSession(pctx, fmt.Errorf(string(pkt.Payload)))
			return nil, nil
		}
		p.closeSession(pctx, nil)
	case pbagent.ExecWriteStdin,
		pbagent.TerminalWriteStdin,
		pbagent.TCPConnectionWrite:
		return nil, p.writeOnReceive(pctx.SID, eventlogv1.InputType, pkt.Payload, eventMetadata)
	}
	return nil, nil
}

func (p *auditPlugin) OnDisconnect(pctx plugintypes.Context, errMsg error) error {
	log.With("sid", pctx.SID, "origin", pctx.ClientOrigin, "agent", pctx.AgentName).
		Debugf("processing disconnect")
	switch pctx.ClientOrigin {
	case pb.ConnectionOriginAgent:
		log.With("agent", pctx.AgentName).Infof("agent shutdown, graceful closing session")
		for msid, objAgentID := range memorySessionStore.List() {
			if pctx.AgentID != fmt.Sprintf("%v", objAgentID) {
				continue
			}
			pctx.SID = msid
			p.closeSession(pctx, errMsg)
		}
	default:
		p.closeSession(pctx, errMsg)
	}
	return nil
}

func (p *auditPlugin) closeSession(pctx plugintypes.Context, errMsg error) {
	log.With("sid", pctx.SID).Infof("closing session, reason=%v", errMsg)
	go func() {
		if err := p.writeOnClose(pctx, errMsg); err != nil {
			log.Warnf("session=%v - failed closing session: %v", pctx.SID, err)
			return
		}
		memorySessionStore.Del(pctx.SID)
	}()
}

func (p *auditPlugin) OnShutdown() {}

func parseSpecAsEventMetadata(pkt *pb.Packet) map[string][]byte {
	if dataMaskingInfo, ok := pkt.Spec[spectypes.DataMaskingInfoKey]; ok {
		return map[string][]byte{spectypes.DataMaskingInfoKey: dataMaskingInfo}
	}
	tsEnc := pkt.Spec[pb.SpecDLPTransformationSummary]
	if tsEnc == nil {
		return nil
	}
	// keep compatibility with old clients
	// in the future it'll be safe to remove this
	var ts []*pb.TransformationSummary
	if err := pb.GobDecodeInto(tsEnc, &ts); err != nil {
		log.With("plugin", "audit").Debugf("failed decoding dlp transformation summary, err=%v", err)
		return nil
	}
	if len(ts) == 0 {
		return nil
	}
	overviewItems := []*spectypes.TransformationOverview{}
	for _, t := range ts {
		overview := &spectypes.TransformationOverview{
			Err:       t.Err,
			Summaries: []spectypes.TransformationSummary{},
		}
		specTs := spectypes.TransformationSummary{Results: []spectypes.SummaryResult{}}
		for _, s := range t.SummaryResult {
			if len(s) != 3 {
				continue
			}
			count, err := strconv.ParseInt(s[0], 10, 64)
			if err != nil {
				count = -1
			}
			specTs.Results = append(specTs.Results, spectypes.SummaryResult{
				Count:   count,
				Code:    s[1],
				Details: s[2],
			})
		}
		overview.Summaries = append(overview.Summaries, specTs)
		overviewItems = append(overviewItems, overview)
	}
	infoEnc, _ := (&spectypes.DataMaskingInfo{Items: overviewItems}).Encode()
	if len(infoEnc) > 0 {
		return map[string][]byte{spectypes.DataMaskingInfoKey: infoEnc}
	}
	return nil
}
