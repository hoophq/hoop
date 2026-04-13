package audit

import (
	"encoding/base64"
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
	pb "github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/common/proto/spectypes"
	"github.com/hoophq/hoop/gateway/analytics"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	eventlogv1 "github.com/hoophq/hoop/gateway/session/eventlog/v1"
	"github.com/hoophq/hoop/gateway/session/wal"
	sessionwal "github.com/hoophq/hoop/gateway/session/wal"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
)

// <audit-path>/<orgid>-<sessionid>-wal
const (
	walFolderTmpl            string = `%s/%s-%s-wal`
	walInteractionFolderTmpl string = `%s/%s-%s-%d-wal`
	eventLogTypeName         string = "_footer_error"
)

var internalExitCode = func() *int { v := 254; return &v }()

type walLogRWMutex struct {
	log        *sessionwal.WalLog
	mu         sync.RWMutex
	folderName string
}

func (p *auditPlugin) writeOnConnect(pctx plugintypes.Context) error {
	walFolder := fmt.Sprintf(walFolderTmpl, plugintypes.AuditPath, pctx.OrgID, pctx.SID)
	// sometimes a client could execute the same session id (review flow bug)
	if fi, _ := os.Stat(walFolder); fi != nil {
		_ = os.RemoveAll(walFolder)
	}

	walog, err := sessionwal.OpenWriteHeader(walFolder, &sessionwal.Header{
		EventLogVersion: eventlogv1.Version,
		OrgID:           pctx.OrgID,
		SessionID:       pctx.SID,
		Status:          pctx.ParamsData.GetString("status"),
		StartDate:       pctx.ParamsData.GetTime("start_date"),
	})
	if err != nil {
		return fmt.Errorf("failed opening wal file, err=%v", err)
	}
	p.walSessionStore.Set(pctx.SID, &walLogRWMutex{walog, sync.RWMutex{}, walFolder})
	return nil
}

func (p *auditPlugin) writeOnReceive(sessionID string, eventType eventlogv1.EventType, event []byte, metadata map[string][]byte) error {
	walLogObj := p.walSessionStore.Get(sessionID)
	walogm, ok := walLogObj.(*walLogRWMutex)
	if !ok {
		return fmt.Errorf("failed obtaining write ahead log for session %v", sessionID)
	}
	walogm.mu.Lock()
	defer walogm.mu.Unlock()
	return walogm.log.Write(eventlogv1.New(time.Now().UTC(), eventType, event, metadata))
}

// writeOnReceiveMachine writes an event to the current interaction WAL for a machine session,
// lazily opening a new interaction WAL if none exists (first packet of a new interaction).
func (p *auditPlugin) writeOnReceiveMachine(pctx plugintypes.Context, eventType eventlogv1.EventType, event []byte, metadata map[string][]byte) error {
	stateObj := p.machineSessionStore.Get(pctx.SID)
	state, ok := stateObj.(*machineSessionState)
	if !ok {
		return fmt.Errorf("failed obtaining machine session state for session %v", pctx.SID)
	}

	state.mu.Lock()
	defer state.mu.Unlock()

	// lazily open a new interaction WAL
	if state.currentWAL == nil {
		state.currentSequence++
		now := time.Now().UTC()
		state.startDate = &now
		walFolder := fmt.Sprintf(walInteractionFolderTmpl, plugintypes.AuditPath, state.orgID, state.sessionID, state.currentSequence)

		if fi, _ := os.Stat(walFolder); fi != nil {
			_ = os.RemoveAll(walFolder)
		}

		walog, err := sessionwal.OpenWriteHeader(walFolder, &sessionwal.Header{
			EventLogVersion: eventlogv1.Version,
			OrgID:           state.orgID,
			SessionID:       state.sessionID,
			Status:          string(openapi.SessionStatusOpen),
			StartDate:       state.startDate,
		})
		if err != nil {
			return fmt.Errorf("failed opening interaction wal file: %v", err)
		}
		state.currentWAL = &walLogRWMutex{log: walog, folderName: walFolder}
	}

	state.currentWAL.mu.Lock()
	defer state.currentWAL.mu.Unlock()
	return state.currentWAL.log.Write(eventlogv1.New(time.Now().UTC(), eventType, event, metadata))
}

