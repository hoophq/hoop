package audit

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/memory"
	mssqltypes "github.com/hoophq/hoop/common/mssqltypes"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	pbclient "github.com/hoophq/hoop/common/proto/client"
	"github.com/hoophq/hoop/common/proto/spectypes"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	eventlogv1 "github.com/hoophq/hoop/gateway/session/eventlog/v1"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
)

var memorySessionStore = memory.New()

type auditPlugin struct {
	walSessionStore memory.Store
	started         bool
	mu              sync.RWMutex
}

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
func (p *auditPlugin) OnUpdate(_, _ plugintypes.PluginResource) error { return nil }
func (p *auditPlugin) OnConnect(pctx plugintypes.Context) error {
	log.With("sid", pctx.SID).Infof("processing on-connect")
	if pctx.OrgID == "" || pctx.SID == "" {
		return fmt.Errorf("failed processing audit plugin, missing org_id and session_id params")
	}
	startDate := time.Now().UTC()
	pctx.ParamsData["status"] = string(openapi.SessionStatusOpen)
	pctx.ParamsData["start_date"] = &startDate
	if err := p.writeOnConnect(pctx); err != nil {
		return err
	}

	// persist session for public gRPC clients
	if !strings.HasPrefix(pctx.ClientOrigin, pb.ConnectionOriginClientAPI) {
		err := models.UpsertSession(models.Session{
			ID:                   pctx.SID,
			OrgID:                pctx.OrgID,
			UserEmail:            pctx.UserEmail,
			UserID:               pctx.UserID,
			UserName:             pctx.UserName,
			Connection:           pctx.ConnectionName,
			ConnectionType:       pctx.ConnectionType,
			ConnectionSubtype:    pctx.ConnectionSubType,
			ConnectionTags:       pctx.ConnectionTags,
			Verb:                 pctx.ClientVerb,
			Labels:               nil,
			Metadata:             nil,
			IntegrationsMetadata: nil,
			Status:               string(openapi.SessionStatusOpen),
			ExitCode:             nil,
			CreatedAt:            startDate,
			EndSession:           nil,
		})
		if err != nil {
			return fmt.Errorf("failed persisting session to store, reason=%v", err)
		}
	}
	p.mu = sync.RWMutex{}
	memorySessionStore.Set(pctx.SID, pctx.AgentID)
	return nil
}

func (p *auditPlugin) OnReceive(pctx plugintypes.Context, pkt *pb.Packet) (*plugintypes.ConnectResponse, error) {
	eventMetadata := parseSpecAsEventMetadata(pkt)
	switch pb.PacketType(pkt.GetType()) {
	case pbagent.SessionOpen:
		// update session input when executing ad-hoc executions via cli
		if strings.HasPrefix(pctx.ClientOrigin, pb.ConnectionOriginClient) {
			if err := models.UpdateSessionInput(pctx.OrgID, pctx.SID, string(pkt.Payload)); err != nil {
				return nil, plugintypes.InternalErr("failed updating session input: %v", err)
			}
		}

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
		return nil, p.writeOnReceive(pctx.SID, eventlogv1.InputType, pkt.Payload, eventMetadata)
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
	case pbagent.ExecWriteStdin,
		pbagent.TerminalWriteStdin,
		pbagent.TCPConnectionWrite:
		return nil, p.writeOnReceive(pctx.SID, eventlogv1.InputType, pkt.Payload, eventMetadata)
	}
	return nil, nil
}

func (p *auditPlugin) OnDisconnect(pctx plugintypes.Context, err error) error {
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
			p.closeSession(pctx, err)
		}
	default:
		p.closeSession(pctx, err)
	}
	return nil
}

func (p *auditPlugin) closeSession(pctx plugintypes.Context, err error) {
	log.With("sid", pctx.SID, "origin", pctx.ClientOrigin, "verb", pctx.ClientVerb).
		Infof("closing session, reason=%v", err)
	go func() {
		defer memorySessionStore.Del(pctx.SID)
		if err := p.writeOnClose(pctx, err); err != nil {
			log.With("sid", pctx.SID, "origin", pctx.ClientOrigin, "verb", pctx.ClientVerb).
				Warnf("failed closing session, reason=%v", err)
		}
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
