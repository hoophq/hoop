package audit

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/smithy-go/ptr"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/memory"
	mssqltypes "github.com/hoophq/hoop/common/mssqltypes"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	pbclient "github.com/hoophq/hoop/common/proto/client"
	"github.com/hoophq/hoop/common/proto/spectypes"
	"github.com/hoophq/hoop/gateway/analytics"
	"github.com/hoophq/hoop/gateway/api/openapi"
	sessionapi "github.com/hoophq/hoop/gateway/api/session"
	"github.com/hoophq/hoop/gateway/models"
	eventlogv1 "github.com/hoophq/hoop/gateway/session/eventlog/v1"
	"github.com/hoophq/hoop/gateway/session/interactionbroker"
	"github.com/hoophq/hoop/gateway/storagev2"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
)

var memorySessionStore = memory.New()

const (
	identityTypeMachine = "machine"
	identityTypeUser    = "user"
)

// machineSessionState tracks the current interaction WAL and sequence counter for a machine session.
type machineSessionState struct {
	mu              sync.Mutex
	currentSequence int
	currentWAL      *walLogRWMutex
	startDate       *time.Time // start of current interaction
	sessionID       string
	orgID           string
}

type auditPlugin struct {
	walSessionStore     memory.Store // human sessions: key=sessionID
	machineSessionStore memory.Store // machine sessions: key=sessionID, value=*machineSessionState
	started             bool
	mu                  sync.RWMutex
}