func (p *auditPlugin) dropWalLog(sid string) {
	walLogObj := p.walSessionStore.Pop(sid)
	walogm, ok := walLogObj.(*walLogRWMutex)
	if !ok {
		return
	}
	walogm.mu.Lock()
	_ = walogm.log.Close()
	if err := os.RemoveAll(walogm.folderName); err != nil {
		log.Errorf("failed removing wal file %q, err=%v", walogm.folderName, err)
	}
	walogm.mu.Unlock()
}

// drainWALResult holds the output of reading and encoding a WAL's contents.
type drainWALResult struct {
	rawJSONBlobStream string
	metrics           SessionMetric
}

// drainWAL reads all events from a WAL, encodes them as a JSON blob stream, and aggregates DLP metrics.
// This is the shared logic used by both writeOnClose (human sessions) and closeInteraction (machine sessions).
func (p *auditPlugin) drainWAL(walogm *walLogRWMutex, protocolConnectionType pb.ConnectionType, startDate *time.Time) (*drainWALResult, error) {
	var rawJSONBlobStream string
	metrics := newSessionMetric()
	var err error
	metrics.Truncated, err = walogm.log.ReadFull(func(data []byte) error {
		ev, err := eventlogv1.Decode(data)
		if err != nil {
			return err
		}
		if infoEnc := ev.GetMetadata(spectypes.DataMaskingInfoKey); infoEnc != nil {
			dataMaskingInfo, err := spectypes.Decode(infoEnc)
			if err != nil {
				log.Warnf("failed decoding data masking info, reason=%v", err)
				dataMaskingInfo = &spectypes.DataMaskingInfo{}
			}
			for _, item := range dataMaskingInfo.Items {
				if item == nil {
					continue
				}
				metrics.DataMasking.TransformedBytes += item.TransformedBytes
				if item.Err != nil {
					metrics.DataMasking.ErrCount++
					continue
				}

				for _, ts := range item.Summaries {
					var redactPerInfoType int64
					for _, rs := range ts.Results {
						switch rs.Code {
						case "SUCCESS":
							metrics.DataMasking.TotalRedactCount += int64(rs.Count)
							redactPerInfoType += int64(rs.Count)
						case "ERROR":
							metrics.DataMasking.ErrCount++
						}
					}
					if ts.InfoType == "" {
						continue
					}
					metrics.addInfoType(ts.InfoType, redactPerInfoType)
				}
			}
		}

		if len(ev.Payload) == 0 {
			return nil
		}

		eventStream := p.truncateTCPEventStream(ev.Payload, protocolConnectionType)
		eventList := fmt.Sprintf("[%v, %q, %q],",
			ev.EventTime.Sub(*startDate).Seconds(),
			string(ev.EventType),
			base64.StdEncoding.EncodeToString(eventStream),
		)
		rawJSONBlobStream += eventList
		return nil
	})
	if err != nil {
		return nil, err
	}

	rawJSONBlobStream = fmt.Sprintf("[%v]", strings.TrimSuffix(rawJSONBlobStream, ","))
	metrics.EventSize = int64(len(rawJSONBlobStream))
	return &drainWALResult{rawJSONBlobStream: rawJSONBlobStream, metrics: metrics}, nil
}

// closeInteraction drains the current interaction WAL for a machine session,
// writes blobs and a session_interactions row, and prepares for the next interaction.
func (p *auditPlugin) closeInteraction(pctx plugintypes.Context, pkt *pb.Packet) error {
	stateObj := p.machineSessionStore.Get(pctx.SID)
	state, ok := stateObj.(*machineSessionState)
	if !ok {
		return fmt.Errorf("failed obtaining machine session state for session %v", pctx.SID)
	}

	state.mu.Lock()
	defer state.mu.Unlock()

	if state.currentWAL == nil {
		log.With("sid", pctx.SID).Warnf("interaction close received but no interaction WAL is open")
		return nil
	}

	walogm := state.currentWAL
	sequence := state.currentSequence
	startDate := state.startDate

	// clear current WAL so a new interaction can start
	state.currentWAL = nil
	state.startDate = nil

	walogm.mu.Lock()
	defer func() { _ = walogm.log.Close(); walogm.mu.Unlock() }()

	// write final error event if the packet carries one
	exitCode := parseExitCodeFromInteractionPacket(pkt)
	if len(pkt.Payload) > 0 {
		err := walogm.log.Write(eventlogv1.New(time.Now().UTC(), eventlogv1.ErrorType, pkt.Payload, nil))
		if err != nil {
			log.With("sid", pctx.SID).Warnf("failed writing interaction end error, err=%v", err)
		}
	}

	protocolConnectionType := pctx.ProtoConnectionType()
	result, err := p.drainWAL(walogm, protocolConnectionType, startDate)
	if err != nil {
		return fmt.Errorf("failed draining interaction WAL: %v", err)
	}

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
			Sequence:  sequence,
			ExitCode:  exitCode,
			CreatedAt: *startDate,
			EndedAt:   &endDate,
		},
		nil, // TODO: capture interaction input when input tracking is wired
		json.RawMessage(result.rawJSONBlobStream),
		blobFormat,
	)

	log.With("sid", pctx.SID, "sequence", sequence).
		Infof("finished persisting interaction, err=%v", err)

	if err == nil {
		if removeErr := os.RemoveAll(walogm.folderName); removeErr != nil {
			log.Errorf("failed removing interaction wal file %q, err=%v", walogm.folderName, removeErr)
		}
	}

	// increment masked metrics for this interaction
	if maskedErr := models.IncrementSessionMaskedMetrics(models.DB, pctx.SID, result.metrics.DataMasking.InfoTypes); maskedErr != nil {
		log.With("sid", pctx.SID).Warnf("failed incrementing session masked metrics for interaction, reason=%v", maskedErr)
	}

	return err
}

func parseExitCodeFromInteractionPacket(pkt *pb.Packet) *int {
	exitCodeStr := string(pkt.Spec[pb.SpecClientExitCodeKey])
	if exitCodeStr == "" {
		v := 0
		return &v
	}
	ecode, err := strconv.Atoi(exitCodeStr)
	if err != nil {
		v := 254
		return &v
	}
	return &ecode
}