func New() *auditPlugin {
	return &auditPlugin{
		walSessionStore:     memory.New(),
		machineSessionStore: memory.New(),
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

	isMachine := pctx.IdentityType == identityTypeMachine
	if isMachine {
		// machine sessions: don't open a WAL yet (no interaction started)
		p.machineSessionStore.Set(pctx.SID, &machineSessionState{
			sessionID: pctx.SID,
			orgID:     pctx.OrgID,
		})
	} else {
		if err := p.writeOnConnect(pctx); err != nil {
			return err
		}
	}

	// persist session for public gRPC clients
	if !strings.HasPrefix(pctx.ClientOrigin, pb.ConnectionOriginClientAPI) {
		ctx := storagev2.NewContext(pctx.UserID, pctx.OrgID)
		ctx.WithUserInfo(pctx.UserName, pctx.UserEmail, "active", "", pctx.UserGroups)

		connection, err := models.GetConnectionByNameOrID(ctx, pctx.ConnectionName)
		if err != nil {
			return err
		}

		if connection == nil {
			return fmt.Errorf("connection not found")
		}

		var sessionMetadata map[string]any
		if pctx.CredentialSessionID != "" {
			sessionMetadata = map[string]any{"credential_session": pctx.CredentialSessionID}
		}

		sessionIdentityType := identityTypeUser
		if isMachine {
			sessionIdentityType = identityTypeMachine
		}

		newSession := models.Session{
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
			Metadata:             sessionMetadata,
			IntegrationsMetadata: nil,
			Status:               string(openapi.SessionStatusOpen),
			ExitCode:             nil,
			IdentityType:         sessionIdentityType,
			CreatedAt:            startDate,
			EndSession:           nil,
		}
		if pctx.CorrelationID != "" {
			newSession.CorrelationID = &pctx.CorrelationID
		}

		if err := models.UpsertSession(newSession); err != nil {
			return fmt.Errorf("failed persisting session to store, reason=%v", err)
		}

		trackClient := analytics.New()
		defer trackClient.Close()

		trackClient.TrackSessionUsageData(analytics.EventSessionCreated, pctx.OrgID, pctx.UserID, pctx.SID)
	}
	p.mu = sync.RWMutex{}
	memorySessionStore.Set(pctx.SID, pctx.AgentID)
	return nil
}

func (p *auditPlugin) OnReceive(pctx plugintypes.Context, pkt *pb.Packet) (*plugintypes.ConnectResponse, error) {
	// handle interaction close for machine sessions
	if pb.PacketType(pkt.GetType()) == pbclient.InteractionClose && pctx.IdentityType == identityTypeMachine {
		return nil, p.closeInteraction(pctx, pkt)
	}

	eventMetadata := parseSpecAsEventMetadata(pkt)
	switch pb.PacketType(pkt.GetType()) {
	case pbagent.SessionOpen:
		// update session input when executing ad-hoc executions via cli
		if pctx.ClientOrigin == pb.ConnectionOriginClient && pctx.ClientVerb == "exec" {
			if err := models.UpdateSessionInput(pctx.OrgID, pctx.SID, string(pkt.Payload)); err != nil {
				return nil, plugintypes.InternalErr("failed updating session input", err)
			}

			// Check AI
			orgID := uuid.MustParse(pctx.OrgID)
			analyzeRes, err := sessionapi.AIAnalyze(pctx.Context, orgID, pctx.ConnectionName, string(pkt.Payload))
			if err != nil {
				log.With("sid", pctx.SID, "org_id", pctx.OrgID).Errorf("failed analyzing session input with AI, err=%v", err)
				return nil, plugintypes.InternalErr("failed analyzing session input with AI", err)
			}

			if analyzeRes != nil {
				session, err := models.GetSessionByID(pctx.OrgID, pctx.SID)
				if err != nil {
					return nil, plugintypes.InternalErr("failed getting session by ID", err)
				}

				session.AIAnalysis = analyzeRes

				shouldBlock := analyzeRes.Action == string(models.BlockExecution)
				if shouldBlock {
					session.Status = string(openapi.SessionStatusDone)
					session.ExitCode = internalExitCode
					endTime := time.Now().UTC()
					session.EndSession = &endTime
				}

				if err := models.UpsertSession(*session); err != nil {
					log.Errorf("failed updating session, err=%v", err)
					return nil, plugintypes.InternalErr("failed updating session", err)
				}
				trackClient := analytics.New()
				defer trackClient.Close()

				trackClient.TrackSessionUsageData(analytics.EventSessionFinished, pctx.OrgID, pctx.UserID, pctx.SID)

				if shouldBlock {
					return nil, plugintypes.NewPacketErr("session blocked by AI risk analyzer", nil)
				}
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
			return nil, p.dispatchWrite(pctx, eventlogv1.OutputType, nil, eventMetadata)
		}
	case pbagent.PGConnectionWrite:
		return nil, p.dispatchWrite(pctx, eventlogv1.InputType, pkt.Payload, eventMetadata)
	case pbagent.MySQLConnectionWrite:
		if queryBytes := decodeMySQLCommandQuery(pkt.Payload); queryBytes != nil {
			return nil, p.dispatchWrite(pctx, eventlogv1.InputType, queryBytes, eventMetadata)
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
				return nil, p.dispatchWrite(pctx, eventlogv1.InputType, []byte(query), eventMetadata)
			}
		}
	case pbagent.MongoDBConnectionWrite:
		decJSONPayload, err := decodeClientMongoOpMsgPacket(pkt.Payload)
		if err != nil {
			return nil, err
		}
		if decJSONPayload != nil {
			return nil, p.dispatchWrite(pctx, eventlogv1.InputType, decJSONPayload, eventMetadata)
		}
	case pbclient.WriteStdout, pbclient.WriteStderr:
		err := p.dispatchWrite(pctx, eventlogv1.OutputType, pkt.Payload, eventMetadata)
		if err != nil {
			log.Warnf("failed writing agent packet response, err=%v", err)
		}
		return nil, nil
	case pbagent.ExecWriteStdin, pbagent.TerminalWriteStdin, pbagent.TCPConnectionWrite:
		return nil, p.dispatchWrite(pctx, eventlogv1.InputType, pkt.Payload, eventMetadata)
	case pbclient.SSHConnectionWrite:
		return nil, p.dispatchWrite(pctx, eventlogv1.OutputType, pkt.Payload, eventMetadata)
	case pbagent.SSHConnectionWrite:
		return nil, p.dispatchWrite(pctx, eventlogv1.InputType, pkt.Payload, eventMetadata)
	case pbagent.HttpProxyConnectionWrite:
		return nil, p.dispatchWrite(pctx, eventlogv1.InputType, pkt.Payload, eventMetadata)
	case pbclient.HttpProxyConnectionWrite:
		return nil, p.dispatchWrite(pctx, eventlogv1.OutputType, pkt.Payload, eventMetadata)
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

		if pctx.IdentityType == identityTypeMachine {
			p.closeMachineSession(pctx, err)
		} else {
			if err := p.writeOnClose(pctx, err); err != nil {
				log.With("sid", pctx.SID, "origin", pctx.ClientOrigin, "verb", pctx.ClientVerb).
					Warnf("failed closing session, reason=%v", err)
			}
		}

		trackClient := analytics.New()
		defer trackClient.Close()

		_ = models.SetSessionMetricsEndedAt(models.DB, pctx.SID)
		trackClient.TrackSessionUsageData(analytics.EventSessionFinished, pctx.OrgID, pctx.UserID, pctx.SID)
	}()
}

// closeMachineSession flushes any in-flight interaction WAL and marks the session as done.
func (p *auditPlugin) closeMachineSession(pctx plugintypes.Context, errMsg error) {
	stateObj := p.machineSessionStore.Pop(pctx.SID)
	state, ok := stateObj.(*machineSessionState)
	if !ok {
		log.With("sid", pctx.SID).Warnf("no machine session state found on disconnect")
		p.markSessionDone(pctx, errMsg)
		interactionbroker.Default.PublishAndRemove(pctx.SID, interactionbroker.InteractionEvent{
			Type: interactionbroker.SessionEnded,
		})
		return
	}

	state.mu.Lock()
	defer state.mu.Unlock()

	// flush in-flight interaction WAL if any
	if state.currentWAL != nil {
		walogm := state.currentWAL
		state.currentWAL = nil

		walogm.mu.Lock()
		defer func() { _ = walogm.log.Close(); walogm.mu.Unlock() }()

		// write final error if present
		if errMsg != nil && errMsg != io.EOF {
			_ = walogm.log.Write(eventlogv1.New(time.Now().UTC(), eventlogv1.ErrorType, []byte(errMsg.Error()), nil))
		}

		protocolConnectionType := pctx.ProtoConnectionType()
		result, err := p.drainWAL(walogm, protocolConnectionType, state.startDate)
		if err != nil {
			log.With("sid", pctx.SID).Warnf("failed draining in-flight interaction WAL: %v", err)
		} else {
			var blobFormat *string
			switch protocolConnectionType {
			case pb.ConnectionTypePostgres:
				blobFormat = ptr.String(models.BlobFormatWireProtoType)
			}

			interactionID := uuid.NewString()
			endDate := time.Now().UTC()
			err = models.CreateInteractionWithBlobs(
				models.DB,
				models.SessionInteraction{
					ID:        interactionID,
					SessionID: pctx.SID,
					OrgID:     pctx.OrgID,
					Sequence:  state.currentSequence,
					ExitCode:  parseExitCodeFromErr(errMsg),
					CreatedAt: *state.startDate,
					EndedAt:   &endDate,
				},
				nil,
				json.RawMessage(result.rawJSONBlobStream),
				blobFormat,
			)
			if err != nil {
				log.With("sid", pctx.SID).Warnf("failed persisting in-flight interaction: %v", err)
			} else {
				_ = os.RemoveAll(walogm.folderName)
				interactionbroker.Default.Publish(pctx.SID, interactionbroker.InteractionEvent{
					Type: interactionbroker.InteractionCreated, Sequence: state.currentSequence,
				})
			}
		}
	}

	p.markSessionDone(pctx, errMsg)
	interactionbroker.Default.PublishAndRemove(pctx.SID, interactionbroker.InteractionEvent{
		Type: interactionbroker.SessionEnded,
	})
}

func (p *auditPlugin) markSessionDone(pctx plugintypes.Context, errMsg error) {
	endDate := time.Now().UTC()
	err := models.UpdateSessionEventStream(models.SessionDone{
		ID:         pctx.SID,
		OrgID:      pctx.OrgID,
		Metrics:    make(map[string]any),
		BlobStream: json.RawMessage(`[]`),
		Status:     string(openapi.SessionStatusDone),
		ExitCode:   parseExitCodeFromErr(errMsg),
		EndSession: &endDate,
	})
	if err != nil {
		log.With("sid", pctx.SID).Warnf("failed marking machine session as done: %v", err)
	}
}

// dispatchWrite routes WAL writes to either the session WAL (human) or interaction WAL (machine).
func (p *auditPlugin) dispatchWrite(pctx plugintypes.Context, eventType eventlogv1.EventType, event []byte, metadata map[string][]byte) error {
	if pctx.IdentityType == identityTypeMachine {
		return p.writeOnReceiveMachine(pctx, eventType, event, metadata)
	}
	return p.writeOnReceive(pctx.SID, eventType, event, metadata)
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