func (p *auditPlugin) writeOnClose(pctx plugintypes.Context, errMsg error) error {
	walFolder := fmt.Sprintf(walFolderTmpl, plugintypes.AuditPath, pctx.OrgID, pctx.SID)
	if wal.FileNotExists(walFolder) {
		// if the wal file does not exist we neet to update the session event stream
		blobStream := fmt.Sprintf(`[ [0, "%s", "%s"] ]`, "e", base64.StdEncoding.EncodeToString([]byte("no log found on disk")))
		emptyMetrics := make(map[string]any, 0)

		var blobFormat *string
		switch pctx.ProtoConnectionType() {
		case pb.ConnectionTypePostgres:
			blobFormat = ptr.String(models.BlobFormatWireProtoType)
		}

		endDate := time.Now().UTC()

		trackClient := analytics.New()
		defer trackClient.Close()

		err := models.UpdateSessionEventStream(models.SessionDone{
			ID:         pctx.SID,
			OrgID:      pctx.OrgID,
			Metrics:    emptyMetrics,
			BlobStream: json.RawMessage(blobStream),
			BlobFormat: blobFormat,
			Status:     string(openapi.SessionStatusDone),
			ExitCode:   parseExitCodeFromErr(errMsg),
			EndSession: &endDate,
		})

		trackClient.TrackSessionUsageData(analytics.EventSessionFinished, pctx.OrgID, pctx.UserID, pctx.SID)
		log.With("sid", pctx.SID, "origin", pctx.ClientOrigin, "verb", pctx.ClientVerb).
			Infof("finished persisting session to store (empty log), update-session-err=%v, context-err=%v", err, errMsg)

		if err != nil {
			log.With("sid", pctx.SID).Warnf("failed updating session event stream: %v", err)
		}

		log.With("sid", pctx.SID).Debugf("no wal log found on disk for session, path=%v, err=%v", walFolder, err)
		p.walSessionStore.Pop(pctx.SID)
		return err

	}
	// first we gonna try to obtain the wal from memory because could have unflushed logs from memory to disk
	walLogObjMemory := p.walSessionStore.Pop(pctx.SID)
	walogm, ok := walLogObjMemory.(*walLogRWMutex)

	if !ok {
		log.With("sid", pctx.SID).Warnf("failed obtaining write ahead log from memory for session %v", pctx.SID)
		// then if not found on memory we try to open from disk
		// so we can be sure everything is flushed to disk
		walog, _, err := sessionwal.OpenWithHeader(walFolder)
		if err != nil {
			return fmt.Errorf("failed opening wal file to read header, err=%v", err)
		}

		walogm = &walLogRWMutex{
			log:        walog,
			mu:         sync.RWMutex{},
			folderName: walFolder,
		}
	}

	walogm.mu.Lock()
	defer func() { _ = walogm.log.Close(); walogm.mu.Unlock() }()
	if errMsg != nil && errMsg != io.EOF {
		err := walogm.log.Write(eventlogv1.New(time.Now().UTC(), eventlogv1.ErrorType, []byte(errMsg.Error()), nil))
		if err != nil {
			log.With("sid", pctx.SID).Warnf("failed writing end error message, err=%v", err)
		}
	}

	wh, err := walogm.log.Header()
	if err != nil {
		return fmt.Errorf("failed decoding wal header object, err=%v", err)
	}
	if wh.SessionID != pctx.SID {
		return fmt.Errorf("mismatch wal header session id, session=%v, session-header=%v",
			pctx.SID, wh.SessionID)
	}
	protocolConnectionType := pctx.ProtoConnectionType()

	result, err := p.drainWAL(walogm, protocolConnectionType, wh.StartDate)
	if err != nil {
		return err
	}

	var blobFormat *string
	switch protocolConnectionType {
	case pb.ConnectionTypePostgres:
		blobFormat = ptr.String(models.BlobFormatWireProtoType)
	}

	endDate := time.Now().UTC()
	sessionMetrics, err := result.metrics.toMap()
	if err != nil {
		log.With("sid", pctx.SID).Warnf("failed parsing session metrics to map, reason=%v", err)
	}

	err = models.UpdateSessionEventStream(models.SessionDone{
		ID:         wh.SessionID,
		OrgID:      wh.OrgID,
		Metrics:    sessionMetrics,
		BlobStream: json.RawMessage(result.rawJSONBlobStream),
		BlobFormat: blobFormat,
		Status:     string(openapi.SessionStatusDone),
		ExitCode:   parseExitCodeFromErr(errMsg),
		EndSession: &endDate,
	})
	log.With("sid", pctx.SID, "origin", pctx.ClientOrigin, "verb", pctx.ClientVerb).
		Infof("finished persisting session to store, update-session-err=%v, context-err=%v", err, errMsg)

	err = models.IncrementSessionMaskedMetrics(models.DB, wh.SessionID, result.metrics.DataMasking.InfoTypes)
	if err != nil {
		log.With("sid", pctx.SID).Warnf("failed incrementing session masked metrics, reason=%v", err)
	}

	if err != nil {
		_ = walogm.log.Write(eventlogv1.NewCommitError(endDate, err.Error()))
	} else {
		if err := os.RemoveAll(walogm.folderName); err != nil {
			log.Errorf("failed removing wal file %q, err=%v", walogm.folderName, err)
		}
	}
	return err
}

func (p *auditPlugin) truncateTCPEventStream(eventStream []byte, protoConnType pb.ConnectionType) []byte {
	if len(eventStream) > 5000 && protoConnType == pb.ConnectionTypeTCP {
		return eventStream[0:5000]
	}
	return eventStream
}

func parseExitCodeFromErr(err error) (exitCode *int) {
	switch v := err.(type) {
	case *plugintypes.PacketErr:
		exitCode = v.ExitCode()
	case nil:
		exitCode = func() *int { v := 0; return &v }()
	default:
		exitCode = internalExitCode
	}
	return
}
